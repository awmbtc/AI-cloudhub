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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/auth"
	"github.com/awmbtc/AI-cloudhub/internal/sandbox"
)

const serverName = "ai-cloudhub-mcp"
// Keep in sync with internal/version.Version / release tags.
const serverVersion = "0.2.27"

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
	case "list_drives", "list_bindings", "ensure_mounted_hint", "workspace_env", "resolve_path", "list_snapshots", "create_snapshot", "whoami", "list_objects", "object_restore_plan", "object_presign_get", "object_restore_version", "list_jobs", "job_stats", "get_job", "create_job", "claim_next_job", "complete_job", "heartbeat_job", "cancel_job", "list_providers",
		"list_marketplace", "install_marketplace", "list_memory", "put_memory", "search_memory", "list_graph", "link_graph", "list_connectors", "connectors_catalog", "create_connector", "get_connector", "delete_connector", "marketplace_checkout", "list_lineage", "record_lineage":
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
			name: "list_bindings", description: "List mount bindings (GET /v1/bindings). Optional device_id filter. Requires drive.read for agent tokens.",
			scopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional device filter (query device_id)"},
				},
			},
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
			name: "object_presign_get", description: "Short-lived GET URL (optional versionId). type=qiniu → method=qiniu_download (native HMAC); else S3 presign. Bytes client↔store only. Requires drive.read.",
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
		{
			name: "list_jobs", description: "List BYOC jobs. Filters: status, agent_id, claimed_by_agent_id, labels, limit, cursor. Keyset next_cursor. Requires job.run.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status":              map[string]interface{}{"type": "string", "description": "pending (claimable) or exact status"},
					"agent_id":            map[string]interface{}{"type": "string"},
					"claimed_by_agent_id": map[string]interface{}{"type": "string"},
					"region":              map[string]interface{}{"type": "string", "description": "Only with status=pending"},
					"labels":              map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
					"limit":               map[string]interface{}{"type": "integer", "description": "Page size default 100 max 500 (ignored for status=pending)"},
					"cursor":              map[string]interface{}{"type": "string", "description": "Opaque next_cursor from previous page"},
				},
			},
		},
		{
			name: "job_stats", description: "Per-status job counts (GET /v1/jobs/stats). Requires job.run for agents.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			name: "get_job", description: "Get one BYOC job by id (GET /v1/jobs/{id}). Includes exit_code/duration_ms when completed. Requires job.run.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"job_id"},
			},
		},
		{
			name: "create_job", description: "Enqueue BYOC job for user runners (D-001: no platform pool). Optional connector_id for git clone on runner. Requires job.run.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"drive_id":     map[string]interface{}{"type": "string"},
					"command":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"mode":         map[string]interface{}{"type": "string", "description": "mount | sync_workspace | direct"},
					"binding_id":   map[string]interface{}{"type": "string"},
					"region_hint":  map[string]interface{}{"type": "string"},
					"note":         map[string]interface{}{"type": "string"},
					"connector_id": map[string]interface{}{"type": "string", "description": "Optional Stage C connector (e.g. git) for runner materialization"},
					"timeout_sec":  map[string]interface{}{"type": "integer", "description": "Hard wall-clock seconds from claim (0=none)"},
					"max_attempts": map[string]interface{}{"type": "integer", "description": "Max claims before lease expiry fails job (0=unlimited)"},
					"priority":     map[string]interface{}{"type": "integer", "description": "Higher claimed first (default 0, clamped ±1000)"},
					"labels":          map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}, "description": "Optional string labels (max 16)"},
					"idempotency_key": map[string]interface{}{"type": "string", "description": "Client dedup key unique per user (replay returns same job)"},
				},
				"required": []string{"drive_id", "command"},
			},
		},
		{
			name: "claim_next_job", description: "Claim highest-priority pending BYOC job (policy-filtered). Optional runner_id and region. Requires job.run.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"runner_id": map[string]interface{}{"type": "string", "description": "Optional BYOC runner identity"},
					"region":    map[string]interface{}{"type": "string", "description": "Only claim jobs with matching region_hint"},
				},
			},
		},
		{
			name: "complete_job", description: "Mark claimed job succeeded or failed. Optional exit_code, duration_ms, stdout, stderr (capped). Requires job.run.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id":      map[string]interface{}{"type": "string"},
					"ok":          map[string]interface{}{"type": "boolean", "description": "true=succeeded (default true)"},
					"note":        map[string]interface{}{"type": "string"},
					"exit_code":   map[string]interface{}{"type": "integer", "description": "Process exit code from runner"},
					"duration_ms": map[string]interface{}{"type": "integer", "description": "Wall time ms"},
					"stdout":           map[string]interface{}{"type": "string", "description": "Capped process stdout (tail)"},
					"stderr":           map[string]interface{}{"type": "string", "description": "Capped process stderr (tail)"},
					"stdout_truncated": map[string]interface{}{"type": "boolean"},
					"stderr_truncated": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"job_id"},
			},
		},
		{
			name: "heartbeat_job", description: "Refresh BYOC job lease (POST /v1/jobs/{id}/heartbeat). Requires job.run.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"job_id"},
			},
		},
		{
			name: "cancel_job", description: "Cancel a non-terminal BYOC job. Requires job.run.",
			scopes: []string{auth.ScopeJobRun},
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"job_id"},
			},
		},
		{
			name: "list_providers", description: "List providers (public fields only). Requires provider.read (or provider.write).",
			scopes: []string{auth.ScopeProviderRead, auth.ScopeProviderWrite},
			schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		// Stage C — memory / marketplace / graph / connectors / lineage (auth only at API today).
		{
			name: "list_marketplace", description: "List marketplace catalog (GET /v1/marketplace). Optional mine=true. Any authenticated principal.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mine": map[string]interface{}{"type": "boolean", "description": "Only items published by current user"},
				},
			},
		},
		{
			name: "install_marketplace", description: "Install catalog item (POST /v1/marketplace/{id}/install). skill/manifest OK for agents; agent_template is human-only. Paid items need status=paid purchase.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"item_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"item_id"},
			},
		},
		{
			name: "list_memory", description: "List Memory Kernel entries (GET /v1/memory). Agents only see their own agent_id rows.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"layer":    map[string]interface{}{"type": "string", "description": "working|episodic|semantic"},
					"key":      map[string]interface{}{"type": "string"},
					"drive_id": map[string]interface{}{"type": "string"},
					"limit":    map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			name: "put_memory", description: "Put a memory entry (POST /v1/memory). Agents force agent_id to self. Optional embedding for vector search.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"layer":     map[string]interface{}{"type": "string"},
					"content":   map[string]interface{}{"type": "string"},
					"key":       map[string]interface{}{"type": "string"},
					"drive_id":  map[string]interface{}{"type": "string"},
					"meta":      map[string]interface{}{"type": "object"},
					"embedding": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}},
					"ttl_sec":   map[string]interface{}{"type": "integer"},
				},
				"required": []string{"content"},
			},
		},
		{
			name: "search_memory", description: "Vector search memories (POST /v1/memory/search). Client-supplied query embedding; cosine top-k.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}},
					"k":     map[string]interface{}{"type": "integer"},
					"layer": map[string]interface{}{"type": "string"},
				},
				"required": []string{"query"},
			},
		},
		{
			name: "list_graph", description: "List identity graph edges (GET /v1/graph). Optional subject/object filters.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"subject": map[string]interface{}{"type": "string"},
					"object":  map[string]interface{}{"type": "string"},
					"limit":   map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			name: "link_graph", description: "Upsert identity graph edge (POST /v1/graph): subject --relation--> object.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"subject":  map[string]interface{}{"type": "string"},
					"relation": map[string]interface{}{"type": "string"},
					"object":   map[string]interface{}{"type": "string"},
					"meta":     map[string]interface{}{"type": "object"},
				},
				"required": []string{"subject", "relation", "object"},
			},
		},
		{
			name: "list_connectors", description: "List connector bindings (GET /v1/connectors). Non-secret config only; clone on BYOC runner.",
			scopes: nil,
			schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			name: "connectors_catalog", description: "List connector types (GET /v1/connectors/catalog).",
			scopes: nil,
			schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			name: "create_connector", description: "Register connector binding (POST /v1/connectors). Human session required. Secrets in config are stripped; BYOC materializes on runner.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":   map[string]interface{}{"type": "string", "description": "git|postgres|mysql|…"},
					"name":   map[string]interface{}{"type": "string"},
					"config": map[string]interface{}{"type": "object", "description": "Non-secret config only"},
				},
				"required": []string{"type"},
			},
		},
		{
			name: "get_connector", description: "Get connector binding (GET /v1/connectors/{id}).",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"connector_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"connector_id"},
			},
		},
		{
			name: "delete_connector", description: "Delete connector binding (DELETE /v1/connectors/{id}). Human session required.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"connector_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"connector_id"},
			},
		},
		{
			name: "marketplace_checkout", description: "Checkout paid marketplace item (POST …/checkout). Human session; returns checkout_url + stripe_metadata.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"item_id": map[string]interface{}{"type": "string"},
				},
				"required": []string{"item_id"},
			},
		},
		{
			name: "list_lineage", description: "List lineage events (GET /v1/lineage). Optional entity filter.",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"entity": map[string]interface{}{"type": "string"},
					"limit":  map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			name: "record_lineage", description: "Append lineage event (POST /v1/lineage).",
			scopes: nil,
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{"type": "string"},
					"entity": map[string]interface{}{"type": "string"},
					"parent": map[string]interface{}{"type": "string"},
					"detail": map[string]interface{}{"type": "string"},
				},
				"required": []string{"action", "entity"},
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
	// Local-only tools skip token/scope gate; all API tools require token (empty scopes still OK for agents).
	localOnly := name == "workspace_env" || name == "resolve_path"
	if !localOnly {
		if err := ensureScopes(api, token, pc, meta.scopes); err != nil {
			return nil, err
		}
	}

	switch name {
	case "whoami":
		return toolWhoami(api, token, pc)
	case "list_drives":
		return toolListDrives(api, token)
	case "list_bindings":
		var args struct {
			DeviceID string `json:"device_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		return toolListBindings(api, token, args.DeviceID)
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
	case "list_jobs":
		var args struct {
			Status           string            `json:"status"`
			AgentID          string            `json:"agent_id"`
			ClaimedByAgentID string            `json:"claimed_by_agent_id"`
			Region           string            `json:"region"`
			Labels           map[string]string `json:"labels"`
			Limit            int               `json:"limit"`
			Cursor           string            `json:"cursor"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		return toolListJobs(api, token, args.Status, args.AgentID, args.ClaimedByAgentID, args.Region, args.Labels, args.Limit, args.Cursor)
	case "job_stats":
		return toolJobStats(api, token)
	case "get_job":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.JobID) == "" {
			return nil, fmt.Errorf("job_id required")
		}
		return toolGetJob(api, token, args.JobID)
	case "create_job":
		var args struct {
			DriveID     string   `json:"drive_id"`
			Command     []string `json:"command"`
			Mode        string   `json:"mode"`
			BindingID   string   `json:"binding_id"`
			RegionHint  string   `json:"region_hint"`
			Note        string   `json:"note"`
			ConnectorID string   `json:"connector_id"`
			TimeoutSec  int               `json:"timeout_sec"`
			MaxAttempts int               `json:"max_attempts"`
			Priority       int               `json:"priority"`
			Labels         map[string]string `json:"labels"`
			IdempotencyKey string            `json:"idempotency_key"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.DriveID) == "" || len(args.Command) == 0 {
			return nil, fmt.Errorf("drive_id and command required")
		}
		return toolCreateJob(api, token, args.DriveID, args.Command, args.Mode, args.BindingID, args.RegionHint, args.Note, args.ConnectorID, args.TimeoutSec, args.MaxAttempts, args.Priority, args.Labels, args.IdempotencyKey)
	case "claim_next_job":
		var args struct {
			RunnerID string `json:"runner_id"`
			Region   string `json:"region"`
		}
		_ = decodeArgs(argsJSON, &args)
		return toolClaimNextJob(api, token, args.RunnerID, args.Region)
	case "complete_job":
		var args struct {
			JobID           string `json:"job_id"`
			OK              *bool  `json:"ok"`
			Note            string `json:"note"`
			ExitCode        *int   `json:"exit_code"`
			DurationMs      int64  `json:"duration_ms"`
			Stdout          string `json:"stdout"`
			Stderr          string `json:"stderr"`
			StdoutTruncated bool   `json:"stdout_truncated"`
			StderrTruncated bool   `json:"stderr_truncated"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.JobID) == "" {
			return nil, fmt.Errorf("job_id required")
		}
		ok := true
		if args.OK != nil {
			ok = *args.OK
		}
		return toolCompleteJob(api, token, args.JobID, ok, args.Note, args.ExitCode, args.DurationMs, args.Stdout, args.Stderr, args.StdoutTruncated, args.StderrTruncated)
	case "heartbeat_job":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.JobID) == "" {
			return nil, fmt.Errorf("job_id required")
		}
		return toolHeartbeatJob(api, token, args.JobID)
	case "cancel_job":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.JobID) == "" {
			return nil, fmt.Errorf("job_id required")
		}
		return toolCancelJob(api, token, args.JobID)
	case "list_providers":
		return toolListProviders(api, token)
	case "list_marketplace":
		var args struct {
			Mine bool `json:"mine"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		return toolListMarketplace(api, token, args.Mine)
	case "install_marketplace":
		var args struct {
			ItemID string `json:"item_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ItemID) == "" {
			return nil, fmt.Errorf("item_id required")
		}
		return toolInstallMarketplace(api, token, args.ItemID)
	case "list_memory":
		var args struct {
			Layer   string `json:"layer"`
			Key     string `json:"key"`
			DriveID string `json:"drive_id"`
			Limit   int    `json:"limit"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		return toolListMemory(api, token, args.Layer, args.Key, args.DriveID, args.Limit)
	case "put_memory":
		var args struct {
			Layer     string                 `json:"layer"`
			Content   string                 `json:"content"`
			Key       string                 `json:"key"`
			DriveID   string                 `json:"drive_id"`
			Meta      map[string]interface{} `json:"meta"`
			Embedding []float64              `json:"embedding"`
			TTLSec    int                    `json:"ttl_sec"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Content) == "" {
			return nil, fmt.Errorf("content required")
		}
		return toolPutMemory(api, token, args.Layer, args.Content, args.Key, args.DriveID, args.Meta, args.Embedding, args.TTLSec)
	case "search_memory":
		var args struct {
			Query []float64 `json:"query"`
			K     int       `json:"k"`
			Layer string    `json:"layer"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if len(args.Query) == 0 {
			return nil, fmt.Errorf("query embedding required")
		}
		return toolSearchMemory(api, token, args.Query, args.K, args.Layer)
	case "list_graph":
		var args struct {
			Subject string `json:"subject"`
			Object  string `json:"object"`
			Limit   int    `json:"limit"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		return toolListGraph(api, token, args.Subject, args.Object, args.Limit)
	case "link_graph":
		var args struct {
			Subject  string                 `json:"subject"`
			Relation string                 `json:"relation"`
			Object   string                 `json:"object"`
			Meta     map[string]interface{} `json:"meta"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Subject) == "" || strings.TrimSpace(args.Relation) == "" || strings.TrimSpace(args.Object) == "" {
			return nil, fmt.Errorf("subject, relation, object required")
		}
		return toolLinkGraph(api, token, args.Subject, args.Relation, args.Object, args.Meta)
	case "list_connectors":
		return toolListConnectors(api, token)
	case "connectors_catalog":
		return toolConnectorsCatalog(api, token)
	case "create_connector":
		var args struct {
			Type   string                 `json:"type"`
			Name   string                 `json:"name"`
			Config map[string]interface{} `json:"config"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Type) == "" {
			return nil, fmt.Errorf("type required")
		}
		return toolCreateConnector(api, token, args.Type, args.Name, args.Config)
	case "get_connector":
		var args struct {
			ConnectorID string `json:"connector_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ConnectorID) == "" {
			return nil, fmt.Errorf("connector_id required")
		}
		return toolGetConnector(api, token, args.ConnectorID)
	case "delete_connector":
		var args struct {
			ConnectorID string `json:"connector_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ConnectorID) == "" {
			return nil, fmt.Errorf("connector_id required")
		}
		return toolDeleteConnector(api, token, args.ConnectorID)
	case "marketplace_checkout":
		var args struct {
			ItemID string `json:"item_id"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ItemID) == "" {
			return nil, fmt.Errorf("item_id required")
		}
		return toolMarketplaceCheckout(api, token, args.ItemID)
	case "list_lineage":
		var args struct {
			Entity string `json:"entity"`
			Limit  int    `json:"limit"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		return toolListLineage(api, token, args.Entity, args.Limit)
	case "record_lineage":
		var args struct {
			Action string `json:"action"`
			Entity string `json:"entity"`
			Parent string `json:"parent"`
			Detail string `json:"detail"`
		}
		if err := decodeArgs(argsJSON, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Action) == "" || strings.TrimSpace(args.Entity) == "" {
			return nil, fmt.Errorf("action and entity required")
		}
		return toolRecordLineage(api, token, args.Action, args.Entity, args.Parent, args.Detail)
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

func toolListBindings(api, token, deviceID string) (interface{}, error) {
	url := api + "/v1/bindings"
	if strings.TrimSpace(deviceID) != "" {
		url += "?device_id=" + deviceID
	}
	body, status, err := httpDo(http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, fmt.Errorf("GET /v1/bindings HTTP %d: %s", status, truncate(string(body), 512))
	}
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return toolResult(false, string(body)), nil
	}
	return toolResultJSON(map[string]interface{}{
		"bindings": parsed,
		"hint":     "Use ensure_mounted_hint with binding_id for session probe; hubd/runner for actual mount.",
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

func toolJobStats(api, token string) (interface{}, error) {
	body, code, err := httpDo(http.MethodGet, api+"/v1/jobs/stats", token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("job stats HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolListJobs(api, token, status, agentID, claimedBy, region string, labels map[string]string, limit int, cursor string) (interface{}, error) {
	url := api + "/v1/jobs"
	q := []string{}
	if status != "" {
		q = append(q, "status="+status)
	}
	if agentID != "" {
		q = append(q, "agent_id="+agentID)
	}
	if claimedBy != "" {
		q = append(q, "claimed_by_agent_id="+claimedBy)
	}
	if region != "" {
		q = append(q, "region="+region)
	}
	if limit > 0 {
		q = append(q, "limit="+strconv.Itoa(limit))
	}
	if cursor != "" {
		q = append(q, "cursor="+cursor)
	}
	for k, v := range labels {
		if strings.TrimSpace(k) == "" {
			continue
		}
		q = append(q, "label="+k+":"+v)
	}
	if len(q) > 0 {
		url += "?" + strings.Join(q, "&")
	}
	body, code, err := httpDo(http.MethodGet, url, token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("list jobs HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolCreateJob(api, token, driveID string, command []string, mode, bindingID, regionHint, note, connectorID string, timeoutSec, maxAttempts, priority int, labels map[string]string, idempotencyKey string) (interface{}, error) {
	payload := map[string]interface{}{
		"drive_id": driveID,
		"command":  command,
	}
	if mode != "" {
		payload["mode"] = mode
	}
	if bindingID != "" {
		payload["binding_id"] = bindingID
	}
	if regionHint != "" {
		payload["region_hint"] = regionHint
	}
	if note != "" {
		payload["note"] = note
	}
	if connectorID != "" {
		payload["connector_id"] = connectorID
	}
	if timeoutSec > 0 {
		payload["timeout_sec"] = timeoutSec
	}
	if maxAttempts > 0 {
		payload["max_attempts"] = maxAttempts
	}
	if priority != 0 {
		payload["priority"] = priority
	}
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	if strings.TrimSpace(idempotencyKey) != "" {
		payload["idempotency_key"] = strings.TrimSpace(idempotencyKey)
	}
	body, code, err := httpDo(http.MethodPost, api+"/v1/jobs", token, payload)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("create job HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolClaimNextJob(api, token, runnerID, region string) (interface{}, error) {
	headers := map[string]string{}
	if rid := strings.TrimSpace(runnerID); rid != "" {
		headers["X-AI-Cloudhub-Runner-Id"] = rid
	}
	if reg := strings.TrimSpace(region); reg != "" {
		headers["X-AI-Cloudhub-Region"] = reg
	}
	payload := map[string]string{}
	if rid := strings.TrimSpace(runnerID); rid != "" {
		payload["runner_id"] = rid
	}
	if reg := strings.TrimSpace(region); reg != "" {
		payload["region"] = reg
	}
	var bodyPayload interface{}
	if len(payload) > 0 {
		bodyPayload = payload
	}
	body, code, err := httpDoHeaders(http.MethodPost, api+"/v1/jobs/next/claim", token, bodyPayload, headers)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("claim next HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolGetJob(api, token, jobID string) (interface{}, error) {
	body, code, err := httpDo(http.MethodGet, api+"/v1/jobs/"+jobID, token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("get job HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolHeartbeatJob(api, token, jobID string) (interface{}, error) {
	body, code, err := httpDo(http.MethodPost, api+"/v1/jobs/"+jobID+"/heartbeat", token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("heartbeat job HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolCompleteJob(api, token, jobID string, ok bool, note string, exitCode *int, durationMs int64, stdout, stderr string, stdoutTrunc, stderrTrunc bool) (interface{}, error) {
	payload := map[string]interface{}{"ok": ok, "note": note}
	if exitCode != nil {
		payload["exit_code"] = *exitCode
	}
	if durationMs > 0 {
		payload["duration_ms"] = durationMs
	}
	if stdout != "" {
		payload["stdout"] = stdout
	}
	if stderr != "" {
		payload["stderr"] = stderr
	}
	if stdoutTrunc {
		payload["stdout_truncated"] = true
	}
	if stderrTrunc {
		payload["stderr_truncated"] = true
	}
	body, code, err := httpDo(http.MethodPost, api+"/v1/jobs/"+jobID+"/complete", token, payload)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("complete job HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolCancelJob(api, token, jobID string) (interface{}, error) {
	body, code, err := httpDo(http.MethodPost, api+"/v1/jobs/"+jobID+"/cancel", token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("cancel job HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolListProviders(api, token string) (interface{}, error) {
	body, code, err := httpDo(http.MethodGet, api+"/v1/providers", token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("list providers HTTP %d: %s", code, truncate(string(body), 512))
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
			"mcp_scopes":  "Agent tokens need drive.read/write, job.run, provider.* for those tools; Stage C memory/graph/marketplace use authenticated token (skill install OK for agents)",
			"path_jail":   "resolve_path and mount_point checked against workspace",
			"runner_env":  "runner filters secrets; set AI_CLOUDHUB_PASS_TOKEN=1 to pass API token into agent",
			"mcp_version": serverVersion,
		},
		"tools": []string{
			"whoami", "list_drives", "list_bindings", "list_providers", "ensure_mounted_hint", "workspace_env", "resolve_path",
			"list_snapshots", "create_snapshot", "list_objects",
			"list_jobs", "job_stats", "get_job", "create_job", "claim_next_job", "complete_job", "heartbeat_job", "cancel_job",
			"list_marketplace", "install_marketplace", "marketplace_checkout", "list_memory", "put_memory", "search_memory",
			"list_graph", "link_graph", "list_connectors", "connectors_catalog", "create_connector", "get_connector", "delete_connector",
			"list_lineage", "record_lineage",
		},
	}
	out, err := toolResultJSON(doc)
	if err != nil {
		return toolResult(false, fmt.Sprintf("%v", doc))
	}
	return out
}

func httpDo(method, url, token string, payload interface{}) ([]byte, int, error) {
	return httpDoHeaders(method, url, token, payload, nil)
}

func httpDoHeaders(method, url, token string, payload interface{}, extra map[string]string) ([]byte, int, error) {
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
	for k, v := range extra {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
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
