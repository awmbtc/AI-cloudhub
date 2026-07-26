package job

import (
	"bytes"
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
	AgentID          string    `json:"agent_id,omitempty"`            // creator agent
	ClaimedByAgentID string    `json:"claimed_by_agent_id,omitempty"` // last claimer agent
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
func (s *Service) Create(userID string, in CreateInput) (*Job, error) {
	if strings.TrimSpace(in.DriveID) == "" {
		return nil, fmt.Errorf("drive_id required")
	}
	if len(in.Command) == 0 {
		return nil, fmt.Errorf("command required (runs on BYOC runner, not platform pool)")
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
	sj := &store.Job{
		ID:          uuid.NewString(),
		UserID:      userID,
		DriveID:     in.DriveID,
		BindingID:   in.BindingID,
		Mode:        mode,
		CommandJSON: cmdJSON,
		Status:      string(StatusPending),
		RegionHint:  in.RegionHint,
		Note:        note,
		ConnectorID: strings.TrimSpace(in.ConnectorID),
		AgentID:     strings.TrimSpace(in.AgentID),
		TimeoutSec:  timeoutSec,
		MaxAttempts: maxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.CreateJob(sj); err != nil {
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
}

// List returns jobs for user, optionally filtered by agent ids and/or status.
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

// ClaimNext claims the oldest pending job for the user (BYOC worker).
// Lists claimable jobs, then tries atomic claim on each until one succeeds
// (another worker may have claimed in between). claimedByAgentID may be empty (human).
// Stale running jobs (no heartbeat within lease) are reclaimed first.
func (s *Service) ClaimNext(userID, claimedByAgentID string) (*Job, error) {
	_, _ = s.ReclaimStale(userID)
	list, err := s.store.ListPendingJobs(userID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no pending jobs")
	}
	// sqlite/postgres return oldest-first; memory map order is undefined — sort.
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	for _, j := range list {
		if j.Status != string(StatusPending) && j.Status != string(StatusDispatched) {
			continue
		}
		claimed, err := s.store.ClaimPendingJob(userID, j.ID, claimedByAgentID)
		if err != nil {
			// Already claimed or gone — try next.
			continue
		}
		return jobFromStore(claimed), nil
	}
	return nil, fmt.Errorf("no pending jobs")
}

// Claim marks a pending job as running (atomic: only if still claimable).
// claimedByAgentID may be empty (human runner).
func (s *Service) Claim(userID, id, claimedByAgentID string) (*Job, error) {
	_, _ = s.ReclaimStale(userID)
	sj, err := s.store.ClaimPendingJob(userID, id, claimedByAgentID)
	if err != nil {
		return nil, err
	}
	return jobFromStore(sj), nil
}

// Heartbeat refreshes lease on a running job. Runners should call periodically.
func (s *Service) Heartbeat(userID, id string) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
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
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	j := jobFromStore(sj)
	notifyJobTerminal(j)
	return j, nil
}

// notifyJobTerminal best-effort POSTs job JSON to AI_CLOUDHUB_JOB_WEBHOOK_URL (async).
func notifyJobTerminal(j *Job) {
	if j == nil {
		return
	}
	url := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_WEBHOOK_URL"))
	if url == "" {
		return
	}
	payload, err := json.Marshal(j)
	if err != nil {
		return
	}
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AI-cloudhub-job-webhook/1")
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

// ClaimNextFiltered claims the oldest pending job whose driveID passes allow.
// allow(driveID) should return "" if allowed, or a short deny reason if not.
//
// Jobs are filtered **before** claim using the pending list (avoids reclaim loops).
// After a successful atomic claim, allow is re-checked; on deny the job is
// ReleaseToPending and the scan continues. If allow is nil, behaves like ClaimNext.
// claimedByAgentID may be empty (human).
func (s *Service) ClaimNextFiltered(userID, claimedByAgentID string, allow func(driveID string) string) (*Job, error) {
	if allow == nil {
		return s.ClaimNext(userID, claimedByAgentID)
	}
	_, _ = s.ReclaimStale(userID)
	list, err := s.store.ListPendingJobs(userID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no pending jobs")
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	var lastDeny string
	var skipped int
	for _, cand := range list {
		if cand.Status != string(StatusPending) && cand.Status != string(StatusDispatched) {
			continue
		}
		if reason := allow(cand.DriveID); reason != "" {
			lastDeny = reason
			skipped++
			continue
		}
		claimed, err := s.store.ClaimPendingJob(userID, cand.ID, claimedByAgentID)
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
func (s *Service) Complete(userID, id string, in CompleteInput) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
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
		ClaimedByAgentID: sj.ClaimedByAgentID,
		DurationMs:       sj.DurationMs,
		TimeoutSec:       sj.TimeoutSec,
		AttemptCount:     sj.AttemptCount,
		MaxAttempts:      sj.MaxAttempts,
		Stdout:           sj.Stdout,
		Stderr:           sj.Stderr,
		StdoutTruncated:  sj.StdoutTruncated,
		StderrTruncated:  sj.StderrTruncated,
		CreatedAt:        sj.CreatedAt,
		UpdatedAt:        sj.UpdatedAt,
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
