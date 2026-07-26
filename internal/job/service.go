package job

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/metrics"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// serializes outbox delivery within one process (avoids concurrent fail clobbering success).
var webhookProcessMu sync.Mutex

// ErrIdempotencyConflict is returned when idempotency_key is reused with a different payload.
var ErrIdempotencyConflict = errors.New("idempotency conflict: key reused with different payload")

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
// When IdempotencyKey is set and a job already exists for this user+key with the same
// payload, that job is returned with created=false. Different payload → ErrIdempotencyConflict.
func (s *Service) Create(userID string, in CreateInput) (j *Job, created bool, err error) {
	if strings.TrimSpace(in.DriveID) == "" {
		return nil, false, fmt.Errorf("drive_id required")
	}
	if len(in.Command) == 0 {
		return nil, false, fmt.Errorf("command required (runs on BYOC runner, not platform pool)")
	}
	idem := strings.TrimSpace(in.IdempotencyKey)
	if len(idem) > 128 {
		return nil, false, fmt.Errorf("idempotency_key max 128 chars")
	}
	mode := in.Mode
	if mode == "" {
		mode = "mount"
	}
	cmdJSON, err := json.Marshal(in.Command)
	if err != nil {
		return nil, false, err
	}
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
		return nil, false, err
	}
	connectorID := strings.TrimSpace(in.ConnectorID)
	if idem != "" {
		if existing, gerr := s.store.GetJobByIdempotencyKey(userID, idem); gerr == nil && existing != nil {
			if !sameCreatePayload(existing, in, mode, cmdJSON, labelsJSON, connectorID, priority, timeoutSec, maxAttempts) {
				return nil, false, ErrIdempotencyConflict
			}
			return jobFromStore(existing), false, nil
		}
	}
	now := time.Now().UTC()
	note := strings.TrimSpace(in.Note)
	if note != "" {
		note += " | "
	}
	note += "BYOC only: claim with your runner; no platform large pool (D-001)"
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
		ConnectorID:    connectorID,
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
		// Race on unique index: re-check existing.
		if idem != "" {
			if existing, gerr := s.store.GetJobByIdempotencyKey(userID, idem); gerr == nil && existing != nil {
				if !sameCreatePayload(existing, in, mode, cmdJSON, labelsJSON, connectorID, priority, timeoutSec, maxAttempts) {
					return nil, false, ErrIdempotencyConflict
				}
				return jobFromStore(existing), false, nil
			}
		}
		return nil, false, err
	}
	return jobFromStore(sj), true, nil
}

// sameCreatePayload reports whether an existing job matches the create input (idempotent replay).
func sameCreatePayload(sj *store.Job, in CreateInput, mode string, cmdJSON, labelsJSON []byte, connectorID string, priority, timeoutSec, maxAttempts int) bool {
	if sj.DriveID != in.DriveID || sj.BindingID != in.BindingID || sj.Mode != mode {
		return false
	}
	if !bytes.Equal(sj.CommandJSON, cmdJSON) {
		return false
	}
	if strings.TrimSpace(sj.RegionHint) != strings.TrimSpace(in.RegionHint) {
		return false
	}
	if sj.ConnectorID != connectorID {
		return false
	}
	if sj.Priority != priority || sj.TimeoutSec != timeoutSec || sj.MaxAttempts != maxAttempts {
		return false
	}
	// Labels: treat empty/null equal.
	a := bytes.TrimSpace(sj.LabelsJSON)
	b := bytes.TrimSpace(labelsJSON)
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	// Compare as decoded maps (key order independent).
	return mapsEqual(decodeJobLabels(a), decodeJobLabels(b))
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Stats counts jobs by status for a user (BYOC queue snapshot).
type Stats struct {
	Pending    int `json:"pending"`
	Dispatched int `json:"dispatched"`
	Running    int `json:"running"`
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	Cancelled  int `json:"cancelled"`
	Total      int `json:"total"`
}

// Stats returns per-status counts for the user (full aggregation, not list-capped).
func (s *Service) Stats(userID string) Stats {
	c, err := s.store.CountJobsByStatus(strings.TrimSpace(userID))
	if err != nil || c == nil {
		return Stats{}
	}
	return statsFromCounts(c)
}

// AdminListFilter filters cross-user admin job listings.
// Cursor is an opaque keyset token from a previous next_cursor (created_at DESC, id DESC).
type AdminListFilter struct {
	UserID string
	Status string
	Limit  int
	Cursor string
}

// AdminList returns jobs across users (admin). Limit default 100, max 500.
// When more pages exist, nextCursor is a non-empty opaque token for ?cursor=.
func (s *Service) AdminList(f AdminListFilter) (items []*Job, nextCursor string) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	sf := store.AdminJobFilter{
		UserID: strings.TrimSpace(f.UserID),
		Status: strings.TrimSpace(f.Status),
		Limit:  limit + 1, // peek one extra for next_cursor
	}
	if ca, id, ok := decodeAdminCursor(f.Cursor); ok {
		sf.CursorCreated = ca
		sf.CursorID = id
	}
	list, err := s.store.ListJobsAdmin(sf)
	if err != nil {
		return nil, ""
	}
	out := make([]*Job, 0, len(list))
	for _, sj := range list {
		out = append(out, jobFromStore(sj))
	}
	if len(out) > limit {
		last := out[limit-1]
		nextCursor = encodeAdminCursor(last.CreatedAt, last.ID)
		out = out[:limit]
	}
	return out, nextCursor
}

