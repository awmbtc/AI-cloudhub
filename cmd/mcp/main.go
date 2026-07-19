// mcp — AI-cloudhub agent helper (MCP-compatible-ish over stdio).
//
// Reads AI_CLOUDHUB_API + AI_CLOUDHUB_TOKEN and speaks JSON-RPC 2.0
// (one JSON object per line) on stdin/stdout. Tools:
//
//	list_drives
//	ensure_mounted_hint
//	workspace_env
//
// This is a skeleton for agents — not a full MCP SDK host.
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
	"time"
)

const serverName = "ai-cloudhub-mcp"
const serverVersion = "0.1.0"

func main() {
	api := strings.TrimRight(env("AI_CLOUDHUB_API", "http://127.0.0.1:8080"), "/")
	token := os.Getenv("AI_CLOUDHUB_TOKEN")

	// Logs go to stderr so stdout stays clean for JSON-RPC.
	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "ai-cloudhub-mcp: "+format+"\n", args...)
	}
	logf("starting api=%s token_set=%v", api, token != "")

	sc := bufio.NewScanner(os.Stdin)
	// Allow large tool results (default 64K is tight for drive lists).
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		resp := handleLine(api, token, line)
		if resp == nil {
			// notifications (no id) → no response
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

// ---- JSON-RPC types ----

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

func handleLine(api, token, line string) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return errResp(nil, -32700, "parse error: "+err.Error(), nil)
	}
	if req.JSONRPC == "" {
		req.JSONRPC = "2.0"
	}

	// Notifications have no id → no response.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		if req.Method == "notifications/initialized" || req.Method == "initialized" {
			return nil
		}
		// Still process fire-and-forget if useful; currently ignore.
		return nil
	}

	var id interface{}
	_ = json.Unmarshal(req.ID, &id)

	switch req.Method {
	case "initialize":
		return okResp(id, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    serverName,
				"version": serverVersion,
			},
			"instructions": "AI-cloudhub MCP helper. Use list_drives, ensure_mounted_hint, workspace_env. Artifacts go under AI_CLOUDHUB_WORKSPACE.",
		})

	case "ping":
		return okResp(id, map[string]interface{}{})

	case "tools/list":
		return okResp(id, map[string]interface{}{
			"tools": toolDescriptors(),
		})

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
		result, err := callTool(api, token, p.Name, p.Arguments)
		if err != nil {
			// MCP-style tool error as result content, not transport error.
			return okResp(id, toolResult(true, err.Error()))
		}
		return okResp(id, result)

	// Direct tool methods (line-protocol convenience).
	case "list_drives", "ensure_mounted_hint", "workspace_env":
		result, err := callTool(api, token, req.Method, req.Params)
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
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg, Data: data},
	}
}

// ---- Tools ----

