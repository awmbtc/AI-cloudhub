package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/agent"
	"github.com/awmbtc/AI-cloudhub/internal/auth"
	"github.com/awmbtc/AI-cloudhub/internal/config"
	"github.com/awmbtc/AI-cloudhub/internal/device"
	"github.com/awmbtc/AI-cloudhub/internal/drive"
	"github.com/awmbtc/AI-cloudhub/internal/connector"
	"github.com/awmbtc/AI-cloudhub/internal/idgraph"
	"github.com/awmbtc/AI-cloudhub/internal/job"
	"github.com/awmbtc/AI-cloudhub/internal/lineage"
	"github.com/awmbtc/AI-cloudhub/internal/marketplace"
	"github.com/awmbtc/AI-cloudhub/internal/memkernel"
	"github.com/awmbtc/AI-cloudhub/internal/metrics"
	"github.com/awmbtc/AI-cloudhub/internal/policy"
	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/awmbtc/AI-cloudhub/internal/runtimeenv"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/awmbtc/AI-cloudhub/internal/version"
	"github.com/awmbtc/AI-cloudhub/internal/workspace"
)

type ctxKey int

const principalCtxKey ctxKey = 1

// Server is the HTTP control plane.
type Server struct {
	cfg       config.Config
	auth      *auth.Service
	ws        *workspace.Service // optional legacy
	providers *provider.Service
	drives    *drive.Service
	devices   *device.Service
	jobs      *job.Service
	agents    *agent.Service
	memory     *memkernel.Service
	market     *marketplace.Service
	lineage    *lineage.Service
	idgraph    *idgraph.Service
	connectors *connector.Service
	limit      policy.RateLimiter
	authLim    *policy.AuthLimiter
	authFail   *policy.FailureTracker
	store      store.Store
}

// Deps wires services into the HTTP layer.
type Deps struct {
	Config     config.Config
	Auth       *auth.Service
	Workspace  *workspace.Service // may be nil
	Providers  *provider.Service
	Drives     *drive.Service
	Devices    *device.Service // may be nil (devices routes omitted)
	Jobs       *job.Service
	Agents     *agent.Service
	Memory     *memkernel.Service
	Market     *marketplace.Service
	Lineage    *lineage.Service
	IDGraph    *idgraph.Service
	Connectors *connector.Service
	Limiter    policy.RateLimiter
	Store      store.Store
}

