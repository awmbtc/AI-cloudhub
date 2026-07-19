package drive

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Barrier is a write-completion signal for a drive/session (ARCHITECTURE write barrier).
type Barrier struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	DriveID   string    `json:"drive_id"`
	DeviceID  string    `json:"device_id,omitempty"`
	Status    string    `json:"status"` // pending|ok|failed
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	DoneAt    time.Time `json:"done_at,omitempty"`
}

// CompleteBarrierInput from runtime after fsync/flush.
type CompleteBarrierInput struct {
	DriveID  string `json:"drive_id"`
	DeviceID string `json:"device_id"`
	Status   string `json:"status"` // ok|failed
	Note     string `json:"note"`
}

// barrier storage on Service
func (s *Service) ensureBarriers() {
	if s.barriers == nil {
		s.barriers = make(map[string]*Barrier)
	}
}

// BeginBarrier starts a write barrier record (optional explicit start).
func (s *Service) BeginBarrier(userID, driveID, deviceID string) (*Barrier, error) {
	if _, err := s.Get(userID, driveID); err != nil {
		return nil, err
	}
	b := &Barrier{
		ID:        uuid.NewString(),
		UserID:    userID,
		DriveID:   driveID,
		DeviceID:  deviceID,
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.ensureBarriers()
	s.barriers[b.ID] = b
	s.mu.Unlock()
	return b, nil
}

// CompleteBarrier marks flush done (Runtime after vfs flush / agent task complete).
func (s *Service) CompleteBarrier(userID string, in CompleteBarrierInput) (*Barrier, error) {
	if in.Status == "" {
		in.Status = "ok"
	}
	if in.Status != "ok" && in.Status != "failed" {
		return nil, fmt.Errorf("status must be ok or failed")
	}
	if _, err := s.Get(userID, in.DriveID); err != nil {
		return nil, err
	}
	b := &Barrier{
		ID:        uuid.NewString(),
		UserID:    userID,
		DriveID:   in.DriveID,
		DeviceID:  in.DeviceID,
		Status:    in.Status,
		Note:      in.Note,
		CreatedAt: time.Now().UTC(),
		DoneAt:    time.Now().UTC(),
	}
	s.mu.Lock()
	s.ensureBarriers()
	s.barriers[b.ID] = b
	// keep last 100 per process for MVP
	if len(s.barriers) > 500 {
		s.barriers = map[string]*Barrier{b.ID: b}
	}
	s.mu.Unlock()
	return b, nil
}

// ListBarriers returns recent barriers for a drive.
func (s *Service) ListBarriers(userID, driveID string) []*Barrier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Barrier
	for _, b := range s.barriers {
		if b.UserID == userID && (driveID == "" || b.DriveID == driveID) {
			out = append(out, b)
		}
	}
	return out
}