// encodeAdminCursor builds an opaque base64url token: created_at_RFC3339Nano|id
func encodeAdminCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeAdminCursor parses opaque cursor; ok=false if empty or invalid (ignored as first page).
func decodeAdminCursor(cursor string) (time.Time, string, bool) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return time.Time{}, "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		// try padded std encoding for hand-rolled clients
		b, err = base64.URLEncoding.DecodeString(cursor)
		if err != nil {
			return time.Time{}, "", false
		}
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		t, err = time.Parse(time.RFC3339, parts[0])
		if err != nil {
			return time.Time{}, "", false
		}
	}
	return t.UTC(), parts[1], true
}

// AdminGet returns any job by id (admin).
func (s *Service) AdminGet(id string) (*Job, error) {
	sj, err := s.store.GetJobByID(strings.TrimSpace(id))
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	return jobFromStore(sj), nil
}

// AdminStats returns global or per-user status counts (admin).
// Full store aggregation via COUNT GROUP BY — no row cap.
func (s *Service) AdminStats(userID string) Stats {
	return s.Stats(strings.TrimSpace(userID))
}

func statsFromCounts(c *store.JobStatusCounts) Stats {
	if c == nil {
		return Stats{}
	}
	return Stats{
		Pending:    c.Pending,
		Dispatched: c.Dispatched,
		Running:    c.Running,
		Succeeded:  c.Succeeded,
		Failed:     c.Failed,
		Cancelled:  c.Cancelled,
		Total:      c.Total,
	}
}

// statsFromStoreJobs remains for tests that build ad-hoc slices.
func statsFromStoreJobs(list []*store.Job) Stats {
	var st Stats
	for _, sj := range list {
		st.Total++
		switch Status(sj.Status) {
		case StatusPending:
			st.Pending++
		case StatusDispatched:
			st.Dispatched++
		case StatusRunning:
			st.Running++
		case StatusSucceeded:
			st.Succeeded++
		case StatusFailed:
			st.Failed++
		case StatusCancelled:
			st.Cancelled++
		}
	}
	return st
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
	// Limit default 100, max 500. Cursor is opaque keyset from next_cursor.
	Limit  int
	Cursor string
}

