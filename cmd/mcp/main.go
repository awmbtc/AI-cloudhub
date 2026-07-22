// mcp — AI-cloudhub agent helper (MCP-compatible-ish over stdio).
//
// Reads AI_CLOUDHUB_API + AI_CLOUDHUB_TOKEN and speaks JSON-RPC 2.0
// (one JSON object per line) on stdin/stdout.
//
// Tools enforce:
//   - required scopes (from GET /v1/me when agent token)
//   - path jail under AI_CLOUDHUB_WORKSPACE / mount root
//
// See docs/MCP.md.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/auth"
	"github.com/awmbtc/AI-cloudhub/internal/sandbox"
)

const serverName = "ai-cloudhub-mcp"
const serverVersion = "0.2.0"

type principalCache struct {
	mu       sync.Mutex
	loaded   bool
	agentID  string
	scopes   []string
	role     string
	username string
	userID   string
	err      error
}

func main() {
	api := strings.TrimRight(env("AI_CLOUDHUB_API", "http://127.0.0.1:8080"), "/")
	token := os.Getenv("AI_CLOUDHUB_TOKEN")
	workspace := env("AI_CLOUDHUB_WORKSPACE", env("AI_CLOUDHUB_MOUNT", "/workspace"))

	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "ai-cloudhub-mcp: "+format+"\n", args...)
	}
	logf("starting api=%s token_set=%v workspace=%s", api, token != "", workspace)

	if token == "" {
		logf("WARNING: AI_CLOUDHUB_TOKEN unset — API tools will fail until set")
	}

	pc := &principalCache{}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		resp := handleLine(api, token, workspace, pc, line)
		if resp == nil {
			continue
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(resp); err != nil {
			logf("encode: %v", err)
			return
		}
	}
	if err := sc.Err(); err != nil {
		logf("stdin: %v", err)
		os.Exit(1)
	}
}

// ---- JSON-RPC ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func handleLine(api, token, workspace string, pc *principalCache, line string) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return errResp(nil, -32700, "parse error: "+err.Error(), nil)
	}
	if req.JSONRPC == "" {
		req.JSONRPC = "2.0"
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		if req.Method == "notifications/initialized" || req.Method == "initialized" {
			return nil
		}
		return nil
	}
	var id interface{}
	_ = json.Unmarshal(req.ID, &id)

	switch req.Method {
	case "initialize":
		return okResp(id, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": serverName, "version": serverVersion},
			"instructions": "AI-cloudhub MCP helper v0.2. Tools require scopes when using agent tokens. " +
				"Paths must stay under workspace. Prefer hubd/runner for mounts.",
		})
	case "ping":
		return okResp(id, map[string]interface{}{})
	case "tools/list":
		return okResp(id, map[string]interface{}{"tools": toolDescriptors()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return errResp(id, -32602, "invalid tools/call params: "+err.Error(), nil)
			}
		}
		result, err := callTool(api, token, workspace, pc, p.Name, p.Arguments)
		if err != nil {
			return okResp(id, toolResult(true, err.Error()))
		}
		return okResp(id, result)
	case "list_drives", "ensure_mounted_hint", "workspace_env", "resolve_path", "list_snapshots", "create_snapshot", "whoami", "list_objects", "object_restore_plan", "object_presign_get", "object_restore_version":
		result, err := callTool(api, token, workspace, pc, req.Method, req.Params)
		if err != nil {
			return okResp(id, toolResult(true, err.Error()))
		}
		return okResp(id, result)
	default:
		return errResp(id, -32601, "method not found: "+req.Method, nil)
	}
}

func okResp(id interface{}, result interface{}) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id interface{}, code int, msg string, data interface{}) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}}
}

// ---- Tool registry with required scopes ----

type toolMeta struct {
	name        string
	description string
	scopes      []string // any-of for agent tokens; empty = human or any authenticated
	schema      map[string]interface{}
}

