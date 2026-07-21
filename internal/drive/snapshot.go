package drive

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// SnapshotView is the API representation of a workspace metadata snapshot.
type SnapshotView struct {
	ID        string          `json:"id"`
	DriveID   string          `json:"drive_id"`
	AgentID   string          `json:"agent_id,omitempty"`
	Label     string          `json:"label,omitempty"`
	Note      string          `json:"note,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// SnapshotCreate input.
type SnapshotCreate struct {
	Label       string          `json:"label"`
	Note        string          `json:"note"`
	AgentID     string          `json:"agent_id"`
	Manifest    json.RawMessage `json:"manifest"` // optional client-supplied manifest blob
}

// CreateSnapshot stores drive map + optional manifest (metadata only — not object storage).
func (s *Service) CreateSnapshot(userID, driveID string, in SnapshotCreate) (*SnapshotView, error) {
	m, err := s.Get(userID, driveID)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"kind":    "ai-cloudhub.snapshot.v0",
		"drive":   m,
		"captured_at": time.Now().UTC(),
	}
	if len(in.Manifest) > 0 {
		var man interface{}
		if err := json.Unmarshal(in.Manifest, &man); err != nil {
			return nil, fmt.Errorf("manifest: %w", err)
		}
		payload["manifest"] = man
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sn := &store.Snapshot{
		ID:          uuid.NewString(),
		UserID:      userID,
		DriveID:     driveID,
		AgentID:     strings.TrimSpace(in.AgentID),
		Label:       strings.TrimSpace(in.Label),
		Note:        strings.TrimSpace(in.Note),
		PayloadJSON: raw,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.CreateSnapshot(sn); err != nil {
		return nil, err
	}
	return snapshotView(sn), nil
}

// ListSnapshots returns recent snapshots for a drive.
func (s *Service) ListSnapshots(userID, driveID string, limit int) ([]*SnapshotView, error) {
	if _, err := s.Get(userID, driveID); err != nil {
		return nil, err
	}
	list, err := s.store.ListSnapshots(userID, driveID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*SnapshotView, 0, len(list))
	for _, sn := range list {
		out = append(out, snapshotView(sn))
	}
	return out, nil
}

// GetSnapshot returns one snapshot.
func (s *Service) GetSnapshot(userID, driveID, id string) (*SnapshotView, error) {
	sn, err := s.store.GetSnapshot(userID, driveID, id)
	if err != nil {
		return nil, fmt.Errorf("snapshot not found")
	}
	return snapshotView(sn), nil
}

// DeleteSnapshot removes a snapshot.
func (s *Service) DeleteSnapshot(userID, driveID, id string) error {
	if err := s.store.DeleteSnapshot(userID, driveID, id); err != nil {
		return fmt.Errorf("snapshot not found")
	}
	return nil
}

// RestoreSnapshot returns the payload for client/runtime rehydration (no automatic object rollback).
func (s *Service) RestoreSnapshot(userID, driveID, id string) (map[string]interface{}, error) {
	sn, err := s.store.GetSnapshot(userID, driveID, id)
	if err != nil {
		return nil, fmt.Errorf("snapshot not found")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(sn.PayloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("corrupt snapshot payload")
	}
	return map[string]interface{}{
		"snapshot_id": sn.ID,
		"drive_id":    driveID,
		"label":       sn.Label,
		"note":        sn.Note,
		"created_at":  sn.CreatedAt,
		"payload":     payload,
		"hint":        "Metadata restore only. Re-issue session / remount using payload.drive and payload.manifest. Object bytes are not versioned in v0.",
	}, nil
}

func snapshotView(sn *store.Snapshot) *SnapshotView {
	return &SnapshotView{
		ID:        sn.ID,
		DriveID:   sn.DriveID,
		AgentID:   sn.AgentID,
		Label:     sn.Label,
		Note:      sn.Note,
		Payload:   json.RawMessage(sn.PayloadJSON),
		CreatedAt: sn.CreatedAt,
	}
}
