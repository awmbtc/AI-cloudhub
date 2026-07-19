package httpserver

import (
	"context"
	"encoding/json"
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
	"github.com/awmbtc/AI-cloudhub/internal/job"
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
	limit     policy.RateLimiter
	authLim   *policy.AuthLimiter
	authFail  *policy.FailureTracker
	store     store.Store
}

// Deps wires services into the HTTP layer.
type Deps struct {
	Config    config.Config
	Auth      *auth.Service
	Workspace *workspace.Service // may be nil
	Providers *provider.Service
	Drives    *drive.Service
	Devices   *device.Service // may be nil (devices routes omitted)
	Jobs      *job.Service
	Agents    *agent.Service
	Limiter   policy.RateLimiter
	Store     store.Store
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
		limit:     lim,
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

	// Agent Identity (ROADMAP-2.0 stage A)
	if s.agents != nil {
		mux.HandleFunc("/v1/agents", s.withAuth(s.routeAgentsRoot))
		mux.HandleFunc("/v1/agents/", s.withAuth(s.routeAgentsSub))
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
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"product": "AI-cloudhub",
		"batch_a": []string{"s3", "r2", "minio"},
		"batch_b": []string{"b2", "oss", "cos"},
		"batch_c": []string{"qiniu", "oracle"},
		"p0":      []string{"sts", "manifest", "binding", "hubd", "runner"},
		"p1":      []string{"sqlite", "secretbox", "ratelimit", "write_barrier", "devices", "binding_quota", "drive_quota", "provider_quota"},
		"p2":      []string{"region", "sync_workspace", "session_refresh", "runtime_check", "jobs_byoc", "minio_sts"},
		"p3":      []string{"jobs_durable", "worker", "mcp", "metrics", "rbac", "readyz", "postgres", "redis_limit", "audit", "cors", "graceful_shutdown", "provider_health", "config_validate", "auth_lockout", "sec_headers", "register_gate", "token_revoke", "refresh_token", "admin_create_user", "agent_identity", "path_jail"},
		"version": version.Version,
	})
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
		j, err := s.jobs.Create(userID, in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncJobCreated()
		writeJSON(w, http.StatusCreated, j)
	case http.MethodGet:
		if r.URL.Query().Get("status") == "pending" {
			region := r.URL.Query().Get("region")
			writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.jobs.ListPending(userID, region)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.jobs.List(userID)})
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
		j, err := s.jobs.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
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
		// id == "next" → claim oldest pending
		var j *job.Job
		var err error
		if id == "next" {
			j, err = s.jobs.ClaimNext(userID)
		} else {
			j, err = s.jobs.Claim(userID, id)
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncJobClaimed()
		writeJSON(w, http.StatusOK, j)
	case "complete":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			OK   bool   `json:"ok"`
			Note string `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		j, err := s.jobs.Complete(userID, id, body.OK, body.Note)
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
		j, err := s.jobs.Cancel(userID, id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, j)
	default:
		writeErr(w, http.StatusNotFound, "not found")
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
		s.auth.Audit(userID, "provider.create", rec.ID, string(rec.Type))
		writeJSON(w, http.StatusCreated, rec.Public())
	case http.MethodGet:
		if !s.requireScope(w, r, auth.ScopeProviderRead) {
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
		res, err := s.providers.HealthProbe(r.Context(), userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		status := http.StatusOK
		if !res.OK {
			status = http.StatusBadGateway // credentials/network failed
		}
		s.auth.Audit(userID, "provider.health", id, res.Message)
		writeJSON(w, status, res)
		return
	}
	if len(parts) != 1 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
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
		// Capture name/type for audit before delete removes the row.
		var detail string
		if rec, err := s.providers.Get(userID, id); err == nil {
			detail = rec.Name + "/" + string(rec.Type)
		}
		if err := s.providers.Delete(userID, id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		s.auth.Audit(userID, "provider.delete", id, detail)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.drives.List(userID)})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
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
			m, err := s.drives.Get(userID, id)
			if err != nil {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, m)
		case http.MethodDelete:
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

	switch parts[1] {
	case "mount":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
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
		var body struct {
			MountPoint string `json:"mount_point"`
			DeviceID   string `json:"device_id"`
			Mode       string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		bundle, err := s.drives.IssueSession(userID, id, body.DeviceID, body.MountPoint, body.Mode)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncSession()
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
		s.auth.Audit(userID, "binding.create", b.ID, b.DriveID)
		writeJSON(w, http.StatusCreated, b)
	case http.MethodGet:
		dev := r.URL.Query().Get("device_id")
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": s.drives.ListBindings(userID, dev)})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeDevicesRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
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
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		b, err := s.drives.GetBinding(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, b)
		return
	}
	switch parts[1] {
	case "session":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		bundle, err := s.drives.IssueSessionForBinding(userID, id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		metrics.IncSession()
		writeJSON(w, http.StatusOK, bundle)
	case "report":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
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
		s.auth.Audit(userID, "binding.report", id, string(b.Actual))
		writeJSON(w, http.StatusOK, b)
	case "desired":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
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
		s.auth.Audit(userID, "binding.desired", id, string(b.Desired))
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
	list, err := s.auth.ListAudit(limit, filterUser, filterAction)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":   list,
		"limit":   limit,
		"user_id": filterUser,
		"action":  filterAction,
	})
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

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