func toolRegistry() []toolMeta {
	return []toolMeta{
		{
			name: "whoami", description: "Return principal from control plane (GET /v1/me): human vs agent, scopes.",
			scopes: nil,
			schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			name: "list_drives", description: "List logical drives (GET /v1/drives). Requires drive.read for agent tokens.",
			scopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
			schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			name: "ensure_mounted_hint",
			description: "Mount instructions + optional session probe. Requires drive.read. " +
				"mount_point must be under workspace jail.",
			scopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id":    map[string]interface{}{"type": "string"},
					"binding_id":  map[string]interface{}{"type": "string"},
					"mount_point": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			name: "workspace_env", description: "Document AI_CLOUDHUB_* env contract (local, no API).",
			scopes: nil,
			schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			name: "resolve_path", description: "Check whether a path is inside the workspace jail (local).",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Path relative or absolute"},
				},
				"required": []string{"path"},
			},
		},
		{
			name: "list_snapshots", description: "List metadata snapshots for a drive. Requires drive.read.",
			scopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"drive_id"},
			},
		},
		{
			name: "create_snapshot", description: "Create metadata snapshot. Requires drive.write.",
			scopes: []string{auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id": map[string]interface{}{"type": "string"},
					"label":    map[string]interface{}{"type": "string"},
					"note":     map[string]interface{}{"type": "string"},
				},
				"required": []string{"drive_id"},
			},
		},
		{
			name: "list_objects", description: "Live object inventory for a drive (metadata). Requires drive.read.",
			scopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id": map[string]interface{}{"type": "string"},
					"versions": map[string]interface{}{"type": "boolean", "description": "Include version ids if bucket versioning on"},
					"max":      map[string]interface{}{"type": "integer"},
				},
				"required": []string{"drive_id"},
			},
		},
		{
			name: "object_restore_plan", description: "BYOS restore guidance: CLI hint + optional presign GET + api_restore path. Requires drive.read.",
			scopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id":   map[string]interface{}{"type": "string"},
					"key":        map[string]interface{}{"type": "string"},
					"version_id": map[string]interface{}{"type": "string"},
					"ttl_min":    map[string]interface{}{"type": "integer", "description": "Presign TTL minutes (default 15)"},
				},
				"required": []string{"drive_id", "key"},
			},
		},
		{
			name: "object_presign_get", description: "Short-lived presigned GET (optional versionId). Bytes client↔store only. Requires drive.read.",
			scopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id":   map[string]interface{}{"type": "string"},
					"key":        map[string]interface{}{"type": "string"},
					"version_id": map[string]interface{}{"type": "string"},
					"ttl_min":    map[string]interface{}{"type": "integer"},
				},
				"required": []string{"drive_id", "key"},
			},
		},
		{
			name: "object_restore_version", description: "Server-side S3 CopyObject version→current on BYOS (no body proxy). Requires drive.write + bucket versioning.",
			scopes: []string{auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id":   map[string]interface{}{"type": "string"},
					"key":        map[string]interface{}{"type": "string"},
					"version_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"drive_id", "key", "version_id"},
			},
		},
	}
}

func toolDescriptors() []map[string]interface{} {
	var out []map[string]interface{}
	for _, t := range toolRegistry() {
		out = append(out, map[string]interface{}{
			"name":        t.name,
			"description": t.description,
			"inputSchema": t.schema,
			"annotations": map[string]interface{}{
				"required_scopes_any": t.scopes,
			},
		})
	}
	return out
}

func metaFor(name string) (toolMeta, bool) {
	for _, t := range toolRegistry() {
		if t.name == name {
			return t, true
		}
	}
	return toolMeta{}, false
}