func toolDescriptors() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "list_drives",
			"description": "List logical drives for the authenticated AI-cloudhub user (GET /v1/drives).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name": "ensure_mounted_hint",
			"description": "Return mount / ensure-mounted instructions for agents. " +
				"If drive_id or binding_id is provided, probes the control plane session endpoint. " +
				"Does not mount locally — points at hubd/runner.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional drive id; probes POST /v1/drives/{id}/session",
					},
					"binding_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional binding id; probes POST /v1/bindings/{id}/session",
					},
					"mount_point": map[string]interface{}{
						"type":        "string",
						"description": "Desired mount point (default /workspace or AI_CLOUDHUB_MOUNT)",
					},
				},
			},
		},
		{
			"name":        "workspace_env",
			"description": "Return AI_CLOUDHUB_* environment variable names and meanings from the Workspace Manifest contract.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func callTool(api, token, name string, argsJSON json.RawMessage) (interface{}, error) {
	switch name {
	case "list_drives":
		return toolListDrives(api, token)
	case "ensure_mounted_hint":
		var args struct {
			DriveID    string `json:"drive_id"`
			BindingID  string `json:"binding_id"`
			MountPoint string `json:"mount_point"`
		}
		if len(argsJSON) > 0 && string(argsJSON) != "null" {
			if err := json.Unmarshal(argsJSON, &args); err != nil {
				return nil, fmt.Errorf("bad arguments: %w", err)
			}
		}
		return toolEnsureMountedHint(api, token, args.DriveID, args.BindingID, args.MountPoint)
	case "workspace_env":
		return toolWorkspaceEnv(), nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func toolResult(isError bool, text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
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
	// Encode appends a trailing newline; trim for cleaner tool text.
	return toolResult(false, strings.TrimSpace(buf.String())), nil
}

func toolListDrives(api, token string) (interface{}, error) {
	if token == "" {
		return nil, fmt.Errorf("AI_CLOUDHUB_TOKEN is not set")
	}
	body, status, err := httpDo(http.MethodGet, api+"/v1/drives", token, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("GET /v1/drives HTTP %d: %s", status, truncate(string(body), 512))
	}
	// Pass through JSON (array or wrapped object).
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return toolResult(false, string(body)), nil
	}
	return toolResultJSON(map[string]interface{}{
		"drives": parsed,
		"hint":   "Use ensure_mounted_hint with a drive_id, then hubd (desktop) or runner (BYOC) to mount.",
	})
}

func toolEnsureMountedHint(api, token, driveID, bindingID, mountPoint string) (interface{}, error) {
	if mountPoint == "" {
		mountPoint = env("AI_CLOUDHUB_MOUNT", "/workspace")
	}
	out := map[string]interface{}{
		"action":      "ensure_mounted_hint",
		"mount_point": mountPoint,
		"instructions": []string{
			"AI-cloudhub does not auto-mount from this MCP helper.",
			"Desktop: run hubd with AI_CLOUDHUB_API + AI_CLOUDHUB_TOKEN + AI_CLOUDHUB_DEVICE_ID (reconciles bindings desired=mounted).",
			"Cloud BYOC: run runner with AI_CLOUDHUB_DRIVE_ID or AI_CLOUDHUB_BINDING_ID (never a platform runner pool).",
			"After mount, write all artifacts under AI_CLOUDHUB_WORKSPACE (see workspace_env tool / manifest env).",
			"Manifest path: $AI_CLOUDHUB_WORKSPACE/.ai-cloudhub/manifest.json",
		},
		"commands": map[string]string{
			"hubd": fmt.Sprintf(
				"AI_CLOUDHUB_API=%s AI_CLOUDHUB_TOKEN=<token> AI_CLOUDHUB_DEVICE_ID=<device> ./.bin/hubd",
				api,
			),
			"runner": fmt.Sprintf(
				"AI_CLOUDHUB_API=%s AI_CLOUDHUB_TOKEN=<token> AI_CLOUDHUB_DRIVE_ID=<drive_id> AI_CLOUDHUB_MOUNT=%s ./.bin/runner -- <agent>",
				api, mountPoint,
			),
		},
	}

	// Optional live probe.
	if bindingID != "" || driveID != "" {
		if token == "" {
			out["session_probe"] = map[string]interface{}{
				"ok":    false,
				"error": "AI_CLOUDHUB_TOKEN is not set; cannot probe session",
			}
		} else {
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
			probe := map[string]interface{}{
				"url":    url,
				"status": status,
			}
			if err != nil {
				probe["ok"] = false
				probe["error"] = err.Error()
			} else if status >= 300 {
				probe["ok"] = false
				probe["error"] = truncate(string(body), 512)
			} else {
				probe["ok"] = true
				// Summarize without dumping rclone secrets in full if huge.
				var parsed map[string]interface{}
				if json.Unmarshal(body, &parsed) == nil {
					probe["summary"] = sessionSummary(parsed)
				} else {
					probe["body_bytes"] = len(body)
				}
				probe["note"] = "Session issued successfully. Prefer hubd/runner to mount; this helper only probes."
			}
			out["session_probe"] = probe
		}
	} else {
		out["note"] = "Pass drive_id or binding_id to probe POST .../session against the control plane."
	}

	return toolResultJSON(out)
}

func sessionSummary(parsed map[string]interface{}) map[string]interface{} {
	sum := map[string]interface{}{}
	// Common shapes from api/runner: top-level or nested under "session".
	if m, ok := parsed["manifest"].(map[string]interface{}); ok {
		sum["has_manifest"] = true
		if env, ok := m["env"].(map[string]interface{}); ok {
			sum["manifest_env_keys"] = mapKeys(env)
		}
		if mp, ok := m["mount_point"].(string); ok {
			sum["mount_point"] = mp
		}
		if did, ok := m["drive_id"].(string); ok {
			sum["drive_id"] = did
		}
	}
	if s, ok := parsed["session"].(map[string]interface{}); ok {
		if exp, ok := s["expires_at"]; ok {
			sum["expires_at"] = exp
		}
		if m, ok := s["manifest"].(map[string]interface{}); ok {
			if env, ok := m["env"].(map[string]interface{}); ok {
				sum["session_manifest_env_keys"] = mapKeys(env)
			}
		}
	}
	if sp, ok := parsed["spec"].(map[string]interface{}); ok {
		if rp, ok := sp["remote_path"].(string); ok {
			sum["remote_path"] = rp
		}
		if mp, ok := sp["mount_point"].(string); ok {
			sum["spec_mount_point"] = mp
		}
		if _, ok := sp["rclone_conf"].(string); ok {
			sum["has_rclone_conf"] = true
		}
	}
	return sum
}

func toolWorkspaceEnv() interface{} {
	// From protocols/workspace-manifest.schema.json + internal/manifest + ARCHITECTURE §8.
	doc := map[string]interface{}{
		"source": []string{
			"protocols/workspace-manifest.schema.json",
			"internal/manifest/manifest.go",
			"docs/ARCHITECTURE.md §8",
		},
		"required_env": []map[string]string{
			{"name": "AI_CLOUDHUB_WORKSPACE", "meaning": "Absolute workspace root; all agent artifacts MUST land here."},
			{"name": "AI_CLOUDHUB_DRIVE_ID", "meaning": "Logical drive id bound to this workspace."},
			{"name": "AI_CLOUDHUB_MODE", "meaning": "mount | sync_workspace | direct"},
		},
		"common_env": []map[string]string{
			{"name": "AI_CLOUDHUB_API", "meaning": "Control-plane base URL."},
			{"name": "AI_CLOUDHUB_TOKEN", "meaning": "Bearer token for API / MCP helper (not always in manifest)."},
			{"name": "AI_CLOUDHUB_MOUNT", "meaning": "Desired mount point for hubd/runner (default /workspace)."},
			{"name": "AI_CLOUDHUB_DEVICE_ID", "meaning": "Device id for hubd binding reconcile."},
			{"name": "AI_CLOUDHUB_BINDING_ID", "meaning": "Optional binding id for runner session."},
			{"name": "AI_CLOUDHUB_STORAGE_REGION", "meaning": "Optional storage region hint from drive."},
			{"name": "AI_CLOUDHUB_JOB_ID", "meaning": "Set by runner worker when executing a job."},
			{"name": "AI_CLOUDHUB_WORKER", "meaning": "Set 1/true to run runner in job poll mode."},
		},
		"manifest_path": "$AI_CLOUDHUB_WORKSPACE/.ai-cloudhub/manifest.json",
		"agent_policy": map[string]interface{}{
			"deny_upload_tools": true,
			"instructions":      "All artifacts MUST be written under AI_CLOUDHUB_WORKSPACE. Do not use cloud upload APIs.",
		},
	}
	// toolWorkspaceEnv always succeeds; ignore marshal edge cases.
	out, err := toolResultJSON(doc)
	if err != nil {
		return toolResult(false, fmt.Sprintf("%v", doc))
	}
	return out
}

// ---- HTTP ----

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