// New builds an HTTP handler.
func New(d Deps) http.Handler {
	lim := d.Limiter
	if lim == nil {
		lim = policy.NewLimiter(20, 40) // per-user API
	}
	authRate := float64(d.Config.AuthRatePerMin)
	if authRate <= 0 {
		authRate = 20
	}
	failMax := d.Config.AuthFailMax
	if failMax <= 0 {
		failMax = 8
	}
	failWin := d.Config.AuthFailWindowMin
	if failWin <= 0 {
		failWin = 15
	}
	agentsSvc := d.Agents
	if agentsSvc == nil && d.Store != nil {
		agentsSvc = agent.NewService(d.Store)
	}
	s := &Server{
		cfg:       d.Config,
		auth:      d.Auth,
		ws:        d.Workspace,
		providers: d.Providers,
		drives:    d.Drives,
		devices:   d.Devices,
		jobs:      d.Jobs,
		agents:    agentsSvc,
		memory:     d.Memory,
		market:     d.Market,
		lineage:    d.Lineage,
		idgraph:    d.IDGraph,
		connectors: d.Connectors,
		limit:      lim,
		authLim:   policy.NewAuthLimiter(authRate, 5),
		authFail:  policy.NewFailureTracker(failMax, time.Duration(failWin)*time.Minute),
		store:     d.Store,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.method(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/readyz", s.method(http.MethodGet, s.handleReadyz))
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/runtime/check", s.method(http.MethodGet, s.handleRuntimeCheck))
	mux.HandleFunc("/v1/auth/register", s.method(http.MethodPost, s.handleRegister))
	mux.HandleFunc("/v1/auth/login", s.method(http.MethodPost, s.handleLogin))
	mux.HandleFunc("/v1/auth/refresh", s.method(http.MethodPost, s.handleRefresh))
	mux.HandleFunc("/v1/auth/logout", s.withAuth(s.handleLogout))
	mux.HandleFunc("/v1/me", s.withAuth(s.handleMe))
	mux.HandleFunc("/v1/me/password", s.withAuth(s.handleChangePassword))
	mux.HandleFunc("/v1/admin/users", s.withAdmin(s.routeAdminUsersRoot))
	mux.HandleFunc("/v1/admin/users/", s.withAdmin(s.routeAdminUsers))
	mux.HandleFunc("/v1/admin/audit", s.withAdmin(s.handleAdminAudit))
	mux.HandleFunc("/v1/admin/policy", s.withAdmin(s.handleAdminPolicy))
	if s.jobs != nil {
		mux.HandleFunc("/v1/admin/jobs", s.withAdmin(s.handleAdminJobsList))
		mux.HandleFunc("/v1/admin/jobs/", s.withAdmin(s.routeAdminJobsSub))
		mux.HandleFunc("/v1/admin/job-webhooks", s.withAdmin(s.handleAdminJobWebhooksList))
		mux.HandleFunc("/v1/admin/job-webhooks/", s.withAdmin(s.routeAdminJobWebhooksSub))
	}
	mux.HandleFunc("/v1/modules", s.method(http.MethodGet, s.handleModules))
	// Stripe (or stub) payment webhook — verified via AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET
	mux.HandleFunc("/v1/webhooks/stripe", s.handleStripeWebhook)

	// Agent Identity (ROADMAP-2.0 stage A)
	if s.agents != nil {
		mux.HandleFunc("/v1/agents", s.withAuth(s.routeAgentsRoot))
		mux.HandleFunc("/v1/agents/", s.withAuth(s.routeAgentsSub))
	}

	// Stage C: Memory / Marketplace / Lineage / Graph / Connectors (embedded modules)
	if s.memory != nil {
		mux.HandleFunc("/v1/memory", s.withAuth(s.routeMemoryRoot))
		mux.HandleFunc("/v1/memory/search", s.withAuth(s.routeMemorySearch))
		mux.HandleFunc("/v1/memory/", s.withAuth(s.routeMemorySub))
	}
	if s.market != nil {
		mux.HandleFunc("/v1/marketplace", s.withAuth(s.routeMarketplaceRoot))
		mux.HandleFunc("/v1/marketplace/", s.withAuth(s.routeMarketplaceSub))
		mux.HandleFunc("/v1/purchases", s.withAuth(s.routePurchases))
		mux.HandleFunc("/v1/purchases/", s.withAuth(func(w http.ResponseWriter, r *http.Request, userID, a, b string) {
			path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/purchases/"), "/")
			parts := strings.Split(path, "/")
			if len(parts) == 2 && parts[1] == "pay" {
				s.routePurchaseWebhook(w, r, userID, parts[0])
				return
			}
			writeErr(w, http.StatusNotFound, "not found")
		}))
	}
	if s.lineage != nil {
		mux.HandleFunc("/v1/lineage", s.withAuth(s.routeLineage))
	}
	if s.idgraph != nil {
		mux.HandleFunc("/v1/graph", s.withAuth(s.routeGraph))
	}
	if s.connectors != nil {
		mux.HandleFunc("/v1/connectors/catalog", s.method(http.MethodGet, s.handleConnectorCatalog))
		mux.HandleFunc("/v1/connectors", s.withAuth(s.routeConnectorsRoot))
		mux.HandleFunc("/v1/connectors/", s.withAuth(s.routeConnectorsSub))
	}

	// Batch A: vendor catalog + provider bindings + drive maps
	mux.HandleFunc("/v1/providers/catalog", s.method(http.MethodGet, s.handleProviderCatalog))
	mux.HandleFunc("/v1/providers", s.withAuth(s.routeProvidersRoot))
	mux.HandleFunc("/v1/providers/", s.withAuth(s.routeProvidersSub))
	mux.HandleFunc("/v1/drives", s.withAuth(s.routeDrivesRoot))
	mux.HandleFunc("/v1/drives/", s.withAuth(s.routeDrivesSub))
	mux.HandleFunc("/v1/bindings", s.withAuth(s.routeBindingsRoot))
	mux.HandleFunc("/v1/bindings/", s.withAuth(s.routeBindingsSub))
	mux.HandleFunc("/v1/sessions/refresh", s.withAuth(s.handleSessionRefresh))
	if s.jobs != nil {
		mux.HandleFunc("/v1/jobs", s.withAuth(s.routeJobsRoot))
		mux.HandleFunc("/v1/jobs/", s.withAuth(s.routeJobsSub))
	}
	if s.devices != nil {
		mux.HandleFunc("/v1/devices", s.withAuth(s.routeDevicesRoot))
		mux.HandleFunc("/v1/devices/", s.withAuth(s.routeDevicesSub))
	}

	// Legacy workspace (optional, when platform S3 configured)
	if s.ws != nil {
		mux.HandleFunc("/v1/workspaces", s.withAuth(s.routeWorkspacesRoot))
		mux.HandleFunc("/v1/workspaces/", s.withAuth(s.routeWorkspaceSub))
	}

	// Global middleware stack (outermost last applied = first executed).
	var h http.Handler = mux
	h = withMaxBody(d.Config.MaxBodyBytes, h)
	h = withCORS(h)
	h = withSecurityHeaders(d.Config.HSTS, h)
	h = withRequestID(h)
	return h
}

func (s *Server) method(m string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != m {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	body := map[string]interface{}{
		"status":  "ok",
		"product": "AI-cloudhub",
		"version": version.Version,
		"batch_a": []string{"s3", "r2", "minio"},
		"batch_b": []string{"b2", "oss", "cos"},
		"batch_c": []string{"qiniu", "oracle"},
		"p0":      []string{"sts", "manifest", "binding", "hubd", "runner"},
		"p1":      []string{"sqlite", "secretbox", "ratelimit", "write_barrier", "devices", "binding_quota", "drive_quota", "provider_quota"},
		"p2":      []string{"region", "sync_workspace", "session_refresh", "runtime_check", "jobs_byoc", "minio_sts"},
		// p3 is additive: never remove keys; smoke scripts may probe membership.
		"p3": []string{
			"jobs_durable", "worker", "mcp", "metrics", "rbac", "readyz", "postgres", "redis_limit",
			"audit", "cors", "graceful_shutdown", "provider_health", "config_validate", "auth_lockout",
			"sec_headers", "register_gate", "token_revoke", "refresh_token", "admin_create_user",
			"agent_identity", "path_jail", "env_filter", "snapshots_v0", "policy_file", "seccomp",
			"objects_presign", "restore_version", "sts_aliyun", "sts_tencent", "s3_sts",
			"job_agent_id", "mcp_jobs", "binding_agent_gate", "devices_human_only", "smoke_mcp",
		},
		// features: compact capability flags for newer surface (mirrors recent p3 adds).
		"features": []string{
			"agent_identity", "path_jail", "env_filter", "snapshots_v0", "policy_file", "opa_rego", "seccomp",
			"objects_presign", "restore_version", "sts_aliyun", "sts_tencent", "s3_sts",
			"qiniu_download", "oci_iam", "oci_par", "oci_secret", "remote_pdp",
			"memory_kernel_v0", "marketplace_v0", "modules_registry",
			"data_lineage_v0", "identity_graph_v0", "vector_memory_v0",
			"connectors_catalog", "marketplace_checkout",
			"job_agent_id", "mcp_jobs", "binding_agent_gate", "devices_human_only", "smoke_mcp",
		},
	}
	if s.jobs != nil {
		if c, err := s.jobs.WebhookOutboxStats(); err == nil && c != nil {
			body["webhook_outbox"] = map[string]int{
				"pending": c.Pending, "delivered": c.Delivered, "dead": c.Dead, "total": c.Total,
			}
		}
		st := s.jobs.AdminStats("")
		body["jobs"] = map[string]int{
			"pending": st.Pending, "running": st.Running, "succeeded": st.Succeeded,
			"failed": st.Failed, "cancelled": st.Cancelled, "total": st.Total,
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleRuntimeCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, runtimeenv.Check())
}

func (s *Server) handleSessionRefresh(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var in drive.RefreshSessionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	bundle, err := s.drives.RefreshSession(userID, in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	metrics.IncSession()
	if bundle.Session != nil {
		metrics.IncSTSSource(bundle.Session.Source)
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) routeJobsRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.jobs == nil {
		writeErr(w, http.StatusNotFound, "jobs disabled")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if !s.requireScope(w, r, auth.ScopeJobRun) {
			return
		}
		var in job.CreateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := s.drives.Get(userID, in.DriveID); err != nil {
			writeErr(w, http.StatusBadRequest, "drive: "+err.Error())
			return
		}
		if !s.allowAgentJob(w, r, in.DriveID) {
			return
		}
		if pr := principalFrom(r); pr != nil {
			in.AgentID = pr.AgentID
		}
		j, created, err := s.jobs.Create(userID, in)
		if err != nil {
			if errors.Is(err, job.ErrIdempotencyConflict) {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if created {
			metrics.IncJobCreated()
			if j.ConnectorID != "" {
				metrics.IncJobWithConnector()
			}
			if pr := principalFrom(r); pr != nil {
				s.auth.AuditAgent(userID, pr.AgentID, "job.create", j.ID, in.DriveID)
			} else {
				s.auth.Audit(userID, "job.create", j.ID, in.DriveID)
			}
			writeJSON(w, http.StatusCreated, j)
			return
		}
		// Idempotent replay: same key + same payload.
		writeJSON(w, http.StatusOK, j)
	case http.MethodGet:
		// List is human-oriented; agents need job.run for pending claim workflows.
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
			if !s.requireScope(w, r, auth.ScopeJobRun) {
				return
			}
			if !s.allowAgentJob(w, r, "") {
				return
			}
		}
		statusQ := strings.TrimSpace(r.URL.Query().Get("status"))
		// status=pending keeps claimable set (pending+dispatched) + optional region.
		if statusQ == "pending" {
			region := r.URL.Query().Get("region")
			writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.jobs.ListPending(userID, region)})
			return
		}
		limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
		filt := job.ListFilter{
			AgentID:          strings.TrimSpace(r.URL.Query().Get("agent_id")),
			ClaimedByAgentID: strings.TrimSpace(r.URL.Query().Get("claimed_by_agent_id")),
			Status:           statusQ, // running|succeeded|failed|cancelled|dispatched
			Labels:           parseLabelQuery(r.URL.Query()["label"]),
			Limit:            limit,
			Cursor:           strings.TrimSpace(r.URL.Query().Get("cursor")),
		}
		items, nextCursor := s.jobs.List(userID, filt)
		effLimit := filt.Limit
		if effLimit <= 0 {
			effLimit = 100
		}
		if effLimit > 500 {
			effLimit = 500
		}
		resp := map[string]interface{}{
			"items":               items,
			"agent_id":            filt.AgentID,
			"claimed_by_agent_id": filt.ClaimedByAgentID,
			"status":              filt.Status,
			"labels":              filt.Labels,
			"limit":               effLimit,
			"count":               len(items),
		}
		if nextCursor != "" {
			resp["next_cursor"] = nextCursor
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeJobsSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.jobs == nil {
		writeErr(w, http.StatusNotFound, "jobs disabled")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		// GET /v1/jobs/stats — per-status counts (before job id lookup).
		if id == "stats" {
			if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
				if !s.requireScope(w, r, auth.ScopeJobRun) {
					return
				}
				if !s.allowAgentJob(w, r, "") {
					return
				}
			}
			st := s.jobs.Stats(userID)
			writeJSON(w, http.StatusOK, st)
			return
		}
		j, err := s.jobs.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
			if !s.requireScope(w, r, auth.ScopeJobRun) {
				return
			}
			if !s.allowAgentJob(w, r, j.DriveID) {
				return
			}
		}
		writeJSON(w, http.StatusOK, j)
		return
	}
	switch parts[1] {
	case "claim":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeJobRun) {
			return
		}
		// Pre-check job.run policy without drive (still enforces global job denies).
		if !s.allowAgentJob(w, r, "") {
			return
		}
		claimedBy := ""
		if pr := principalFrom(r); pr != nil {
			claimedBy = pr.AgentID
		}
		// Optional BYOC runner identity / region (header preferred; body fallback).
		runnerID := strings.TrimSpace(r.Header.Get("X-AI-Cloudhub-Runner-Id"))
		region := strings.TrimSpace(r.Header.Get("X-AI-Cloudhub-Region"))
		if region == "" {
			region = strings.TrimSpace(r.URL.Query().Get("region"))
		}
		var claimBody struct {
			RunnerID string `json:"runner_id"`
			Region   string `json:"region"`
		}
		_ = json.NewDecoder(r.Body).Decode(&claimBody)
		if runnerID == "" {
			runnerID = strings.TrimSpace(claimBody.RunnerID)
		}
		if region == "" {
			region = strings.TrimSpace(claimBody.Region)
		}
		if len(runnerID) > 128 {
			runnerID = runnerID[:128]
		}
		var j *job.Job
		var err error
		if id == "next" {
			// Skip + release jobs whose drive the agent cannot access.
			j, err = s.jobs.ClaimNextFiltered(userID, claimedBy, runnerID, region, s.agentJobDriveDenyReason(r))
		} else {
			// Known id: pre-check drive when possible, claim, then re-check + release on deny.
			driveID := ""
			if existing, gerr := s.jobs.Get(userID, id); gerr == nil {
				driveID = existing.DriveID
			}
			if !s.allowAgentJob(w, r, driveID) {
				return
			}
			j, err = s.jobs.Claim(userID, id, claimedBy, runnerID)
			if err == nil && j != nil {
				if reason := s.agentJobDriveDenyReason(r)(j.DriveID); reason != "" {
					if _, rerr := s.jobs.ReleaseToPending(userID, j.ID, reason); rerr != nil {
						writeErr(w, http.StatusForbidden, reason+"; release failed: "+rerr.Error())
						return
					}
					writeErr(w, http.StatusForbidden, reason)
					return
				}
			}
		}
		if err != nil {
			// Policy-filtered claim next often returns "no claimable jobs for this agent".
			if strings.Contains(err.Error(), "no claimable") || strings.Contains(err.Error(), "for this agent") {
				writeErr(w, http.StatusForbidden, err.Error())
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncJobClaimed()
		if pr := principalFrom(r); pr != nil {
			detail := j.DriveID
			if j.ClaimedByAgentID != "" {
				detail += " claimer=" + j.ClaimedByAgentID
			}
			s.auth.AuditAgent(userID, pr.AgentID, "job.claim", j.ID, detail)
		}
		writeJSON(w, http.StatusOK, j)
	case "complete":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeJobRun) {
			return
		}
		driveID := ""
		if existing, err := s.jobs.Get(userID, id); err == nil {
			driveID = existing.DriveID
		}
		if !s.allowAgentJob(w, r, driveID) {
			return
		}
		var body struct {
			OK              bool   `json:"ok"`
			Note            string `json:"note"`
			ExitCode        *int   `json:"exit_code"`
			DurationMs      int64  `json:"duration_ms"`
			Stdout          string `json:"stdout"`
			Stderr          string `json:"stderr"`
			StdoutTruncated bool   `json:"stdout_truncated"`
			StderrTruncated bool   `json:"stderr_truncated"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		j, err := s.jobs.Complete(userID, id, job.CompleteInput{
			OK: body.OK, Note: body.Note, ExitCode: body.ExitCode, DurationMs: body.DurationMs,
			Stdout: body.Stdout, Stderr: body.Stderr,
			StdoutTruncated: body.StdoutTruncated, StderrTruncated: body.StderrTruncated,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncJobCompleted()
		if j.ConnectorID != "" {
			metrics.IncJobCompletedWithConnector()
		}
		detail := "ok"
		if !body.OK {
			detail = "failed"
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "job.complete", j.ID, detail)
		}
		writeJSON(w, http.StatusOK, j)
	case "heartbeat":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeJobRun) {
			return
		}
		driveID := ""
		if existing, err := s.jobs.Get(userID, id); err == nil {
			driveID = existing.DriveID
		}
		if !s.allowAgentJob(w, r, driveID) {
			return
		}
		j, err := s.jobs.Heartbeat(userID, id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, j)
	case "cancel":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeJobRun) {
			return
		}
		driveID := ""
		if existing, err := s.jobs.Get(userID, id); err == nil {
			driveID = existing.DriveID
		}
		if !s.allowAgentJob(w, r, driveID) {
			return
		}
		j, err := s.jobs.Cancel(userID, id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncJobCancelled()
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "job.cancel", j.ID, driveID)
		}
		writeJSON(w, http.StatusOK, j)
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// allowAgentJob enforces job.run policy (+ optional drive allowlist) for agent tokens.
// Humans always pass. driveID may be empty (list / claim next pre-check).
func (s *Server) allowAgentJob(w http.ResponseWriter, r *http.Request, driveID string) bool {
	if reason := s.agentJobDriveDenyReason(r)(driveID); reason != "" {
		writeErr(w, http.StatusForbidden, reason)
		return false
	}
	return true
}

// agentJobDriveDenyReason returns a function that checks job.run + drive for the request principal.
// Returns empty string if allowed (or human), else a deny reason.
// When driveID is empty, only ActionJobRun without drive is evaluated.
func (s *Server) agentJobDriveDenyReason(r *http.Request) func(driveID string) string {
	pr := principalFrom(r)
	if pr == nil || pr.AgentID == "" || s.agents == nil {
		return func(string) string { return "" }
	}
	return func(driveID string) string {
		req := policy.Request{
			AgentID: pr.AgentID,
			Scopes:  pr.Scopes,
			Action:  policy.ActionJobRun,
			DriveID: driveID,
		}
		if err := s.agents.CheckAccess(req); err != nil {
			return err.Error()
		}
		return ""
	}
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.authLim != nil && !s.authLim.Allow("reg:"+ip) {
		metrics.IncRateLimited()
		writeErr(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	// Registration gate: when disabled, still allow bootstrap (zero users).
	if !s.cfg.AllowRegister {
		n := 0
		if s.store != nil {
			if c, err := s.store.CountUsers(); err == nil {
				n = c
			}
		}
		if n > 0 {
			writeErr(w, http.StatusForbidden, "registration disabled")
			return
		}
	}
	if !requireJSON(w, r) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.auth.Register(body.Username, body.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.Audit(u.ID, "auth.register", u.Username, "role="+u.Role)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.authLim != nil && !s.authLim.Allow("login:"+ip) {
		metrics.IncRateLimited()
		writeErr(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	userKey := strings.ToLower(strings.TrimSpace(body.Username))
	lockKey := "user:" + userKey + "|ip:" + ip
	if s.authFail != nil && s.authFail.Locked(lockKey) {
		s.auth.Audit("", "auth.login_locked", body.Username, "ip="+ip)
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts; try later")
		return
	}
	pair, err := s.auth.Login(body.Username, body.Password)
	if err != nil {
		locked := false
		if s.authFail != nil {
			locked = s.authFail.Fail(lockKey)
		}
		detail := "fail ip=" + ip
		if locked {
			detail += " locked"
		}
		s.auth.Audit("", "auth.login_fail", body.Username, detail)
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if s.authFail != nil {
		s.authFail.Clear(lockKey)
	}
	s.auth.Audit(pair.User.ID, "auth.login", pair.User.Username, "ok")
	writeJSON(w, http.StatusOK, pair)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.authLim != nil && !s.authLim.Allow("refresh:"+ip) {
		metrics.IncRateLimited()
		writeErr(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pair, err := s.auth.Refresh(body.RefreshToken)
	if err != nil {
		s.auth.Audit("", "auth.refresh_fail", "", "ip="+ip)
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.auth.Audit(pair.User.ID, "auth.refresh", pair.User.Username, "ok")
	writeJSON(w, http.StatusOK, pair)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tok := strings.TrimSpace(s.cfg.MetricsToken)
	if tok != "" {
		ok := false
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			ok = strings.TrimPrefix(h, "Bearer ") == tok
		}
		if !ok && r.URL.Query().Get("token") == tok {
			ok = true
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "metrics token required")
			return
		}
	}
	// Scrape-time refresh of job + webhook outbox gauges.
	if s.jobs != nil {
		if c, err := s.jobs.WebhookOutboxStats(); err == nil && c != nil {
			metrics.SetWebhookOutboxGauges(uint64(c.Pending), uint64(c.Delivered), uint64(c.Dead))
		}
		st := s.jobs.AdminStats("")
		metrics.SetJobStatusGauges(uint64(st.Pending), uint64(st.Running), uint64(st.Succeeded), uint64(st.Failed), uint64(st.Cancelled))
	}
	metrics.Handler(w, r)
}

func (s *Server) handleProviderCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":   provider.Catalog(),
		"batch_a": provider.BatchA,
		"batch_b": provider.BatchB,
		"docs":    "docs/VENDORS.md",
	})
}

func (s *Server) routeProvidersRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	switch r.Method {
	case http.MethodPost:
		if !s.requireScope(w, r, auth.ScopeProviderWrite) {
			return
		}
		if !s.allowAgentProvider(w, r, policy.ActionProviderWrite) {
			return
		}
		var in provider.CreateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		rec, err := s.providers.Create(userID, in)
		if err != nil {
			if strings.Contains(err.Error(), "quota exceeded") {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "provider.create", rec.ID, string(rec.Type))
		} else {
			s.auth.Audit(userID, "provider.create", rec.ID, string(rec.Type))
		}
		writeJSON(w, http.StatusCreated, rec.Public())
	case http.MethodGet:
		if !s.requireScope(w, r, auth.ScopeProviderRead) {
			return
		}
		if !s.allowAgentProvider(w, r, policy.ActionProviderRead) {
			return
		}
		list := s.providers.List(userID)
		items := make([]map[string]interface{}, 0, len(list))
		for _, rec := range list {
			items = append(items, rec.Public())
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeProvidersSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/providers/")
	path = strings.Trim(path, "/")
	if path == "" || path == "catalog" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	parts := strings.Split(path, "/")
	id := parts[0]
	if len(parts) == 2 && parts[1] == "health" {
		// POST /v1/providers/{id}/health — outbound ListBuckets probe
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeProviderRead) {
			return
		}
		if !s.allowAgentProvider(w, r, policy.ActionProviderRead) {
			return
		}
		res, err := s.providers.HealthProbe(r.Context(), userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		status := http.StatusOK
		if !res.OK {
			status = http.StatusBadGateway // credentials/network failed
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "provider.health", id, res.Message)
		} else {
			s.auth.Audit(userID, "provider.health", id, res.Message)
		}
		writeJSON(w, status, res)
		return
	}
	if len(parts) != 1 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.requireScope(w, r, auth.ScopeProviderRead) {
			return
		}
		if !s.allowAgentProvider(w, r, policy.ActionProviderRead) {
			return
		}
		rec, err := s.providers.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rec.Public())
	case http.MethodDelete:
		if !s.requireScope(w, r, auth.ScopeProviderWrite) {
			return
		}
		if !s.allowAgentProvider(w, r, policy.ActionProviderWrite) {
			return
		}
		// Capture name/type for audit before delete removes the row.
		var detail string
		if rec, err := s.providers.Get(userID, id); err == nil {
			detail = rec.Name + "/" + string(rec.Type)
		}
		if err := s.providers.Delete(userID, id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "provider.delete", id, detail)
		} else {
			s.auth.Audit(userID, "provider.delete", id, detail)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// allowAgentProvider runs file policy for provider.read / provider.write (humans always pass).
func (s *Server) allowAgentProvider(w http.ResponseWriter, r *http.Request, action string) bool {
	pr := principalFrom(r)
	if pr == nil || pr.AgentID == "" || s.agents == nil {
		return true
	}
	req := policy.Request{
		AgentID: pr.AgentID,
		Scopes:  pr.Scopes,
		Action:  action,
	}
	if err := s.agents.CheckAccess(req); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return false
	}
	return true
}

func (s *Server) routeDrivesRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	switch r.Method {
	case http.MethodPost:
		if !s.requireScope(w, r, auth.ScopeDriveWrite) {
			return
		}
		var in drive.CreateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		m, err := s.drives.Create(userID, in)
		if err != nil {
			if strings.Contains(err.Error(), "quota exceeded") {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "drive.create", m.ID, m.Name)
		writeJSON(w, http.StatusCreated, m)
	case http.MethodGet:
		if !s.requireScope(w, r, auth.ScopeDriveRead) {
			return
		}
		items := s.drives.List(userID)
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" && s.agents != nil {
			if rec, err := s.agents.GetByID(pr.AgentID); err == nil && len(rec.AllowedDriveIDs) > 0 {
				allow := map[string]bool{}
				for _, d := range rec.AllowedDriveIDs {
					allow[d] = true
				}
				filtered := make([]*drive.Map, 0, len(items))
				for _, m := range items {
					if allow[m.ID] {
						filtered = append(filtered, m)
					}
				}
				items = filtered
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// allowAgentDrive enforces B1 drive allowlist + external policy for agent tokens.
func (s *Server) allowAgentDrive(w http.ResponseWriter, r *http.Request, driveID string) bool {
	pr := principalFrom(r)
	if pr == nil || pr.AgentID == "" || s.agents == nil {
		return true
	}
	action := policy.ActionDriveRead
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		action = policy.ActionDriveWrite
	}
	// Session issuance is a read-capability action.
	path := r.URL.Path
	if strings.Contains(path, "/session") {
		action = policy.ActionDriveSession
	}
	// Object helpers: most POSTs are read-side (presign/plan/hint); only restore-version mutates BYOS.
	if strings.Contains(path, "/objects/") {
		switch {
		case strings.HasSuffix(path, "/restore-version") || strings.Contains(path, "/objects/restore-version"):
			action = policy.ActionDriveWrite
		case strings.Contains(path, "/presign-get"),
			strings.Contains(path, "/restore-plan"),
			strings.Contains(path, "/version-hint"):
			action = policy.ActionDriveRead
		}
	}
	req := policy.Request{
		AgentID: pr.AgentID,
		Scopes:  pr.Scopes,
		Action:  action,
		DriveID: driveID,
	}
	if err := s.agents.CheckAccess(req); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return false
	}
	return true
}

func (s *Server) sessionOptsFrom(r *http.Request) drive.SessionOpts {
	pr := principalFrom(r)
	if pr == nil || pr.AgentID == "" || s.agents == nil {
		return drive.SessionOpts{}
	}
	rec, err := s.agents.GetByID(pr.AgentID)
	if err != nil {
		return drive.SessionOpts{AgentID: pr.AgentID}
	}
	return drive.SessionOpts{
		AgentID:       pr.AgentID,
		ReadPrefixes:  rec.ReadPrefixes,
		WritePrefixes: rec.WritePrefixes,
	}
}

func (s *Server) routeDrivesSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/drives/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if !s.requireScope(w, r, auth.ScopeDriveRead) {
				return
			}
			if !s.allowAgentDrive(w, r, id) {
				return
			}
			m, err := s.drives.Get(userID, id)
			if err != nil {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, m)
		case http.MethodDelete:
			if !s.requireScope(w, r, auth.ScopeDriveWrite) {
				return
			}
			var detail string
			if m, err := s.drives.Get(userID, id); err == nil {
				detail = m.Name
			}
			if err := s.drives.Delete(userID, id); err != nil {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			s.auth.Audit(userID, "drive.delete", id, detail)
			writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// All drive sub-resources require drive access for agent tokens.
	if !s.allowAgentDrive(w, r, id) {
		return
	}

	switch parts[1] {
	case "snapshots":
		s.routeDriveSnapshots(w, r, userID, id, parts[2:])
		return
	case "objects":
		s.routeDriveObjects(w, r, userID, id, parts[2:])
		return
	case "mount":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeDriveRead) {
			return
		}
		bundle, err := s.drives.MountConfig(userID, id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, bundle)
	case "session":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeDriveRead) {
			return
		}
		if !s.allowAgentDrive(w, r, id) {
			return
		}
		var body struct {
			MountPoint string `json:"mount_point"`
			DeviceID   string `json:"device_id"`
			Mode       string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		opts := s.sessionOptsFrom(r)
		bundle, err := s.drives.IssueSessionOpts(userID, id, body.DeviceID, body.MountPoint, body.Mode, opts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncSession()
		if bundle.Session != nil {
			metrics.IncSTSSource(bundle.Session.Source)
		}
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
			s.auth.AuditAgent(userID, pr.AgentID, "drive.session", id, "ok")
		}
		writeJSON(w, http.StatusOK, bundle)
	case "manifest":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		m, err := s.drives.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"drive_id":    m.ID,
			"mount_point": m.MountPoint,
			"hint":        "prefer POST .../session for full manifest + short-lived mount spec",
		})
	case "barrier":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var in drive.CompleteBarrierInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		in.DriveID = id
		b, err := s.drives.CompleteBarrier(userID, in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, b)
	case "barriers":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.drives.ListBarriers(userID, id)})
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// routeDriveObjects handles:
//
//	GET  /v1/drives/{id}/objects[?versions=1]
//	GET|POST .../objects/version-hint
//	POST .../objects/presign-get      {key, version_id?, ttl_min?}  (drive.read)
//	POST .../objects/restore-plan    {key, version_id?, ttl_min?}  (drive.read)
//	POST .../objects/restore-version {key, version_id}             (drive.write; S3 CopyObject)
func (s *Server) routeDriveObjects(w http.ResponseWriter, r *http.Request, userID, driveID string, rest []string) {
	action := ""
	if len(rest) >= 1 {
		action = rest[0]
	}

	// Mutating restore needs write; inventory/presign/plan stay read.
	if action == "restore-version" {
		if !s.requireScope(w, r, auth.ScopeDriveWrite) {
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Key       string `json:"key"`
			VersionID string `json:"version_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Key == "" || body.VersionID == "" {
			writeErr(w, http.StatusBadRequest, "key and version_id required")
			return
		}
		out, err := s.drives.ObjectRestoreVersion(userID, driveID, body.Key, body.VersionID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.auth.Audit(userID, "object.restore_version", driveID, body.Key+":"+body.VersionID)
		writeJSON(w, http.StatusOK, out)
		return
	}

	if !s.requireScope(w, r, auth.ScopeDriveRead) {
		return
	}

	switch action {
	case "version-hint":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key, ver, _, ok := parseObjectKeyBody(w, r)
		if !ok {
			return
		}
		out, err := s.drives.ObjectVersionHint(userID, driveID, key, ver)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
		return

	case "presign-get":
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key, ver, ttl, ok := parseObjectKeyBody(w, r)
		if !ok {
			return
		}
		out, err := s.drives.ObjectPresignGet(userID, driveID, key, ver, ttl)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.auth.Audit(userID, "object.presign_get", driveID, key)
		writeJSON(w, http.StatusOK, out)
		return

	case "restore-plan":
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key, ver, ttl, ok := parseObjectKeyBody(w, r)
		if !ok {
			return
		}
		out, err := s.drives.ObjectRestorePlan(userID, driveID, key, ver, ttl)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
		return

	case "":
		// GET inventory
	default:
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	maxKeys := 1000
	if v := r.URL.Query().Get("max"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxKeys = n
		}
	}
	withVer := r.URL.Query().Get("versions") == "1" || r.URL.Query().Get("versions") == "true"
	inv, err := s.drives.ListObjects(userID, driveID, maxKeys, withVer)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// parseObjectKeyBody reads key/version_id/ttl_min from query and optional JSON body.
// key is required. On failure writes error response and returns ok=false.
func parseObjectKeyBody(w http.ResponseWriter, r *http.Request) (key, versionID string, ttlMin int, ok bool) {
	key = r.URL.Query().Get("key")
	versionID = r.URL.Query().Get("version_id")
	ttlMin = 15
	if v := r.URL.Query().Get("ttl_min"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ttlMin = n
		}
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		var body struct {
			Key       string `json:"key"`
			VersionID string `json:"version_id"`
			TTLMin    int    `json:"ttl_min"`
		}
		// Body optional for GET-style; ignore decode errors when empty.
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Key != "" {
			key = body.Key
		}
		if body.VersionID != "" {
			versionID = body.VersionID
		}
		if body.TTLMin > 0 {
			ttlMin = body.TTLMin
		}
	}
	if key == "" {
		writeErr(w, http.StatusBadRequest, "key required")
		return "", "", 0, false
	}
	return key, versionID, ttlMin, true
}

// routeDriveSnapshots handles /v1/drives/{id}/snapshots[/{sid}[/restore]] and .../snapshots/diff
func (s *Server) routeDriveSnapshots(w http.ResponseWriter, r *http.Request, userID, driveID string, rest []string) {
	if !s.requireScope(w, r, auth.ScopeDriveRead) {
		return
	}
	// GET .../snapshots/diff?a=&b=
	if len(rest) == 1 && rest[0] == "diff" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a := r.URL.Query().Get("a")
		b := r.URL.Query().Get("b")
		if a == "" || b == "" {
			writeErr(w, http.StatusBadRequest, "query a and b (snapshot ids) required")
			return
		}
		out, err := s.drives.DiffSnapshots(userID, driveID, a, b)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// write for create/delete/restore
	if len(rest) == 0 || rest[0] == "" {
		switch r.Method {
		case http.MethodGet:
			list, err := s.drives.ListSnapshots(userID, driveID, 50)
			if err != nil {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"items": list})
		case http.MethodPost:
			if !s.requireScope(w, r, auth.ScopeDriveWrite) {
				return
			}
			if !requireJSON(w, r) {
				return
			}
			var in drive.SnapshotCreate
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if pr := principalFrom(r); pr != nil && pr.AgentID != "" && in.AgentID == "" {
				in.AgentID = pr.AgentID
			}
			sn, err := s.drives.CreateSnapshot(userID, driveID, in)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			metrics.IncSnapshot()
			aid := ""
			if pr := principalFrom(r); pr != nil {
				aid = pr.AgentID
			}
			s.auth.AuditAgent(userID, aid, "snapshot.create", sn.ID, driveID)
			writeJSON(w, http.StatusCreated, sn)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	sid := rest[0]
	if len(rest) >= 2 && rest[1] == "restore" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeDriveWrite) {
			return
		}
		var body struct {
			Apply bool `json:"apply"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// also allow ?apply=1
		if !body.Apply {
			q := r.URL.Query().Get("apply")
			body.Apply = q == "1" || q == "true"
		}
		out, err := s.drives.RestoreSnapshot(userID, driveID, sid, body.Apply)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		aid := ""
		if pr := principalFrom(r); pr != nil {
			aid = pr.AgentID
		}
		detail := "preview"
		if body.Apply {
			detail = "applied"
		}
		s.auth.AuditAgent(userID, aid, "snapshot.restore", sid, driveID+" "+detail)
		writeJSON(w, http.StatusOK, out)
		return
	}
	if len(rest) != 1 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sn, err := s.drives.GetSnapshot(userID, driveID, sid)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sn)
	case http.MethodDelete:
		if !s.requireScope(w, r, auth.ScopeDriveWrite) {
			return
		}
		if err := s.drives.DeleteSnapshot(userID, driveID, sid); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		s.auth.Audit(userID, "snapshot.delete", sid, driveID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeBindingsRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	switch r.Method {
	case http.MethodPost:
		if !s.requireScope(w, r, auth.ScopeDriveWrite) {
			return
		}
		var in drive.BindingCreate
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// Agent may only bind drives they are allowed to access.
		if !s.allowAgentDrive(w, r, in.DriveID) {
			return
		}
		b, err := s.drives.CreateBinding(userID, in)
		if err != nil {
			// Quota exceeded is a client error (429-ish via 400 msg is fine for MVP; use 409 for clear conflict).
			if strings.Contains(err.Error(), "quota exceeded") {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "binding.create", b.ID, b.DriveID)
		} else {
			s.auth.Audit(userID, "binding.create", b.ID, b.DriveID)
		}
		writeJSON(w, http.StatusCreated, b)
	case http.MethodGet:
		if !s.requireScope(w, r, auth.ScopeDriveRead) {
			return
		}
		dev := r.URL.Query().Get("device_id")
		items := s.drives.ListBindings(userID, dev)
		// Filter bindings to agent-allowed drives.
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" && s.agents != nil {
			filtered := items[:0]
			for _, b := range items {
				if s.agents.CheckDriveAccess(pr.AgentID, b.DriveID) == nil {
					// Also run file policy for drive.read
					req := policy.Request{AgentID: pr.AgentID, Scopes: pr.Scopes, Action: policy.ActionDriveRead, DriveID: b.DriveID}
					if s.agents.CheckAccess(req) == nil {
						filtered = append(filtered, b)
					}
				}
			}
			items = filtered
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeDevicesRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	// Device registry is for hubd / human operators — not agent tokens.
	if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
		writeErr(w, http.StatusForbidden, "agent token cannot manage devices")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var in device.RegisterInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		d, err := s.devices.Register(userID, in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, d)
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.devices.List(userID)})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeDevicesSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
		writeErr(w, http.StatusForbidden, "agent token cannot manage devices")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/devices/")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	d, err := s.devices.Get(userID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) routeBindingsSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/bindings/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := parts[0]
	// Load binding once for drive allowlist (agents).
	b0, berr := s.drives.GetBinding(userID, id)
	if berr != nil {
		writeErr(w, http.StatusNotFound, berr.Error())
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeDriveRead) {
			return
		}
		if !s.allowAgentDrive(w, r, b0.DriveID) {
			return
		}
		writeJSON(w, http.StatusOK, b0)
		return
	}
	switch parts[1] {
	case "session":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeDriveRead) {
			return
		}
		if !s.allowAgentDrive(w, r, b0.DriveID) {
			return
		}
		sb, err := s.drives.IssueSessionForBindingOpts(userID, id, s.sessionOptsFrom(r))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncSession()
		if sb != nil && sb.Session != nil {
			metrics.IncSTSSource(sb.Session.Source)
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "binding.session", id, b0.DriveID)
		}
		writeJSON(w, http.StatusOK, sb)
		return
	case "report":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeDriveWrite) {
			return
		}
		if !s.allowAgentDrive(w, r, b0.DriveID) {
			return
		}
		var body struct {
			Actual    string `json:"actual"`
			LastError string `json:"last_error"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		b, err := s.drives.ReportActual(userID, id, drive.ActualState(body.Actual), body.LastError)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "binding.report", id, string(b.Actual))
		} else {
			s.auth.Audit(userID, "binding.report", id, string(b.Actual))
		}
		writeJSON(w, http.StatusOK, b)
	case "desired":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireScope(w, r, auth.ScopeDriveWrite) {
			return
		}
		if !s.allowAgentDrive(w, r, b0.DriveID) {
			return
		}
		var body struct {
			Desired string `json:"desired"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		b, err := s.drives.SetDesired(userID, id, drive.DesiredState(body.Desired))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if pr := principalFrom(r); pr != nil {
			s.auth.AuditAgent(userID, pr.AgentID, "binding.desired", id, string(b.Desired))
		} else {
			s.auth.Audit(userID, "binding.desired", id, string(b.Desired))
		}
		writeJSON(w, http.StatusOK, b)
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// --- legacy workspace routes (unchanged behavior) ---

func (s *Server) routeWorkspacesRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateWorkspace(w, r, userID)
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.ws.ListByOwner(userID)})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeWorkspaceSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/workspaces/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	parts := strings.Split(path, "/")
	wsID := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.handleGetWorkspace(w, r, userID, wsID)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	switch parts[1] {
	case "files":
		key := strings.Join(parts[2:], "/")
		s.routeFiles(w, r, userID, wsID, key)
	case "presign":
		if len(parts) < 3 {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		switch parts[2] {
		case "upload":
			s.handlePresignUpload(w, r, userID, wsID)
		case "download":
			s.handlePresignDownload(w, r, userID, wsID)
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	case "agent-mount":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleAgentMount(w, r, userID, wsID)
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) routeFiles(w http.ResponseWriter, r *http.Request, userID, wsID, key string) {
	if err := s.ensureOwner(wsID, userID); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	if key == "" && r.Method == http.MethodGet {
		prefix := r.URL.Query().Get("prefix")
		items, err := s.ws.ListFiles(r.Context(), wsID, prefix)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
		return
	}
	if key == "" {
		writeErr(w, http.StatusBadRequest, "key required")
		return
	}
	switch r.Method {
	case http.MethodPut:
		ct := r.Header.Get("Content-Type")
		if err := s.ws.PutFile(r.Context(), wsID, key, r.Body, r.ContentLength, ct); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": key, "status": "ok"})
	case http.MethodGet:
		rc, err := s.ws.GetFile(r.Context(), wsID, key)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	case http.MethodDelete:
		if err := s.ws.DeleteFile(r.Context(), wsID, key); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := s.ws.Create(r.Context(), userID, body.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request, userID, wsID string) {
	m, err := s.ws.Get(wsID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if m.OwnerID != userID {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handlePresignUpload(w http.ResponseWriter, r *http.Request, userID, wsID string) {
	if err := s.ensureOwner(wsID, userID); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, bucket, err := s.ws.PresignUpload(r.Context(), wsID, body.Key)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"url":     u.String(),
		"method":  "PUT",
		"bucket":  bucket,
		"key":     body.Key,
		"ttl_min": int(s.cfg.PresignTTL.Minutes()),
	})
}

func (s *Server) handlePresignDownload(w http.ResponseWriter, r *http.Request, userID, wsID string) {
	if err := s.ensureOwner(wsID, userID); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.ws.PresignDownload(r.Context(), wsID, body.Key)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"url":     u.String(),
		"method":  "GET",
		"ttl_min": int(s.cfg.PresignTTL.Minutes()),
	})
}

func (s *Server) handleAgentMount(w http.ResponseWriter, r *http.Request, userID, wsID string) {
	if err := s.ensureOwner(wsID, userID); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	hint, err := s.ws.AgentMountHint(wsID, s.cfg.S3Endpoint, s.cfg.S3AccessKey, s.cfg.S3SecretKey, s.cfg.S3UseSSL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hint)
}

func (s *Server) ensureOwner(wsID, userID string) error {
	m, err := s.ws.Get(wsID)
	if err != nil {
		return err
	}
	if m.OwnerID != userID {
		return errForbidden
	}
	return nil
}

var errForbidden = errString("forbidden")

type errString string

func (e errString) Error() string { return string(e) }

type authed func(w http.ResponseWriter, r *http.Request, userID, username, role string)

func principalFrom(r *http.Request) *auth.Principal {
	if v := r.Context().Value(principalCtxKey); v != nil {
		if p, ok := v.(*auth.Principal); ok {
			return p
		}
	}
	return nil
}

func (s *Server) withAuth(next authed) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		pr, err := s.auth.ParsePrincipal(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		if s.limit != nil && !s.limit.Allow(pr.UserID) {
			metrics.IncRateLimited()
			writeErr(w, http.StatusTooManyRequests, "rate limited")
			return
		}
		metrics.IncHTTP()
		ctx := context.WithValue(r.Context(), principalCtxKey, pr)
		next(w, r.WithContext(ctx), pr.UserID, pr.Username, pr.Role)
	}
}

func (s *Server) withAdmin(next authed) http.HandlerFunc {
	return s.withAuth(func(w http.ResponseWriter, r *http.Request, userID, username, role string) {
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
			writeErr(w, http.StatusForbidden, "agent token cannot use admin APIs")
			return
		}
		if role != auth.RoleAdmin {
			writeErr(w, http.StatusForbidden, "admin required")
			return
		}
		if len(s.cfg.AdminCIDRs) > 0 {
			ip := clientIP(r)
			if !ipAllowed(ip, s.cfg.AdminCIDRs) {
				writeErr(w, http.StatusForbidden, "admin API not allowed from this IP")
				return
			}
		}
		next(w, r, userID, username, role)
	})
}

// requireScope enforces capability scopes for agent tokens; human tokens always pass.
func (s *Server) requireScope(w http.ResponseWriter, r *http.Request, need string) bool {
	pr := principalFrom(r)
	if pr == nil {
		return true
	}
	if auth.HasScope(pr.AgentID, pr.Scopes, need) {
		return true
	}
	writeErr(w, http.StatusForbidden, "missing scope: "+need)
	return false
}

func (s *Server) requireHuman(w http.ResponseWriter, r *http.Request) bool {
	pr := principalFrom(r)
	if pr != nil && pr.AgentID != "" {
		writeErr(w, http.StatusForbidden, "human session required")
		return false
	}
	return true
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.store != nil {
		if err := s.store.Ping(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	out := map[string]interface{}{
		"id":       userID,
		"username": username,
		"role":     role,
	}
	if pr := principalFrom(r); pr != nil {
		if pr.AgentID != "" {
			out["agent_id"] = pr.AgentID
			out["scopes"] = pr.Scopes
			out["principal"] = "agent"
		} else {
			out["principal"] = "human"
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) routeAgentsRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.agents == nil {
		writeErr(w, http.StatusNotFound, "agents disabled")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if !s.requireHuman(w, r) {
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var in agent.CreateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		rec, err := s.agents.Create(userID, in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "agent.create", rec.ID, rec.Name)
		writeJSON(w, http.StatusCreated, rec)
	case http.MethodGet:
		// human or agent with any drive scope can list own agents? keep human-only for list of agents management
		if !s.requireHuman(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.agents.List(userID)})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeAgentsSub(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	if s.agents == nil {
		writeErr(w, http.StatusNotFound, "agents disabled")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/agents/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 && (r.Method == http.MethodPut || r.Method == http.MethodPatch) {
		if !s.requireHuman(w, r) {
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var in agent.UpdateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// empty allowed_drive_ids array with set_drives clears list
		if in.AllowedDriveIDs != nil {
			in.SetDrives = true
		}
		rec, err := s.agents.Update(userID, id, in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "agent.update", id, rec.Name)
		writeJSON(w, http.StatusOK, rec)
		return
	}
	if len(parts) == 2 && parts[1] == "token" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !s.requireHuman(w, r) {
			return
		}
		rec, err := s.agents.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if rec.Status != agent.StatusActive {
			writeErr(w, http.StatusBadRequest, "agent disabled")
			return
		}
		var body struct {
			Scopes []string `json:"scopes"`
			TTLMin int      `json:"ttl_min"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		scopes := body.Scopes
		if len(scopes) == 0 {
			scopes = rec.DefaultScopes
		}
		ttl := time.Duration(body.TTLMin) * time.Minute
		if ttl <= 0 {
			ttl = time.Hour
		}
		// load token_version
		u, err := s.store.GetUserByID(userID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		tok, err := s.auth.IssueAgentToken(userID, username, role, rec.ID, u.TokenVersion, scopes, ttl)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "agent.token", rec.ID, strings.Join(scopes, ","))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"token":      tok,
			"token_type": "Bearer",
			"expires_in": int64(ttl.Seconds()),
			"agent_id":   rec.ID,
			"scopes":     scopes,
		})
		return
	}
	if len(parts) != 1 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.requireHuman(w, r) {
			return
		}
		rec, err := s.agents.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rec)
	case http.MethodDelete:
		if !s.requireHuman(w, r) {
			return
		}
		if err := s.agents.Delete(userID, id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		s.auth.Audit(userID, "agent.delete", id, "")
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // optional body
	h := r.Header.Get("Authorization")
	tok := strings.TrimPrefix(h, "Bearer ")
	if err := s.auth.Logout(tok, body.RefreshToken); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.auth.Audit(userID, "auth.logout", username, "ok")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) routeAdminUsersRoot(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminListUsers(w, r, userID, username, role)
	case http.MethodPost:
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		u, err := s.auth.AdminCreateUser(body.Username, body.Password, body.Role)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "admin.create_user", u.ID, u.Username+" role="+u.Role)
		writeJSON(w, http.StatusCreated, u)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.ChangePassword(userID, body.OldPassword, body.NewPassword); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.Audit(userID, "auth.password_change", username, "sessions_revoked")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "note": "re-login required"})
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	list, err := s.auth.ListUsers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": list})
}

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 100
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	filterUser := strings.TrimSpace(r.URL.Query().Get("user_id"))
	filterAction := strings.TrimSpace(r.URL.Query().Get("action"))
	filterAgent := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	list, err := s.auth.ListAuditFilter(store.AuditFilter{
		Limit: limit, UserID: filterUser, Action: filterAction, AgentID: filterAgent,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":    list,
		"limit":    limit,
		"user_id":  filterUser,
		"action":   filterAction,
		"agent_id": filterAgent,
	})
}