func callTool(api, token, workspace string, pc *principalCache, name string, argsJSON json.RawMessage) (interface{}, error) {
	meta, ok := metaFor(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	// Scope gate for tools that hit the API or declare scopes.
	if len(meta.scopes) > 0 || name == "whoami" {
		if err := ensureScopes(api, token, pc, meta.scopes); err != nil {
			return nil, err
		}
	}

	switch name {
	case "whoami":
		return toolWhoami(api, token, pc)
	case "list_drives":
		return toolListDrives(api, token)
	case "ensure_mounted_hint":
		var args struct {
			DriveID    string `json:"drive_id"`
			BindingID  string `json:"binding_id"`
			MountPoint string `json:"mount_point"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if args.MountPoint != "" {
			if err := jailPath(workspace, args.MountPoint); err != nil {
				return nil, err
			}
		}
		return toolEnsureMountedHint(api, token, workspace, args.DriveID, args.BindingID, args.MountPoint)
	case "workspace_env":
		return toolWorkspaceEnv(workspace), nil
	case "resolve_path":
		var args struct {
			Path string `json:"path"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		return toolResolvePath(workspace, args.Path)
	case "list_snapshots":
		var args struct {
			DriveID string `json:"drive_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.DriveID) == "" {
			return nil, fmt.Errorf("drive_id required")
		}
		return toolListSnapshots(api, token, args.DriveID)
	case "create_snapshot":
		var args struct {
			DriveID string `json:"drive_id"`
			Label   string `json:"label"`
			Note    string `json:"note"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.DriveID) == "" {
			return nil, fmt.Errorf("drive_id required")
		}
		return toolCreateSnapshot(api, token, args.DriveID, args.Label, args.Note)
	case "list_objects":
		var args struct {
			DriveID  string `json:"drive_id"`
			Versions bool   `json:"versions"`
			Max      int    `json:"max"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.DriveID) == "" {
			return nil, fmt.Errorf("drive_id required")
		}
		return toolListObjects(api, token, args.DriveID, args.Versions, args.Max)
	case "object_restore_plan":
		var args struct {
			DriveID   string `json:"drive_id"`
			Key       string `json:"key"`
			VersionID string `json:"version_id"`
			TTLMin    int    `json:"ttl_min"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.DriveID) == "" || strings.TrimSpace(args.Key) == "" {
			return nil, fmt.Errorf("drive_id and key required")
		}
		return toolObjectPost(api, token, args.DriveID, "restore-plan", map[string]interface{}{
			"key": args.Key, "version_id": args.VersionID, "ttl_min": args.TTLMin,
		})
	case "object_presign_get":
		var args struct {
			DriveID   string `json:"drive_id"`
			Key       string `json:"key"`
			VersionID string `json:"version_id"`
			TTLMin    int    `json:"ttl_min"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.DriveID) == "" || strings.TrimSpace(args.Key) == "" {
			return nil, fmt.Errorf("drive_id and key required")
		}
		return toolObjectPost(api, token, args.DriveID, "presign-get", map[string]interface{}{
			"key": args.Key, "version_id": args.VersionID, "ttl_min": args.TTLMin,
		})
	case "object_restore_version":
		var args struct {
			DriveID   string `json:"drive_id"`
			Key       string `json:"key"`
			VersionID string `json:"version_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.DriveID) == "" || strings.TrimSpace(args.Key) == "" || strings.TrimSpace(args.VersionID) == "" {
			return nil, fmt.Errorf("drive_id, key, version_id required")
		}
		return toolObjectPost(api, token, args.DriveID, "restore-version", map[string]interface{}{
			"key": args.Key, "version_id": args.VersionID,
		})
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func decodeArgs(raw json.RawMessage, v interface{}) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func jailPath(workspace, path string) error {
	j := sandbox.NewPathJail(workspace)
	return j.Allow(path)
}

func ensureScopes(api, token string, pc *principalCache, needAny []string) error {
	if token == "" {
		return fmt.Errorf("AI_CLOUDHUB_TOKEN is not set")
	}
	if len(needAny) == 0 {
		return nil
	}
	if err := loadPrincipal(api, token, pc); err != nil {
		return err
	}
	// Human (no agent_id): full access
	if pc.agentID == "" {
		return nil
	}
	for _, need := range needAny {
		if auth.HasScope(pc.agentID, pc.scopes, need) {
			return nil
		}
	}
	return fmt.Errorf("missing scope (need any of %v); agent scopes=%v", needAny, pc.scopes)
}

func loadPrincipal(api, token string, pc *principalCache) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.loaded {
		return pc.err
	}
	pc.loaded = true
	body, status, err := httpDo(http.MethodGet, api+"/v1/me", token, nil)
	if err != nil {
		pc.err = err
		return err
	}
	if status >= 300 {
		pc.err = fmt.Errorf("GET /v1/me HTTP %d: %s", status, truncate(string(body), 256))
		return pc.err
	}
	var me struct {
		ID       string   `json:"id"`
		Username string   `json:"username"`
		Role     string   `json:"role"`
		AgentID  string   `json:"agent_id"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		pc.err = err
		return err
	}
	pc.userID = me.ID
	pc.username = me.Username
	pc.role = me.Role
	pc.agentID = me.AgentID
	pc.scopes = me.Scopes
	return nil
}

func toolWhoami(api, token string, pc *principalCache) (interface{}, error) {
	if err := loadPrincipal(api, token, pc); err != nil {
		return nil, err
	}
	return toolResultJSON(map[string]interface{}{
		"user_id":   pc.userID,
		"username":  pc.username,
		"role":      pc.role,
		"agent_id":  pc.agentID,
		"scopes":    pc.scopes,
		"principal": map[bool]string{true: "agent", false: "human"}[pc.agentID != ""],
	})
}

func toolResult(isError bool, text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func toolResultJSON(v interface{}) (map[string]interface{}, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return toolResult(false, strings.TrimSpace(buf.String())), nil
}

func toolListDrives(api, token string) (interface{}, error) {
	body, status, err := httpDo(http.MethodGet, api+"/v1/drives", token, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("GET /v1/drives HTTP %d: %s", status, truncate(string(body), 512))
	}
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return toolResult(false, string(body)), nil
	}
	return toolResultJSON(map[string]interface{}{
		"drives": parsed,
		"hint":   "Use ensure_mounted_hint with drive_id; hubd/runner for actual mount.",
	})
}

func toolEnsureMountedHint(api, token, workspace, driveID, bindingID, mountPoint string) (interface{}, error) {
	if mountPoint == "" {
		mountPoint = workspace
	}
	if err := jailPath(workspace, mountPoint); err != nil {
		return nil, fmt.Errorf("mount_point jail: %w", err)
	}
	out := map[string]interface{}{
		"action":      "ensure_mounted_hint",
		"mount_point": mountPoint,
		"workspace":   workspace,
		"instructions": []string{
			"This MCP helper does not mount FUSE locally.",
			"Desktop: hubd with AI_CLOUDHUB_API + TOKEN + DEVICE_ID.",
			"Cloud BYOC: runner with DRIVE_ID or BINDING_ID (D-001: no platform pool).",
			"Write artifacts only under workspace (path jail enforced by runner).",
		},
		"commands": map[string]string{
			"hubd":   fmt.Sprintf("AI_CLOUDHUB_API=%s AI_CLOUDHUB_TOKEN=<token> AI_CLOUDHUB_DEVICE_ID=<device> ./.bin/hubd", api),
			"runner": fmt.Sprintf("AI_CLOUDHUB_API=%s AI_CLOUDHUB_TOKEN=<token> AI_CLOUDHUB_DRIVE_ID=<drive_id> AI_CLOUDHUB_MOUNT=%s ./.bin/runner -- <agent>", api, mountPoint),
		},
	}
	if bindingID != "" || driveID != "" {
		var url string
		var payload interface{}
		if bindingID != "" {
			url = api + "/v1/bindings/" + bindingID + "/session"
			out["binding_id"] = bindingID
		} else {
			url = api + "/v1/drives/" + driveID + "/session"
			out["drive_id"] = driveID
			payload = map[string]string{
				"mount_point": mountPoint,
				"device_id":   env("AI_CLOUDHUB_DEVICE_ID", "mcp-helper"),
				"mode":        env("AI_CLOUDHUB_MODE", "mount"),
			}
		}
		body, status, err := httpDo(http.MethodPost, url, token, payload)
		probe := map[string]interface{}{"url": url, "status": status}
		if err != nil {
			probe["ok"] = false
			probe["error"] = err.Error()
		} else if status >= 300 {
			probe["ok"] = false
			probe["error"] = truncate(string(body), 512)
		} else {
			probe["ok"] = true
			var parsed map[string]interface{}
			if json.Unmarshal(body, &parsed) == nil {
				probe["summary"] = sessionSummary(parsed)
			}
		}
		out["session_probe"] = probe
	}
	return toolResultJSON(out)
}

func toolResolvePath(workspace, path string) (interface{}, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path required")
	}
	err := jailPath(workspace, path)
	return toolResultJSON(map[string]interface{}{
		"workspace": workspace,
		"path":      path,
		"allowed":   err == nil,
		"error":     errString(err),
	})
}

func toolListSnapshots(api, token, driveID string) (interface{}, error) {
	body, status, err := httpDo(http.MethodGet, api+"/v1/drives/"+driveID+"/snapshots", token, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("list snapshots HTTP %d: %s", status, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolCreateSnapshot(api, token, driveID, label, note string) (interface{}, error) {
	payload := map[string]string{"label": label, "note": note}
	body, status, err := httpDo(http.MethodPost, api+"/v1/drives/"+driveID+"/snapshots", token, payload)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("create snapshot HTTP %d: %s", status, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolListObjects(api, token, driveID string, versions bool, max int) (interface{}, error) {
	url := api + "/v1/drives/" + driveID + "/objects"
	q := []string{}
	if versions {
		q = append(q, "versions=1")
	}
	if max > 0 {
		q = append(q, fmt.Sprintf("max=%d", max))
	}
	if len(q) > 0 {
		url += "?" + strings.Join(q, "&")
	}
	body, status, err := httpDo(http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("list objects HTTP %d: %s", status, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

// toolObjectPost POSTs to /v1/drives/{id}/objects/{action} (presign-get, restore-plan, restore-version).
func toolObjectPost(api, token, driveID, action string, payload map[string]interface{}) (interface{}, error) {
	url := api + "/v1/drives/" + driveID + "/objects/" + action
	body, status, err := httpDo(http.MethodPost, url, token, payload)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("%s HTTP %d: %s", action, status, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func sessionSummary(parsed map[string]interface{}) map[string]interface{} {
	sum := map[string]interface{}{}
	if m, ok := parsed["manifest"].(map[string]interface{}); ok {
		sum["has_manifest"] = true
		if env, ok := m["env"].(map[string]interface{}); ok {
			sum["manifest_env_keys"] = mapKeys(env)
		}
		if v, ok := m["version"]; ok {
			sum["manifest_version"] = v
		}
		if p, ok := m["permissions"]; ok {
			sum["permissions"] = p
		}
	}
	if s, ok := parsed["session"].(map[string]interface{}); ok {
		if exp, ok := s["expires_at"]; ok {
			sum["expires_at"] = exp
		}
	}
	return sum
}

func toolWorkspaceEnv(workspace string) interface{} {
	doc := map[string]interface{}{
		"workspace_default": workspace,
		"required_env": []map[string]string{
			{"name": "AI_CLOUDHUB_WORKSPACE", "meaning": "Absolute workspace root"},
			{"name": "AI_CLOUDHUB_DRIVE_ID", "meaning": "Logical drive id"},
			{"name": "AI_CLOUDHUB_MODE", "meaning": "mount | sync_workspace | direct"},
		},
		"security": map[string]interface{}{
			"mcp_scopes":  "Agent tokens need drive.read / drive.write for API tools",
			"path_jail":   "resolve_path and mount_point checked against workspace",
			"runner_env":  "runner filters secrets; set AI_CLOUDHUB_PASS_TOKEN=1 to pass API token into agent",
			"mcp_version": serverVersion,
		},
		"tools": []string{"whoami", "list_drives", "ensure_mounted_hint", "workspace_env", "resolve_path", "list_snapshots", "create_snapshot"},
	}
	out, err := toolResultJSON(doc)
	if err != nil {
		return toolResult(false, fmt.Sprintf("%v", doc))
	}
	return out
}

func httpDo(method, url, token string, payload interface{}) ([]byte, int, error) {
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, res.StatusCode, err
	}
	return body, res.StatusCode, nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