// List returns jobs for user, optionally filtered by agent ids, status, and/or labels.
// Filters, keyset cursor, and limit are pushed to the store (ListJobsPage).
// Order: created_at DESC, id DESC. When more pages exist, nextCursor is non-empty.
func (s *Service) List(userID string, filter ...ListFilter) (items []*Job, nextCursor string) {
	var f ListFilter
	if len(filter) > 0 {
		f = filter[0]
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	sf := store.JobListFilter{
		UserID:           strings.TrimSpace(userID),
		AgentID:          strings.TrimSpace(f.AgentID),
		ClaimedByAgentID: strings.TrimSpace(f.ClaimedByAgentID),
		Status:           strings.TrimSpace(f.Status),
		Labels:           f.Labels,
		Limit:            limit + 1,
	}
	if ca, id, ok := decodeAdminCursor(f.Cursor); ok {
		sf.CursorCreated = ca
		sf.CursorID = id
	}
	list, err := s.store.ListJobsPage(sf)
	if err != nil {
		return nil, ""
	}
	out := make([]*Job, 0, len(list))
	for _, sj := range list {
		out = append(out, jobFromStore(sj))
	}
	if len(out) > limit {
		last := out[limit-1]
		nextCursor = encodeAdminCursor(last.CreatedAt, last.ID)
		out = out[:limit]
	}
	return out, nextCursor
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
	s.notifyJobTerminal(j)
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
	s.notifyJobTerminal(j)
	return j, nil
}

// webhookEvent is the envelope POSTed to AI_CLOUDHUB_JOB_WEBHOOK_URL.
type webhookEvent struct {
	EventID    string    `json:"event_id"`
	Event      string    `json:"event"` // job.succeeded | job.failed | job.cancelled
	OccurredAt time.Time `json:"occurred_at"`
	Job        *Job      `json:"job"`
}

func jobWebhookURL() string {
	return strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_URL"))
}

func jobWebhookMaxAttempts() int {
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS"))
	if v == "" {
		return 8
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 8
	}
	if n > 32 {
		return 32
	}
	return n
}

func jobWebhookPollInterval() time.Duration {
	// AI_CLOUDHUB_JOB_WEBHOOK_POLL_SEC default 2
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_POLL_SEC"))
	if v == "" {
		return 2 * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 2 * time.Second
	}
	if n > 60 {
		n = 60
	}
	return time.Duration(n) * time.Second
}

// jobWebhookRetain is how long delivered/dead rows are kept. Default 7d; 0 disables purge.
func jobWebhookRetain() time.Duration {
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_RETAIN_SEC"))
	if v == "" {
		return 7 * 24 * time.Hour
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 7 * 24 * time.Hour
	}
	if n == 0 {
		return 0
	}
	// clamp 1s .. 365d
	if n < 1 {
		n = 1
	}
	if n > 365*24*3600 {
		n = 365 * 24 * 3600
	}
	return time.Duration(n) * time.Second
}

// jobWebhookPurgeInterval is how often the worker runs purge. Default 60s.
func jobWebhookPurgeInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_PURGE_SEC"))
	if v == "" {
		return 60 * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 60 * time.Second
	}
	if n > 3600 {
		n = 3600
	}
	return time.Duration(n) * time.Second
}

// webhookBackoff returns delay after the Nth failed attempt (1-based).
// AI_CLOUDHUB_JOB_WEBHOOK_BACKOFF_SEC=0 forces ~1ms (tests).
func webhookBackoff(attempt int) time.Duration {
	if strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_BACKOFF_SEC")) == "0" {
		return time.Millisecond
	}
	// 5s, 15s, 60s, 5m, 15m, 1h, 2h, 4h …
	secs := []int{5, 15, 60, 300, 900, 3600, 7200, 14400}
	if attempt < 1 {
		attempt = 1
	}
	if attempt <= len(secs) {
		return time.Duration(secs[attempt-1]) * time.Second
	}
	return time.Duration(secs[len(secs)-1]) * time.Second
}

