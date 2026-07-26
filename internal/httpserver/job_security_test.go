package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/agent"
	"github.com/awmbtc/AI-cloudhub/internal/auth"
	"github.com/awmbtc/AI-cloudhub/internal/config"
	"github.com/awmbtc/AI-cloudhub/internal/drive"
	"github.com/awmbtc/AI-cloudhub/internal/job"
	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/awmbtc/AI-cloudhub/internal/store"
)

// jobSecurityEnv wires a minimal API server for job isolation / agent-scope tests.
type jobSecurityEnv struct {
	h      http.Handler
	st     store.Store
	auth   *auth.Service
	jobs   *job.Service
	agents *agent.Service
	drives *drive.Service
}

func newJobSecurityEnv(t *testing.T) *jobSecurityEnv {
	t.Helper()
	st := store.NewMemory()
	authSvc := auth.New("test-secret-for-job-security", st)
	ps := provider.NewService(st)
	ds := drive.NewService(ps, st)
	js := job.NewService(st)
	as := agent.NewService(st)
	h := New(Deps{
		Config: config.Config{
			JWTSecret:      "test-secret-for-job-security",
			AllowRegister:  true,
			MaxBodyBytes:   1 << 20,
			AuthRatePerMin: 1000, // avoid flaky rate limits in tests
		},
		Auth:    authSvc,
		Providers: ps,
		Drives:  ds,
		Jobs:    js,
		Agents:  as,
		Store:   st,
	})
	return &jobSecurityEnv{h: h, st: st, auth: authSvc, jobs: js, agents: as, drives: ds}
}

func (e *jobSecurityEnv) register(t *testing.T, username string) (*auth.User, string) {
	t.Helper()
	u, err := e.auth.Register(username, "password1")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := e.auth.Login(username, "password1")
	if err != nil {
		t.Fatal(err)
	}
	return u, pair.AccessToken
}