// handleAdminPolicy returns external policy file status (not full rule dump by default).
func (s *Server) handleAdminPolicy(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	eng := policy.NewEngine()
	if s.agents != nil && s.agents.Engine() != nil {
		eng = s.agents.Engine()
	}
	st := eng.Status()
	out := map[string]interface{}{
		"status": st,
		"note":   "Built-in scope/drive/path checks always apply. File rules run after. See docs/POLICY.md",
	}
	if r.URL.Query().Get("rules") == "1" || r.URL.Query().Get("rules") == "true" {
		if doc := eng.Document(); doc != nil {
			out["document"] = doc
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) routeAdminUsers(w http.ResponseWriter, r *http.Request, userID, username, role string) {
	// /v1/admin/users/{id}/role | /v1/admin/users/{id}/revoke-sessions
	path := strings.TrimPrefix(r.URL.Path, "/v1/admin/users/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	targetID := parts[0]
	action := parts[1]
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch action {
	case "role":
		var body struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.auth.SetRole(targetID, body.Role); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "admin.set_role", targetID, body.Role)
		writeJSON(w, http.StatusOK, map[string]string{"id": targetID, "role": body.Role})
	case "revoke-sessions":
		ver, err := s.auth.RevokeAllSessions(targetID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "admin.revoke_sessions", targetID, fmt.Sprintf("token_version=%d", ver))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":            targetID,
			"token_version": ver,
			"status":        "revoked",
		})
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// handleAdminJobsList: GET /v1/admin/jobs?user_id=&status=&limit=&cursor=
func (s *Server) handleAdminJobsList(w http.ResponseWriter, r *http.Request, adminID, _, _ string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	filt := job.AdminListFilter{
		UserID: strings.TrimSpace(r.URL.Query().Get("user_id")),
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Limit:  limit,
		Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	items, nextCursor := s.jobs.AdminList(filt)
	// echo effective limit (service clamps)
	effLimit := filt.Limit
	if effLimit <= 0 {
		effLimit = 100
	}
	if effLimit > 500 {
		effLimit = 500
	}
	s.auth.Audit(adminID, "admin.jobs.list", "", fmt.Sprintf("n=%d user_id=%s status=%s has_next=%v", len(items), filt.UserID, filt.Status, nextCursor != ""))
	resp := map[string]interface{}{
		"items":   items,
		"user_id": filt.UserID,
		"status":  filt.Status,
		"limit":   effLimit,
		"count":   len(items),
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	writeJSON(w, http.StatusOK, resp)
}

// routeAdminJobsSub: GET stats|/{id} ; POST /{id}/cancel | POST /{id}/release
func (s *Server) routeAdminJobsSub(w http.ResponseWriter, r *http.Request, adminID, _, _ string) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/admin/jobs/"), "/")
	if path == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	parts := strings.Split(path, "/")
	// POST /v1/admin/jobs/reclaim — global or per-user lease/timeout reclaim
	if path == "reclaim" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		uid := strings.TrimSpace(r.URL.Query().Get("user_id"))
		var (
			n   int
			err error
		)
		if uid != "" {
			n, err = s.jobs.ReclaimStale(uid)
		} else {
			n, err = s.jobs.ReclaimStaleAll()
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auth.Audit(adminID, "admin.jobs.reclaim", uid, fmt.Sprintf("n=%d", n))
		writeJSON(w, http.StatusOK, map[string]interface{}{"reclaimed": n, "user_id": uid})
		return
	}
	// POST /v1/admin/jobs/purge-terminal?older_than_sec=
	if path == "purge-terminal" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var older time.Duration
		if sec, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("older_than_sec"))); err == nil && sec > 0 {
			older = time.Duration(sec) * time.Second
		}
		n, err := s.jobs.AdminPurgeTerminalJobs(older)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auth.Audit(adminID, "admin.jobs.purge_terminal", "", fmt.Sprintf("deleted=%d", n))
		writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": n})
		return
	}
	if len(parts) == 2 && (parts[1] == "cancel" || parts[1] == "release" || parts[1] == "complete") {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Note       string `json:"note"`
			OK         *bool  `json:"ok"`
			ExitCode   *int   `json:"exit_code"`
			DurationMs int64  `json:"duration_ms"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var (
			j   *job.Job
			err error
		)
		switch parts[1] {
		case "cancel":
			j, err = s.jobs.AdminCancel(parts[0], body.Note)
			if err == nil {
				metrics.IncJobCancelled()
				s.auth.Audit(adminID, "admin.jobs.cancel", j.ID, "user="+j.UserID+" note="+strings.TrimSpace(body.Note))
			}
		case "release":
			j, err = s.jobs.AdminRelease(parts[0], body.Note)
			if err == nil {
				metrics.IncJobLeaseReclaim() // reuse reclaim counter for ops force-release
				s.auth.Audit(adminID, "admin.jobs.release", j.ID, "user="+j.UserID+" note="+strings.TrimSpace(body.Note))
			}
		case "complete":
			ok := true
			if body.OK != nil {
				ok = *body.OK
			}
			j, err = s.jobs.AdminComplete(parts[0], job.CompleteInput{
				OK: ok, Note: body.Note, ExitCode: body.ExitCode, DurationMs: body.DurationMs,
			})
			if err == nil {
				if ok {
					metrics.IncJobCompleted()
				}
				s.auth.Audit(adminID, "admin.jobs.complete", j.ID, "user="+j.UserID+" ok="+fmt.Sprint(ok))
			}
		}
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, j)
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if path == "stats" {
		uid := strings.TrimSpace(r.URL.Query().Get("user_id"))
		st := s.jobs.AdminStats(uid)
		s.auth.Audit(adminID, "admin.jobs.stats", uid, fmt.Sprintf("total=%d", st.Total))
		writeJSON(w, http.StatusOK, st)
		return
	}
	// single job id
	j, err := s.jobs.AdminGet(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.auth.Audit(adminID, "admin.jobs.get", j.ID, "user="+j.UserID)
	writeJSON(w, http.StatusOK, j)
}

// handleAdminJobWebhooksList: GET /v1/admin/job-webhooks?status=&job_id=&user_id=&event=&limit=
func (s *Server) handleAdminJobWebhooksList(w http.ResponseWriter, r *http.Request, adminID, _, _ string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "pending" && status != "delivered" && status != "dead" {
		writeErr(w, http.StatusBadRequest, "status must be pending, delivered, or dead")
		return
	}
	event := strings.TrimSpace(r.URL.Query().Get("event"))
	if event != "" && !validJobWebhookEvent(event) {
		writeErr(w, http.StatusBadRequest, "event must be job.succeeded, job.failed, job.cancelled, or job.*")
		return
	}
	filt := job.AdminWebhookFilter{
		Status: status,
		JobID:  strings.TrimSpace(r.URL.Query().Get("job_id")),
		UserID: strings.TrimSpace(r.URL.Query().Get("user_id")),
		Event:  event,
		Limit:  limit,
	}
	items := s.jobs.AdminListWebhooks(filt)
	eff := limit
	if eff <= 0 {
		eff = 100
	}
	if eff > 500 {
		eff = 500
	}
	s.auth.Audit(adminID, "admin.job_webhooks.list", filt.JobID, fmt.Sprintf("n=%d status=%s job_id=%s user_id=%s event=%s", len(items), status, filt.JobID, filt.UserID, event))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":   items,
		"status":  status,
		"job_id":  filt.JobID,
		"user_id": filt.UserID,
		"event":   event,
		"limit":   eff,
		"count":   len(items),
	})
}

// routeAdminJobWebhooksSub: GET /stats | POST /purge | POST /retry-all | GET /{id} | POST /{id}/retry
func (s *Server) routeAdminJobWebhooksSub(w http.ResponseWriter, r *http.Request, adminID, _, _ string) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/admin/job-webhooks/"), "/")
	if path == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if path == "stats" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		c, err := s.jobs.WebhookOutboxStats()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auth.Audit(adminID, "admin.job_webhooks.stats", "", fmt.Sprintf("total=%d", c.Total))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"pending": c.Pending, "delivered": c.Delivered, "dead": c.Dead, "total": c.Total,
		})
		return
	}
	if path == "purge" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		// optional older_than_sec query (default = configured retain / 7d)
		var older time.Duration
		if sec, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("older_than_sec"))); err == nil && sec > 0 {
			older = time.Duration(sec) * time.Second
		}
		n, err := s.jobs.AdminPurgeWebhooks(older)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auth.Audit(adminID, "admin.job_webhooks.purge", "", fmt.Sprintf("deleted=%d older_than_sec=%d", n, int(older.Seconds())))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"deleted": n,
		})
		return
	}
	if path == "retry-all" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		event := strings.TrimSpace(r.URL.Query().Get("event"))
		if event != "" && !validJobWebhookEvent(event) {
			writeErr(w, http.StatusBadRequest, "event must be job.succeeded, job.failed, job.cancelled, or job.*")
			return
		}
		limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
		filt := job.AdminWebhookFilter{
			Status: status,
			JobID:  strings.TrimSpace(r.URL.Query().Get("job_id")),
			UserID: strings.TrimSpace(r.URL.Query().Get("user_id")),
			Event:  event,
			Limit:  limit,
		}
		n, err := s.jobs.AdminRetryWebhooksBatch(filt)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if status == "" {
			status = "dead"
		}
		eff := limit
		if eff <= 0 {
			eff = 100
		}
		if eff > 500 {
			eff = 500
		}
		s.auth.Audit(adminID, "admin.job_webhooks.retry_all", filt.JobID, fmt.Sprintf("requeued=%d status=%s job_id=%s user_id=%s event=%s limit=%d", n, status, filt.JobID, filt.UserID, event, eff))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"requeued": n,
			"status":   status,
			"job_id":   filt.JobID,
			"user_id":  filt.UserID,
			"event":    event,
			"limit":    eff,
		})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "retry" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		v, err := s.jobs.AdminRetryWebhook(parts[0])
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		s.auth.Audit(adminID, "admin.job_webhooks.retry", v.ID, "job="+v.JobID+" event="+v.Event)
		writeJSON(w, http.StatusOK, v)
		return
	}
	if len(parts) != 1 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	v, err := s.jobs.AdminGetWebhook(parts[0])
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.auth.Audit(adminID, "admin.job_webhooks.get", v.ID, "job="+v.JobID)
	writeJSON(w, http.StatusOK, v)
}

// validJobWebhookEvent reports whether event is a known terminal job webhook event
// (job.succeeded|job.failed|job.cancelled) or any non-empty job.* name.
func validJobWebhookEvent(event string) bool {
	switch event {
	case "job.succeeded", "job.failed", "job.cancelled":
		return true
	default:
		// allow forward-compatible job.* event names
		return strings.HasPrefix(event, "job.") && len(event) > len("job.")
	}
}

// parseLabelQuery parses repeated query values "key:value" into a map.
func parseLabelQuery(vals []string) map[string]string {
	if len(vals) == 0 {
		return nil
	}
	out := make(map[string]string, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		i := strings.IndexByte(v, ':')
		if i <= 0 || i >= len(v)-1 {
			continue
		}
		k := strings.TrimSpace(v[:i])
		val := strings.TrimSpace(v[i+1:])
		if k != "" {
			out[k] = val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	if code == http.StatusTooManyRequests {
		// Soft client backoff hint (seconds).
		if w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", "1")
		}
	}
	writeJSON(w, code, map[string]string{"error": msg})
}