// notifyJobTerminal enqueues a durable outbox row when AI_CLOUDHUB_JOB_WEBHOOK_URL is set.
// Delivery is at-least-once via ProcessWebhookOutbox / StartWebhookWorker.
// When AI_CLOUDHUB_JOB_WEBHOOK_SECRET is set, each attempt signs HMAC-SHA256 over "timestamp.body".
func (s *Service) notifyJobTerminal(j *Job) {
	if j == nil || jobWebhookURL() == "" {
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
	now := time.Now().UTC()
	ev := webhookEvent{
		EventID:    uuid.NewString(),
		Event:      evName,
		OccurredAt: now,
		Job:        j,
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	row := &store.WebhookOutbox{
		ID:            ev.EventID,
		JobID:         j.ID,
		UserID:        j.UserID,
		Event:         evName,
		PayloadJSON:   payload,
		Status:        "pending",
		Attempts:      0,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.store.EnqueueWebhookOutbox(row); err != nil {
		return
	}
	// Snappy first attempt without waiting for poll tick.
	go func() { _ = s.ProcessWebhookOutbox(8) }()
}

// StartWebhookWorker polls the durable outbox until ctx is cancelled.
// Also purges old delivered/dead rows per AI_CLOUDHUB_JOB_WEBHOOK_RETAIN_SEC.
// No-op when AI_CLOUDHUB_JOB_WEBHOOK_URL is unset.
func (s *Service) StartWebhookWorker(ctx context.Context) {
	if jobWebhookURL() == "" {
		return
	}
	go func() {
		tick := time.NewTicker(jobWebhookPollInterval())
		defer tick.Stop()
		// Immediate pass on start (recover after restart).
		_ = s.ProcessWebhookOutbox(32)
		_, _ = s.PurgeWebhookOutbox(0)
		lastPurge := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_ = s.ProcessWebhookOutbox(32)
				if time.Since(lastPurge) >= jobWebhookPurgeInterval() {
					_, _ = s.PurgeWebhookOutbox(0)
					lastPurge = time.Now()
				}
			}
		}
	}()
}

// PurgeWebhookOutbox deletes delivered/dead rows older than retain TTL.
// olderThan zero uses AI_CLOUDHUB_JOB_WEBHOOK_RETAIN_SEC (0 env = no-op).
// limit 0 uses 500. Returns deleted count.
func (s *Service) PurgeWebhookOutbox(olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		olderThan = jobWebhookRetain()
	}
	if olderThan <= 0 {
		return 0, nil
	}
	n, err := s.store.PurgeWebhookOutbox(time.Now().UTC().Add(-olderThan), 500)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		metrics.AddJobWebhookPurged(uint64(n))
	}
	return n, nil
}

// AdminPurgeWebhooks force-purges terminal outbox rows older than olderThan.
// olderThan <= 0 uses configured retain (or 7d if retain disabled).
func (s *Service) AdminPurgeWebhooks(olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		olderThan = jobWebhookRetain()
		if olderThan <= 0 {
			olderThan = 7 * 24 * time.Hour
		}
	}
	n, err := s.store.PurgeWebhookOutbox(time.Now().UTC().Add(-olderThan), 2000)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		metrics.AddJobWebhookPurged(uint64(n))
	}
	return n, nil
}

// ProcessWebhookOutbox delivers due pending outbox rows (batch). Returns how many were delivered.
// Serialized per process; at-least-once across replicas (receivers should key on event_id).
func (s *Service) ProcessWebhookOutbox(limit int) int {
	url := jobWebhookURL()
	if url == "" {
		return 0
	}
	if limit <= 0 {
		limit = 32
	}
	webhookProcessMu.Lock()
	defer webhookProcessMu.Unlock()
	due, err := s.store.ListDueWebhookOutbox(time.Now().UTC(), limit)
	if err != nil || len(due) == 0 {
		return 0
	}
	maxAtt := jobWebhookMaxAttempts()
	secret := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_SECRET"))
	client := &http.Client{Timeout: 5 * time.Second}
	delivered := 0
	for _, row := range due {
		if row == nil {
			continue
		}
		// Skip if another path already finished this row.
		fresh, err := s.store.GetWebhookOutbox(row.ID)
		if err != nil || fresh.Status != "pending" {
			continue
		}
		ok, derr := deliverWebhookOnce(client, url, secret, fresh)
		now := time.Now().UTC()
		fresh.Attempts++
		fresh.UpdatedAt = now
		if ok {
			fresh.Status = "delivered"
			fresh.DeliveredAt = now
			fresh.LastError = ""
			if err := s.store.UpdateWebhookOutbox(fresh); err == nil {
				metrics.IncJobWebhook()
				delivered++
			}
			continue
		}
		metrics.IncJobWebhookFail()
		errMsg := derr
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
		fresh.LastError = errMsg
		if fresh.Attempts >= maxAtt {
			fresh.Status = "dead"
			_ = s.store.UpdateWebhookOutbox(fresh)
			metrics.IncJobWebhookDead()
			continue
		}
		fresh.NextAttemptAt = now.Add(webhookBackoff(fresh.Attempts))
		_ = s.store.UpdateWebhookOutbox(fresh)
	}
	return delivered
}

