package job

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
	AgentID          string    `json:"agent_id,omitempty"`            // creator agent
	ClaimedByAgentID string    `json:"claimed_by_agent_id,omitempty"` // last claimer agent
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CreateInput for new job.
type CreateInput struct {
	DriveID    string   `json:"drive_id"`
	BindingID  string   `json:"binding_id"`
	Mode       string   `json:"mode"`
	Command    []string `json:"command"`
	RegionHint string   `json:"region_hint"`
	Note       string   `json:"note"`
	// AgentID set by control plane from principal (not client spoofable).
	AgentID string `json:"-"`
}

// Service is a durable BYOC job queue.
type Service struct {
	store store.Store
}

// NewService creates a job service backed by store.
func NewService(st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st}
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
		AgentID:     strings.TrimSpace(in.AgentID),
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

// List returns jobs for user.
func (s *Service) List(userID string) []*Job {
	list, err := s.store.ListJobs(userID)
	if err != nil {
		return nil
	}
	out := make([]*Job, 0, len(list))
	for _, sj := range list {
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
func (s *Service) ClaimNext(userID, claimedByAgentID string) (*Job, error) {
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
	sj, err := s.store.ClaimPendingJob(userID, id, claimedByAgentID)
	if err != nil {
		return nil, err
	}
	return jobFromStore(sj), nil
}

// ReleaseToPending returns a running job to pending so another BYOC runner can claim it.
// Used when a claim succeeded but agent policy/drive allowlist rejects the job's drive.
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

// Complete sets terminal status.
func (s *Service) Complete(userID, id string, ok bool, note string) (*Job, error) {
	sj, err := s.store.GetJob(userID, id)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}
	if ok {
		sj.Status = string(StatusSucceeded)
	} else {
		sj.Status = string(StatusFailed)
	}
	if note != "" {
		sj.Note = note
	}
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	return jobFromStore(sj), nil
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
	sj.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(sj); err != nil {
		return nil, err
	}
	return jobFromStore(sj), nil
}

func jobFromStore(sj *store.Job) *Job {
	var cmd []string
	_ = json.Unmarshal(sj.CommandJSON, &cmd)
	return &Job{
		ID:               sj.ID,
		UserID:           sj.UserID,
		DriveID:          sj.DriveID,
		BindingID:        sj.BindingID,
		Mode:             sj.Mode,
		Command:          cmd,
		Status:           Status(sj.Status),
		RegionHint:       sj.RegionHint,
		Note:             sj.Note,
		AgentID:          sj.AgentID,
		ClaimedByAgentID: sj.ClaimedByAgentID,
		CreatedAt:        sj.CreatedAt,
		UpdatedAt:        sj.UpdatedAt,
	}
}
