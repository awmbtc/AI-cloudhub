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
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	DriveID    string    `json:"drive_id"`
	BindingID  string    `json:"binding_id,omitempty"`
	Mode       string    `json:"mode"`
	Command    []string  `json:"command"`
	Status     Status    `json:"status"`
	RegionHint string    `json:"region_hint,omitempty"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateInput for new job.
type CreateInput struct {
	DriveID    string   `json:"drive_id"`
	BindingID  string   `json:"binding_id"`
	Mode       string   `json:"mode"`
	Command    []string `json:"command"`
	RegionHint string   `json:"region_hint"`
	Note       string   `json:"note"`
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
// (another worker may have claimed in between).
func (s *Service) ClaimNext(userID string) (*Job, error) {
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
		claimed, err := s.store.ClaimPendingJob(userID, j.ID)
		if err != nil {
			// Already claimed or gone — try next.
			continue
		}
		return jobFromStore(claimed), nil
	}
	return nil, fmt.Errorf("no pending jobs")
}

// Claim marks a pending job as running (atomic: only if still claimable).
func (s *Service) Claim(userID, id string) (*Job, error) {
	sj, err := s.store.ClaimPendingJob(userID, id)
	if err != nil {
		return nil, err
	}
	return jobFromStore(sj), nil
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
		ID:         sj.ID,
		UserID:     sj.UserID,
		DriveID:    sj.DriveID,
		BindingID:  sj.BindingID,
		Mode:       sj.Mode,
		Command:    cmd,
		Status:     Status(sj.Status),
		RegionHint: sj.RegionHint,
		Note:       sj.Note,
		CreatedAt:  sj.CreatedAt,
		UpdatedAt:  sj.UpdatedAt,
	}
}
