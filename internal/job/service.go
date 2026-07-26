package job

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/metrics"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Status of a BYOC job (never implies platform-owned large runner pool).
type Status string

const (
	StatusPending    Status = "pending"
	StatusDispatched Status = "dispatched"
	StatusRunning    Status = "running"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
)

// Job describes work for a user-side runner (BYOC).
type Job struct {
	ID               string    `json:"id"`
	UserID           string    `json:"user_id"`
	DriveID          string    `json:"drive_id"`
	BindingID        string    `json:"binding_id,omitempty"`
	Mode             string    `json:"mode"`
	Command          []string  `json:"command"`
	Status           Status    `json:"status"`
	RegionHint       string    `json:"region_hint,omitempty"`
	Note             string    `json:"note,omitempty"`
	ConnectorID      string    `json:"connector_id,omitempty"` // Stage C: git/etc for runner
	AgentID             string `json:"agent_id,omitempty"`              // creator agent
	ClaimedByAgentID    string `json:"claimed_by_agent_id,omitempty"`   // last claimer agent
	ClaimedByRunnerID   string `json:"claimed_by_runner_id,omitempty"`  // optional runner host/worker id
	// Priority higher claims first (default 0).
	Priority int `json:"priority,omitempty"`
	// Labels optional key/value tags for filtering (capped size).
	Labels map[string]string `json:"labels,omitempty"`
	// IdempotencyKey client create key unique per user (empty = none).
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// ExitCode process exit when reported by runner (nil = not set).
	ExitCode *int `json:"exit_code,omitempty"`
	// DurationMs runner wall time in milliseconds (0 = not reported).
	DurationMs int64 `json:"duration_ms,omitempty"`
	// HeartbeatAt last claim/heartbeat while running (omitted when zero).
	HeartbeatAt *time.Time `json:"heartbeat_at,omitempty"`
	// ClaimedAt when job entered running (wall clock for hard timeout).
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	// TimeoutSec max run seconds from claim (0 = none / runner default only).
	TimeoutSec int `json:"timeout_sec,omitempty"`
	// AttemptCount number of successful claims so far.
	AttemptCount int `json:"attempt_count,omitempty"`
	// MaxAttempts when >0, lease expiry at/after this many claims fails the job (0 = unlimited).
	MaxAttempts int `json:"max_attempts,omitempty"`
	// Stdout / Stderr capped process output from runner (empty if not reported).
	Stdout          string    `json:"stdout,omitempty"`
	Stderr          string    `json:"stderr,omitempty"`
	StdoutTruncated bool      `json:"stdout_truncated,omitempty"`
	StderrTruncated bool      `json:"stderr_truncated,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateInput for new job.
type CreateInput struct {
	DriveID     string   `json:"drive_id"`
	BindingID   string   `json:"binding_id"`
	Mode        string   `json:"mode"`
	Command     []string `json:"command"`
	RegionHint  string   `json:"region_hint"`
	Note        string   `json:"note"`
	ConnectorID string   `json:"connector_id"`
	// TimeoutSec optional hard wall clock from claim (0 = none).
	TimeoutSec int `json:"timeout_sec"`
	// MaxAttempts optional claim budget before lease expiry fails the job (0 = unlimited).
	MaxAttempts int `json:"max_attempts"`
	// Priority higher values are claimed first (clamped; default 0).
	Priority int `json:"priority"`
	// Labels optional string map tags (max 16 keys, short values).
	Labels map[string]string `json:"labels"`
	// IdempotencyKey optional client key unique per user; replay returns same job.
	IdempotencyKey string `json:"idempotency_key"`
	// AgentID set by control plane from principal (not client spoofable).
	AgentID string `json:"-"`
}

// Service is a durable BYOC job queue.
type Service struct {
	store store.Store
	// lease is how long a running job may sit without heartbeat before reclaim.
	// Zero disables automatic lease reclaim (AI_CLOUDHUB_JOB_LEASE_SEC=0).
	lease time.Duration
}

// NewService creates a job service backed by store.
// Lease default 300s from AI_CLOUDHUB_JOB_LEASE_SEC (0 disables reclaim).
func NewService(st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st, lease: leaseFromEnv()}
}

// SetLease overrides lease TTL (tests). Zero disables reclaim.
func (s *Service) SetLease(d time.Duration) {
	s.lease = d
}

// Lease returns the configured lease TTL (0 = disabled).
func (s *Service) Lease() time.Duration {
	return s.lease
}

func leaseFromEnv() time.Duration {
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_LEASE_SEC"))
	if v == "" {
		return 300 * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 300 * time.Second
	}
	if n <= 0 {
		return 0
	}
	if n < 10 {
		n = 10 // floor so short tests still work but not absurd thrash in prod misconfig
	}
	return time.Duration(n) * time.Second
}

// Create enqueues a job for user runners to claim.
// When IdempotencyKey is set and a job already exists for this user+key, that job is returned (200/201 same body).
func (s *Service) Create(userID string, in CreateInput) (*Job, error) {
	if strings.TrimSpace(in.DriveID) == "" {
		return nil, fmt.Errorf("drive_id required")
	}
	if len(in.Command) == 0 {
		return nil, fmt.Errorf("command required (runs on BYOC runner, not platform pool)")
	}
	idem := strings.TrimSpace(in.IdempotencyKey)
	if len(idem) > 128 {
		return nil, fmt.Errorf("idempotency_key max 128 chars")
	}
	if idem != "" {
		if existing, err := s.store.GetJobByIdempotencyKey(userID, idem); err == nil && existing != nil {
			return jobFromStore(existing), nil
		}
	}
	mode := in.Mode
	if mode == "" {
		mode = "mount"
	}
	cmdJSON, err := json.Marshal(in.Command)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	note := strings.TrimSpace(in.Note)
	if note != "" {
		note += " | "
	}
	note += "BYOC only: claim with your runner; no platform large pool (D-001)"
	timeoutSec := in.TimeoutSec
	if timeoutSec < 0 {
		timeoutSec = 0
	}
	maxAttempts := in.MaxAttempts
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	priority := in.Priority
	if priority > 1000 {
		priority = 1000
	}
	if priority < -1000 {
		priority = -1000
	}
	labelsJSON, err := encodeJobLabels(in.Labels)
	if err != nil {
		return nil, err
	}
	sj := &store.Job{
		ID:             uuid.NewString(),
		UserID:         userID,
		DriveID:        in.DriveID,
		BindingID:      in.BindingID,
		Mode:           mode,
		CommandJSON:    cmdJSON,
		Status:         string(StatusPending),
		RegionHint:     in.RegionHint,
		Note:           note,
		ConnectorID:    strings.TrimSpace(in.ConnectorID),
		AgentID:        strings.TrimSpace(in.AgentID),
		Priority:       priority,
		LabelsJSON:     labelsJSON,
		IdempotencyKey: idem,
		TimeoutSec:     timeoutSec,
		MaxAttempts:    maxAttempts,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.CreateJob(sj); err != nil {
		// Race on unique index: return existing if key set.
		if idem != "" {
			if existing, gerr := s.store.GetJobByIdempotencyKey(userID, idem); gerr == nil && existing != nil {
				return jobFromStore(existing), nil
			}
		}
		return nil, err
	}
	return jobFromStore(sj), nil
}

// Get returns a job if owned by user.
func (s *Service) Get(userID, id string) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	return jobFromStore(sj), nil
}

// ListFilter optional filters for List.
type ListFilter struct {
	AgentID          string // creator agent_id
	ClaimedByAgentID string // claimer
	// Status exact match (pending|dispatched|running|succeeded|failed|cancelled).
	// Empty = all. Note: HTTP status=pending still uses ListPending (includes dispatched).
	Status string
	// Labels require all key=value pairs to match job labels.
	Labels map[string]string
}

// List returns jobs for user, optionally filtered by agent ids, status, and/or labels.
func (s *Service) List(userID string, filter ...ListFilter) []*Job {
	list, err := s.store.ListJobs(userID)
	if err != nil {
		return nil
	}
	var f ListFilter
	if len(filter) > 0 {
		f = filter[0]
	}
	status := strings.TrimSpace(f.Status)
	out := make([]*Job, 0, len(list))
	for _, sj := range list {
		if f.AgentID != "" && sj.AgentID != f.AgentID {
			continue
		}
		if f.ClaimedByAgentID != "" && sj.ClaimedByAgentID != f.ClaimedByAgentID {
			continue
		}
		if status != "" && sj.Status != status {
			continue
		}
		if len(f.Labels) > 0 && !jobLabelsMatch(sj.LabelsJSON, f.Labels) {
			continue
		}
		out = append(out, jobFromStore(sj))
	}
	return out
}

// ListPending returns claimable jobs (pending/dispatched).
// When region is non-empty, only jobs with matching region_hint are returned.
func (s *Service) ListPending(userID, region string) []*Job {
	list, err := s.store.ListPendingJobs(userID)
	if err != nil {
		return nil
	}
	region = strings.TrimSpace(region)
	out := make([]*Job, 0, len(list))
	for _, sj := range list {
		if region != "" && sj.RegionHint != region {
			continue
		}
		out = append(out, jobFromStore(sj))
	}
	return out
}

// ClaimNext claims the highest-priority then oldest pending job (BYOC worker).
// claimedByAgentID / claimedByRunnerID may be empty.
// region non-empty only claims jobs whose region_hint matches (empty-hint jobs never match a set region).
// Stale running jobs (no heartbeat within lease) are reclaimed first.
func (s *Service) ClaimNext(userID, claimedByAgentID, claimedByRunnerID, region string) (*Job, error) {
	_, _ = s.ReclaimStale(userID)
	list, err := s.store.ListPendingJobs(userID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no pending jobs")
	}
	region = strings.TrimSpace(region)
	// Priority DESC, then oldest created_at (memory map order is undefined).
	sort.Slice(list, func(i, j int) bool {
		if list[i].Priority != list[j].Priority {
			return list[i].Priority > list[j].Priority
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	for _, j := range list {
		if j.Status != string(StatusPending) && j.Status != string(StatusDispatched) {
			continue
		}
		if region != "" && strings.TrimSpace(j.RegionHint) != region {
			continue
		}
		claimed, err := s.store.ClaimPendingJob(userID, j.ID, claimedByAgentID, claimedByRunnerID)
		if err != nil {
			// Already claimed or gone — try next.
			continue
		}
		return jobFromStore(claimed), nil
	}
	if region != "" {
		return nil, fmt.Errorf("no pending jobs in region %q", region)
	}
	return nil, fmt.Errorf("no pending jobs")
}

// Claim marks a pending job as running (atomic: only if still claimable).
// claimedByAgentID / claimedByRunnerID may be empty.
func (s *Service) Claim(userID, id, claimedByAgentID, claimedByRunnerID string) (*Job, error) {
	_, _ = s.ReclaimStale(userID)
	sj, err := s.store.ClaimPendingJob(userID, id, claimedByAgentID, claimedByRunnerID)
	if err != nil {
		return nil, err
	}
	return jobFromStore(sj), nil
}

// Heartbeat refreshes lease on a running job. Runners should call periodically.
// Returns error if job is not running (including cancelled) so the runner can stop.
func (s *Service) Heartbeat(userID, id string) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	if Status(sj.Status) == StatusCancelled {
		return nil, fmt.Errorf("job cancelled")
	}
	if Status(sj.Status) != StatusRunning {
		return nil, fmt.Errorf("job not running (status=%s)", sj.Status)
	}
	now := time.Now().UTC()
	sj.HeartbeatAt = now
	sj.UpdatedAt = now
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	metrics.IncJobHeartbeat()
	return jobFromStore(sj), nil
}

// ReclaimStale fails timed-out running jobs and releases lease-expired ones.
// Returns how many jobs were transitioned (timeout fail or lease release).
// Lease reclaim is a no-op when lease is disabled.
func (s *Service) ReclaimStale(userID string) (int, error) {
	list, err := s.store.ListJobs(userID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	var leaseCutoff time.Time
	if s.lease > 0 {
		leaseCutoff = now.Add(-s.lease)
	}
	n := 0
	for _, sj := range list {
		if Status(sj.Status) != StatusRunning {
			continue
		}
		// Hard wall-clock timeout from claim (job.TimeoutSec or global default).
		if sec := effectiveTimeoutSec(sj.TimeoutSec); sec > 0 {
			start := sj.ClaimedAt
			if start.IsZero() {
				start = sj.HeartbeatAt
			}
			if start.IsZero() {
				start = sj.UpdatedAt
			}
			if !start.IsZero() && now.After(start.Add(time.Duration(sec)*time.Second)) {
				if _, err := s.failTimeout(userID, sj.ID, sec); err == nil {
					metrics.IncJobTimeout()
					n++
				}
				continue
			}
		}
		if s.lease <= 0 {
			continue
		}
		// Prefer HeartbeatAt; fall back to UpdatedAt for pre-lease rows.
		ts := sj.HeartbeatAt
		if ts.IsZero() {
			ts = sj.UpdatedAt
		}
		if ts.IsZero() || !ts.Before(leaseCutoff) {
			continue
		}
		// max_attempts: stop re-queuing after enough claims
		if sj.MaxAttempts > 0 && sj.AttemptCount >= sj.MaxAttempts {
			if _, err := s.failMaxAttempts(userID, sj.ID, sj.AttemptCount, sj.MaxAttempts); err == nil {
				metrics.IncJobMaxAttempts()
				n++
			}
			continue
		}
		if _, err := s.ReleaseToPending(userID, sj.ID, "lease expired"); err != nil {
			continue
		}
		metrics.IncJobLeaseReclaim()
		n++
	}
	return n, nil
}

// effectiveTimeoutSec returns job-level timeout, else AI_CLOUDHUB_JOB_TIMEOUT_SEC, else 0.
func effectiveTimeoutSec(jobTimeout int) int {
	if jobTimeout > 0 {
		return jobTimeout
	}
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_TIMEOUT_SEC"))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// failTimeout marks a running job failed due to hard deadline (exit 124 convention).
func (s *Service) failTimeout(userID, id string, sec int) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	if Status(sj.Status) != StatusRunning {
		return nil, fmt.Errorf("job not running (status=%s)", sj.Status)
	}
	code := 124
	sj.Status = string(StatusFailed)
	sj.ExitCode = &code
	sj.Note = appendJobNote(sj.Note, fmt.Sprintf("timeout after %ds", sec))
	sj.HeartbeatAt = time.Time{}
	sj.ClaimedAt = time.Time{}
	sj.ClaimedByRunnerID = ""
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	j := jobFromStore(sj)
	notifyJobTerminal(j)
	return j, nil
}

// failMaxAttempts marks running job failed when lease expired and claim budget is exhausted.
func (s *Service) failMaxAttempts(userID, id string, attempt, max int) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	if Status(sj.Status) != StatusRunning {
		return nil, fmt.Errorf("job not running (status=%s)", sj.Status)
	}
	code := 1
	sj.Status = string(StatusFailed)
	sj.ExitCode = &code
	sj.Note = appendJobNote(sj.Note, fmt.Sprintf("max attempts exceeded (%d/%d) after lease expired", attempt, max))
	sj.HeartbeatAt = time.Time{}
	sj.ClaimedAt = time.Time{}
	sj.ClaimedByRunnerID = ""
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	j := jobFromStore(sj)
	notifyJobTerminal(j)
	return j, nil
}

// webhookEvent is the envelope POSTed to AI_CLOUDHUB_JOB_WEBHOOK_URL.
type webhookEvent struct {
	EventID    string    `json:"event_id"`
	Event      string    `json:"event"` // job.succeeded | job.failed | job.cancelled
	OccurredAt time.Time `json:"occurred_at"`
	Job        *Job      `json:"job"`
}

// notifyJobTerminal best-effort POSTs event envelope to AI_CLOUDHUB_JOB_WEBHOOK_URL (async).
// When AI_CLOUDHUB_JOB_WEBHOOK_SECRET is set, signs with HMAC-SHA256 over "timestamp.body"
// and sets X-AI-Cloudhub-Timestamp + X-AI-Cloudhub-Signature: sha256=<hex>.
// Also sets X-AI-Cloudhub-Event-Id and X-AI-Cloudhub-Event for receivers.
func notifyJobTerminal(j *Job) {
	if j == nil {
		return
	}
	url := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_URL"))
	if url == "" {
		return
	}
	evName := "job." + string(j.Status)
	switch j.Status {
	case StatusSucceeded:
		evName = "job.succeeded"
	case StatusFailed:
		evName = "job.failed"
	case StatusCancelled:
		evName = "job.cancelled"
	}
	ev := webhookEvent{
		EventID:    uuid.NewString(),
		Event:      evName,
		OccurredAt: time.Now().UTC(),
		Job:        j,
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	secret := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_SECRET"))
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	var sig string
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(ts + "."))
		_, _ = mac.Write(payload)
		sig = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	eventID := ev.EventID
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AI-cloudhub-job-webhook/1")
		req.Header.Set("X-AI-Cloudhub-Timestamp", ts)
		req.Header.Set("X-AI-Cloudhub-Event-Id", eventID)
		req.Header.Set("X-AI-Cloudhub-Event", evName)
		if sig != "" {
			req.Header.Set("X-AI-Cloudhub-Signature", sig)
		}
		res, err := client.Do(req)
		if err != nil {
			return
		}
		_ = res.Body.Close()
		if res.StatusCode < 300 {
			metrics.IncJobWebhook()
		}
	}()
}

// VerifyJobWebhookSignature checks HMAC for receivers (exported for tests/docs).
// signed = HMAC-SHA256(secret, timestamp + "." + body); header "sha256=<hex>".
func VerifyJobWebhookSignature(secret, timestamp, signatureHeader string, body []byte) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" || timestamp == "" || signatureHeader == "" {
		return false
	}
	want := signatureHeader
	if strings.HasPrefix(want, "sha256=") {
		want = strings.TrimPrefix(want, "sha256=")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(want))
}

// ReleaseToPending returns a running job to pending so another BYOC runner can claim it.
// Used when a claim succeeded but agent policy/drive allowlist rejects the job's drive,
// or when a lease expires (runner died without complete).
// Only transitions from running (or dispatched) → pending; terminal jobs are rejected.
func (s *Service) ReleaseToPending(userID, id, reason string) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	switch Status(sj.Status) {
	case StatusRunning, StatusDispatched:
		// ok
	default:
		return nil, fmt.Errorf("job not releaseable (status=%s)", sj.Status)
	}
	sj.Status = string(StatusPending)
	sj.ClaimedByAgentID = "" // clear claimer so another runner can take ownership
	sj.ClaimedByRunnerID = ""
	sj.HeartbeatAt = time.Time{}
	sj.ClaimedAt = time.Time{}
	sj.UpdatedAt = time.Now().UTC()
	reason = strings.TrimSpace(reason)
	if reason != "" {
		note := strings.TrimSpace(sj.Note)
		if note != "" {
			note += " | "
		}
		note += "released: " + reason
		// Cap note length to avoid unbounded growth from repeated release cycles.
		if len(note) > 2000 {
			note = note[len(note)-2000:]
		}
		sj.Note = note
	}
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	return jobFromStore(sj), nil
}

// ClaimNextFiltered claims the highest-priority pending job whose driveID passes allow.
// allow(driveID) should return "" if allowed, or a short deny reason if not.
//
// Jobs are filtered **before** claim using the pending list (avoids reclaim loops).
// After a successful atomic claim, allow is re-checked; on deny the job is
// ReleaseToPending and the scan continues. If allow is nil, behaves like ClaimNext.
func (s *Service) ClaimNextFiltered(userID, claimedByAgentID, claimedByRunnerID, region string, allow func(driveID string) string) (*Job, error) {
	if allow == nil {
		return s.ClaimNext(userID, claimedByAgentID, claimedByRunnerID, region)
	}
	_, _ = s.ReclaimStale(userID)
	list, err := s.store.ListPendingJobs(userID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no pending jobs")
	}
	region = strings.TrimSpace(region)
	sort.Slice(list, func(i, j int) bool {
		if list[i].Priority != list[j].Priority {
			return list[i].Priority > list[j].Priority
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	var lastDeny string
	var skipped int
	for _, cand := range list {
		if cand.Status != string(StatusPending) && cand.Status != string(StatusDispatched) {
			continue
		}
		if region != "" && strings.TrimSpace(cand.RegionHint) != region {
			continue
		}
		if reason := allow(cand.DriveID); reason != "" {
			lastDeny = reason
			skipped++
			continue
		}
		claimed, err := s.store.ClaimPendingJob(userID, cand.ID, claimedByAgentID, claimedByRunnerID)
		if err != nil {
			// Race: another worker took it.
			continue
		}
		// Re-check after claim (policy may use richer context later).
		if reason := allow(claimed.DriveID); reason != "" {
			lastDeny = reason
			if _, rerr := s.ReleaseToPending(userID, claimed.ID, reason); rerr != nil {
				return nil, fmt.Errorf("%s (also failed to release job %s: %v)", reason, claimed.ID, rerr)
			}
			continue
		}
		return jobFromStore(claimed), nil
	}
	if lastDeny != "" {
		return nil, fmt.Errorf("no claimable jobs for this agent (%d skipped by policy): %s", skipped, lastDeny)
	}
	return nil, fmt.Errorf("no pending jobs")
}

// DefaultMaxJobOutput is the default cap (bytes) for stdout/stderr on complete.
const DefaultMaxJobOutput = 8192

// CompleteInput is the optional structured result of a BYOC runner complete.
type CompleteInput struct {
	OK              bool
	Note            string
	ExitCode        *int  // nil = not reported
	DurationMs      int64 // 0 = not reported
	Stdout          string
	Stderr          string
	StdoutTruncated bool // explicit from runner; also set if API caps
	StderrTruncated bool
}

// Complete sets terminal status.
// Non-empty note is appended to the existing trail (create D-001 / release / clone path),
// not replaced — same pattern as ReleaseToPending. Capped at 2000 chars.
// Stdout/stderr are stored with a tail cap (AI_CLOUDHUB_JOB_OUTPUT_MAX, default 8192).
// Already-terminal jobs (including cancelled) are returned as-is without mutation.
func (s *Service) Complete(userID, id string, in CompleteInput) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	switch Status(sj.Status) {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return jobFromStore(sj), nil
	}
	if in.OK {
		sj.Status = string(StatusSucceeded)
	} else {
		sj.Status = string(StatusFailed)
	}
	if extra := strings.TrimSpace(in.Note); extra != "" {
		sj.Note = appendJobNote(sj.Note, extra)
	}
	if in.ExitCode != nil {
		v := *in.ExitCode
		sj.ExitCode = &v
	}
	if in.DurationMs > 0 {
		sj.DurationMs = in.DurationMs
	}
	maxOut := jobOutputMax()
	if in.Stdout != "" || in.StdoutTruncated {
		capped, trunc := capTailFlag(in.Stdout, maxOut)
		sj.Stdout = capped
		sj.StdoutTruncated = in.StdoutTruncated || trunc
	}
	if in.Stderr != "" || in.StderrTruncated {
		capped, trunc := capTailFlag(in.Stderr, maxOut)
		sj.Stderr = capped
		sj.StderrTruncated = in.StderrTruncated || trunc
	}
	sj.HeartbeatAt = time.Time{}
	sj.ClaimedAt = time.Time{}
	// Keep claimed_by_runner_id on terminal for attribution.
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	j := jobFromStore(sj)
	notifyJobTerminal(j)
	return j, nil
}

func jobOutputMax() int {
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_OUTPUT_MAX"))
	if v == "" {
		return DefaultMaxJobOutput
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return DefaultMaxJobOutput
	}
	if n > 256*1024 {
		return 256 * 1024 // hard ceiling
	}
	return n
}

// capTail keeps the last max bytes of s (UTF-8 safe-ish: byte-level; logs are usually ASCII).
func capTail(s string, max int) string {
	c, _ := capTailFlag(s, max)
	return c
}

// capTailFlag returns capped string and whether truncation occurred.
func capTailFlag(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[len(s)-max:], true
}

// appendJobNote joins note segments with " | " and caps length (tail kept).
func appendJobNote(existing, extra string) string {
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return existing
	}
	note := strings.TrimSpace(existing)
	if note != "" {
		note += " | "
	}
	note += extra
	if len(note) > 2000 {
		note = note[len(note)-2000:]
	}
	return note
}

// Cancel cancels a non-terminal job.
func (s *Service) Cancel(userID, id string) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	if sj.Status == string(StatusSucceeded) || sj.Status == string(StatusFailed) {
		return nil, fmt.Errorf("job already finished")
	}
	sj.Status = string(StatusCancelled)
	sj.HeartbeatAt = time.Time{}
	sj.ClaimedAt = time.Time{}
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	j := jobFromStore(sj)
	notifyJobTerminal(j)
	return j, nil
}

func jobFromStore(sj *store.Job) *Job {
	var cmd []string
	_ = json.Unmarshal(sj.CommandJSON, &cmd)
	j := &Job{
		ID:               sj.ID,
		UserID:           sj.UserID,
		DriveID:          sj.DriveID,
		BindingID:        sj.BindingID,
		Mode:             sj.Mode,
		Command:          cmd,
		Status:           Status(sj.Status),
		RegionHint:       sj.RegionHint,
		Note:             sj.Note,
		ConnectorID:      sj.ConnectorID,
		AgentID:          sj.AgentID,
		ClaimedByAgentID:  sj.ClaimedByAgentID,
		ClaimedByRunnerID: sj.ClaimedByRunnerID,
		Priority:          sj.Priority,
		Labels:            decodeJobLabels(sj.LabelsJSON),
		IdempotencyKey:    sj.IdempotencyKey,
		DurationMs:        sj.DurationMs,
		TimeoutSec:        sj.TimeoutSec,
		AttemptCount:      sj.AttemptCount,
		MaxAttempts:       sj.MaxAttempts,
		Stdout:            sj.Stdout,
		Stderr:            sj.Stderr,
		StdoutTruncated:   sj.StdoutTruncated,
		StderrTruncated:   sj.StderrTruncated,
		CreatedAt:         sj.CreatedAt,
		UpdatedAt:         sj.UpdatedAt,
	}
	if sj.ExitCode != nil {
		v := *sj.ExitCode
		j.ExitCode = &v
	}
	if !sj.HeartbeatAt.IsZero() {
		t := sj.HeartbeatAt.UTC()
		j.HeartbeatAt = &t
	}
	if !sj.ClaimedAt.IsZero() {
		t := sj.ClaimedAt.UTC()
		j.ClaimedAt = &t
	}
	return j
}

// encodeJobLabels validates and JSON-encodes labels (max 16 keys, short values).
func encodeJobLabels(in map[string]string) ([]byte, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > 16 {
		return nil, fmt.Errorf("labels: at most 16 keys")
	}
	clean := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			return nil, fmt.Errorf("labels: empty key")
		}
		if len(k) > 64 || len(v) > 256 {
			return nil, fmt.Errorf("labels: key max 64 / value max 256 chars")
		}
		clean[k] = v
	}
	return json.Marshal(clean)
}

func decodeJobLabels(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return nil
	}
	return m
}

func jobLabelsMatch(raw []byte, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	have := decodeJobLabels(raw)
	if have == nil {
		return false
	}
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}