func (e *jobSecurityEnv) makeDrive(t *testing.T, userID string) string {
	t.Helper()
	ps := provider.NewService(e.st)
	rec, err := ps.Create(userID, provider.CreateInput{
		Name: "p-" + userID,
		Type: provider.TypeMinIO,
		Creds: provider.Credentials{
			AccessKey: "ak",
			SecretKey: "sk",
			Endpoint:  "127.0.0.1:9000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.drives.Create(userID, drive.CreateInput{
		Name:       "d-" + userID,
		ProviderID: rec.ID,
		Bucket:     "b1",
		MountPoint: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func (e *jobSecurityEnv) agentToken(t *testing.T, user *auth.User, name string, scopes, drives []string) (agentID, token string) {
	t.Helper()
	rec, err := e.agents.Create(user.ID, agent.CreateInput{
		Name:            name,
		DefaultScopes:   scopes,
		AllowedDriveIDs: drives,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Reload user for TokenVersion.
	su, err := e.st.GetUserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.auth.IssueAgentToken(user.ID, user.Username, user.Role, rec.ID, su.TokenVersion, scopes, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID, tok
}

func (e *jobSecurityEnv) do(t *testing.T, method, path, token string, body interface{}) (int, map[string]interface{}, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	raw := rr.Body.String()
	var m map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &m)
	return rr.Code, m, raw
}

// TestJobHTTPCrossUserIsolation: user B cannot get/claim/complete/heartbeat/cancel user A's job via HTTP.
func TestJobHTTPCrossUserIsolation(t *testing.T) {
	e := newJobSecurityEnv(t)
	ua, tokA := e.register(t, "alice-jobs")
	_, tokB := e.register(t, "bob-jobs")
	driveA := e.makeDrive(t, ua.ID)

	code, created, raw := e.do(t, http.MethodPost, "/v1/jobs", tokA, map[string]interface{}{
		"drive_id": driveA,
		"command":  []string{"echo", "secret-job"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create %d %s", code, raw)
	}
	jobID, _ := created["id"].(string)
	if jobID == "" {
		t.Fatal("missing job id")
	}

	// Bob cannot get Alice's job
	code, _, raw = e.do(t, http.MethodGet, "/v1/jobs/"+jobID, tokB, nil)
	if code != http.StatusNotFound {
		t.Fatalf("get cross-user: %d %s", code, raw)
	}
	// Bob cannot claim by id
	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/"+jobID+"/claim", tokB, map[string]string{})
	if code == http.StatusOK {
		t.Fatalf("claim cross-user succeeded: %s", raw)
	}
	// Bob claim next must not return Alice's job
	code, body, raw := e.do(t, http.MethodPost, "/v1/jobs/next/claim", tokB, map[string]string{})
	if code == http.StatusOK {
		if id, _ := body["id"].(string); id == jobID {
			t.Fatalf("claim next returned foreign job: %s", raw)
		}
	}
	// Alice claims
	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/"+jobID+"/claim", tokA, map[string]string{})
	if code != http.StatusOK {
		t.Fatalf("owner claim: %d %s", code, raw)
	}
	for _, path := range []string{
		"/v1/jobs/" + jobID + "/heartbeat",
		"/v1/jobs/" + jobID + "/complete",
		"/v1/jobs/" + jobID + "/cancel",
	} {
		var body interface{}
		if strings.HasSuffix(path, "/complete") {
			body = map[string]interface{}{"ok": true, "stdout": "pwn"}
		}
		code, _, raw = e.do(t, http.MethodPost, path, tokB, body)
		if code == http.StatusOK {
			t.Fatalf("cross-user %s succeeded: %s", path, raw)
		}
	}
	// Still running for Alice
	got, err := e.jobs.Get(ua.ID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != job.StatusRunning {
		t.Fatalf("status %s", got.Status)
	}
	if got.Stdout != "" {
		t.Fatalf("stdout mutated: %q", got.Stdout)
	}
}

// TestJobHTTPAgentMissingJobRunScope: agent without job.run cannot create/claim/complete.
func TestJobHTTPAgentMissingJobRunScope(t *testing.T) {
	e := newJobSecurityEnv(t)
	u, humanTok := e.register(t, "carol-jobs")
	driveID := e.makeDrive(t, u.ID)
	_, agentTok := e.agentToken(t, u, "no-job-scope", []string{"drive.read"}, nil)

	// Create denied
	code, _, raw := e.do(t, http.MethodPost, "/v1/jobs", agentTok, map[string]interface{}{
		"drive_id": driveID,
		"command":  []string{"echo", "x"},
	})
	if code != http.StatusForbidden || !strings.Contains(raw, "job.run") {
		t.Fatalf("create without job.run: %d %s", code, raw)
	}
	// Human creates job for claim/complete probes
	code, created, raw := e.do(t, http.MethodPost, "/v1/jobs", humanTok, map[string]interface{}{
		"drive_id": driveID,
		"command":  []string{"echo", "y"},
	})
	if code != http.StatusCreated {
		t.Fatalf("human create: %d %s", code, raw)
	}
	jobID, _ := created["id"].(string)

	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/next/claim", agentTok, map[string]string{})
	if code != http.StatusForbidden || !strings.Contains(raw, "job.run") {
		t.Fatalf("claim next without job.run: %d %s", code, raw)
	}
	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/"+jobID+"/claim", agentTok, map[string]string{})
	if code != http.StatusForbidden || !strings.Contains(raw, "job.run") {
		t.Fatalf("claim without job.run: %d %s", code, raw)
	}
	// Owner claims so complete path is meaningful
	if _, err := e.jobs.Claim(u.ID, jobID, "", ""); err != nil {
		t.Fatal(err)
	}
	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/"+jobID+"/complete", agentTok, map[string]interface{}{"ok": true})
	if code != http.StatusForbidden || !strings.Contains(raw, "job.run") {
		t.Fatalf("complete without job.run: %d %s", code, raw)
	}
	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/"+jobID+"/heartbeat", agentTok, nil)
	if code != http.StatusForbidden || !strings.Contains(raw, "job.run") {
		t.Fatalf("heartbeat without job.run: %d %s", code, raw)
	}
	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", agentTok, nil)
	if code != http.StatusForbidden || !strings.Contains(raw, "job.run") {
		t.Fatalf("cancel without job.run: %d %s", code, raw)
	}
}

// TestJobHTTPAgentDriveAllowlist: agent with job.run but wrong drive allowlist cannot create/claim that drive.
func TestJobHTTPAgentDriveAllowlist(t *testing.T) {
	e := newJobSecurityEnv(t)
	u, humanTok := e.register(t, "dave-jobs")
	// Two drives for same user
	driveAllowed := e.makeDrive(t, u.ID)
	// Second drive via fresh provider
	ps := provider.NewService(e.st)
	rec2, err := ps.Create(u.ID, provider.CreateInput{
		Name: "p2",
		Type: provider.TypeMinIO,
		Creds: provider.Credentials{AccessKey: "ak2", SecretKey: "sk2", Endpoint: "127.0.0.1:9001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := e.drives.Create(u.ID, drive.CreateInput{
		Name: "d2", ProviderID: rec2.ID, Bucket: "b2", MountPoint: "/workspace2",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveDenied := m2.ID

	_, agentTok := e.agentToken(t, u, "drive-limited", []string{"drive.read", "job.run"}, []string{driveAllowed})

	// Create on denied drive
	code, _, raw := e.do(t, http.MethodPost, "/v1/jobs", agentTok, map[string]interface{}{
		"drive_id": driveDenied,
		"command":  []string{"echo", "nope"},
	})
	if code != http.StatusForbidden {
		t.Fatalf("create on denied drive: %d %s", code, raw)
	}
	// Create on allowed drive works
	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs", agentTok, map[string]interface{}{
		"drive_id": driveAllowed,
		"command":  []string{"echo", "ok"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create on allowed: %d %s", code, raw)
	}

	// Human creates job on denied drive; agent claim-by-id / claim-next must not take it
	code, created, raw := e.do(t, http.MethodPost, "/v1/jobs", humanTok, map[string]interface{}{
		"drive_id": driveDenied,
		"command":  []string{"echo", "foreign-drive"},
	})
	if code != http.StatusCreated {
		t.Fatalf("human create denied-drive job: %d %s", code, raw)
	}
	deniedJobID, _ := created["id"].(string)

	code, _, raw = e.do(t, http.MethodPost, "/v1/jobs/"+deniedJobID+"/claim", agentTok, map[string]string{})
	if code != http.StatusForbidden {
		t.Fatalf("claim denied-drive job: %d %s", code, raw)
	}
	// Job must remain pending (released or never claimed)
	j, err := e.jobs.Get(u.ID, deniedJobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != job.StatusPending {
		t.Fatalf("denied job status %s want pending", j.Status)
	}

	// claim next should skip denied drive and fail or claim only allowed jobs
	// create an allowed pending job
	code, allowedCreated, raw := e.do(t, http.MethodPost, "/v1/jobs", agentTok, map[string]interface{}{
		"drive_id": driveAllowed,
		"command":  []string{"echo", "claim-me"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create claimable: %d %s", code, raw)
	}
	allowedID, _ := allowedCreated["id"].(string)

	code, claimed, raw := e.do(t, http.MethodPost, "/v1/jobs/next/claim", agentTok, map[string]string{})
	if code != http.StatusOK {
		t.Fatalf("claim next: %d %s", code, raw)
	}
	if id, _ := claimed["id"].(string); id != allowedID {
		// May claim the earlier allowed job from first create — either is fine if drive allowed
		if did, _ := claimed["drive_id"].(string); did != driveAllowed {
			t.Fatalf("claimed wrong drive: %s", raw)
		}
	}
	// Denied job still pending
	j2, _ := e.jobs.Get(u.ID, deniedJobID)
	if j2.Status != job.StatusPending {
		t.Fatalf("denied job became %s", j2.Status)
	}
}