// deliverWebhookOnce POSTs payload; returns (ok, errorDetail).
func deliverWebhookOnce(client *http.Client, url, secret string, row *store.WebhookOutbox) (bool, string) {
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(row.PayloadJSON))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AI-cloudhub-job-webhook/1")
	req.Header.Set("X-AI-Cloudhub-Timestamp", ts)
	req.Header.Set("X-AI-Cloudhub-Event-Id", row.ID)
	req.Header.Set("X-AI-Cloudhub-Event", row.Event)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(ts + "."))
		_, _ = mac.Write(row.PayloadJSON)
		req.Header.Set("X-AI-Cloudhub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	res, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return false, fmt.Sprintf("HTTP %d", res.StatusCode)
	}
	return true, ""
}

// WebhookOutboxView is the admin/API shape for outbox rows.
type WebhookOutboxView struct {
	ID            string     `json:"id"`
	JobID         string     `json:"job_id"`
	UserID        string     `json:"user_id"`
	Event         string     `json:"event"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	// Payload is the full envelope JSON (included on get / after retry).
	Payload json.RawMessage `json:"payload,omitempty"`
}

func webhookViewFromStore(e *store.WebhookOutbox, withPayload bool) *WebhookOutboxView {
	if e == nil {
		return nil
	}
	v := &WebhookOutboxView{
		ID:            e.ID,
		JobID:         e.JobID,
		UserID:        e.UserID,
		Event:         e.Event,
		Status:        e.Status,
		Attempts:      e.Attempts,
		NextAttemptAt: e.NextAttemptAt,
		LastError:     e.LastError,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
	}
	if !e.DeliveredAt.IsZero() {
		t := e.DeliveredAt
		v.DeliveredAt = &t
	}
	if withPayload && len(e.PayloadJSON) > 0 {
		v.Payload = json.RawMessage(append([]byte(nil), e.PayloadJSON...))
	}
	return v
}

// AdminWebhookFilter filters admin outbox list / batch retry.
type AdminWebhookFilter struct {
	Status string // empty = all (list) or dead (batch retry default)
	JobID  string
	UserID string
	Limit  int
}

// AdminListWebhooks lists outbox rows (admin). List items omit payload (use AdminGetWebhook).
func (s *Service) AdminListWebhooks(f AdminWebhookFilter) []*WebhookOutboxView {
	list, err := s.store.ListWebhookOutbox(store.WebhookOutboxFilter{
		Status: strings.TrimSpace(f.Status),
		JobID:  strings.TrimSpace(f.JobID),
		UserID: strings.TrimSpace(f.UserID),
		Limit:  f.Limit,
	})
	if err != nil {
		return nil
	}
	out := make([]*WebhookOutboxView, 0, len(list))
	for _, e := range list {
		out = append(out, webhookViewFromStore(e, false))
	}
	return out
}

// AdminGetWebhook returns one outbox row including payload (admin).
func (s *Service) AdminGetWebhook(id string) (*WebhookOutboxView, error) {
	e, err := s.store.GetWebhookOutbox(strings.TrimSpace(id))
	if err != nil {
		return nil, fmt.Errorf("webhook outbox not found")
	}
	return webhookViewFromStore(e, true), nil
}

// requeueWebhookRow resets a row to pending for redelivery (same event_id/payload).
func (s *Service) requeueWebhookRow(e *store.WebhookOutbox) error {
	if e == nil {
		return fmt.Errorf("webhook outbox not found")
	}
	now := time.Now().UTC()
	e.Status = "pending"
	e.Attempts = 0
	// Slightly in the past so string/keyset due queries always pick it up immediately.
	e.NextAttemptAt = now.Add(-time.Second)
	e.LastError = ""
	e.DeliveredAt = time.Time{}
	e.UpdatedAt = now
	return s.store.UpdateWebhookOutbox(e)
}

// AdminRetryWebhook requeues a dead/pending/delivered row for immediate delivery (admin).
// Resets status to pending, attempts to 0, next_attempt_at to now; same event_id/payload.
// Kicks ProcessWebhookOutbox asynchronously when WEBHOOK_URL is configured.
func (s *Service) AdminRetryWebhook(id string) (*WebhookOutboxView, error) {
	e, err := s.store.GetWebhookOutbox(strings.TrimSpace(id))
	if err != nil {
		return nil, fmt.Errorf("webhook outbox not found")
	}
	if err := s.requeueWebhookRow(e); err != nil {
		return nil, err
	}
	if jobWebhookURL() != "" {
		go func() { _ = s.ProcessWebhookOutbox(8) }()
	}
	return webhookViewFromStore(e, true), nil
}

// AdminRetryWebhooksBatch requeues up to limit outbox rows matching filter (admin).
// Status defaults to "dead"; allowed: pending|delivered|dead. Optional JobID/UserID scope.
// Limit default 100, max 500. Kicks one delivery pass when any requeued and URL set.
func (s *Service) AdminRetryWebhooksBatch(f AdminWebhookFilter) (int, error) {
	status := strings.TrimSpace(f.Status)
	if status == "" {
		status = "dead"
	}
	switch status {
	case "pending", "delivered", "dead":
	default:
		return 0, fmt.Errorf("status must be pending, delivered, or dead")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	list, err := s.store.ListWebhookOutbox(store.WebhookOutboxFilter{
		Status: status,
		JobID:  strings.TrimSpace(f.JobID),
		UserID: strings.TrimSpace(f.UserID),
		Limit:  limit,
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range list {
		if err := s.requeueWebhookRow(e); err != nil {
			continue
		}
		n++
	}
	if n > 0 && jobWebhookURL() != "" {
		go func() { _ = s.ProcessWebhookOutbox(32) }()
	}
	return n, nil
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
	s.notifyJobTerminal(j)
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
	return s.cancelStoreJob(sj, "")
}

// AdminCancel cancels any non-terminal job by id (no user ownership check).
// optionalNote is appended as "admin cancel: …" when non-empty.
func (s *Service) AdminCancel(id, optionalNote string) (*Job, error) {
	sj, err := s.store.GetJobByID(strings.TrimSpace(id))
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	return s.cancelStoreJob(sj, strings.TrimSpace(optionalNote))
}

// AdminRelease returns a running/dispatched job to pending so another BYOC runner can claim it.
// reason is required for audit trail (appended as released: admin: …).
func (s *Service) AdminRelease(id, reason string) (*Job, error) {
	sj, err := s.store.GetJobByID(strings.TrimSpace(id))
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "admin force release"
	} else {
		reason = "admin: " + reason
	}
	return s.ReleaseToPending(sj.UserID, sj.ID, reason)
}

func (s *Service) cancelStoreJob(sj *store.Job, adminNote string) (*Job, error) {
	if sj.Status == string(StatusSucceeded) || sj.Status == string(StatusFailed) {
		return nil, fmt.Errorf("job already finished")
	}
	if sj.Status == string(StatusCancelled) {
		return jobFromStore(sj), nil // idempotent
	}
	sj.Status = string(StatusCancelled)
	if adminNote != "" {
		sj.Note = appendJobNote(sj.Note, "admin cancel: "+adminNote)
	}
	sj.HeartbeatAt = time.Time{}
	sj.ClaimedAt = time.Time{}
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	j := jobFromStore(sj)
	s.notifyJobTerminal(j)
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
