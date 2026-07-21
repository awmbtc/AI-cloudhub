package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/s3store"
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
	Label          string          `json:"label"`
	Note           string          `json:"note"`
	AgentID        string          `json:"agent_id"`
	Manifest       json.RawMessage `json:"manifest"` // optional client-supplied manifest blob
	IncludeObjects bool            `json:"include_objects"` // ListObjects inventory (metadata only)
	MaxObjects     int             `json:"max_objects"`     // default 1000, max 5000
}

// DefaultMaxSnapshotsPerDrive caps metadata snapshots per drive (B6 hardening).
const DefaultMaxSnapshotsPerDrive = 50

// CreateSnapshot stores drive map + optional manifest + optional object inventory.
// Object inventory is ListObjects metadata only (no body download / no version restore).
func (s *Service) CreateSnapshot(userID, driveID string, in SnapshotCreate) (*SnapshotView, error) {
	m, err := s.Get(userID, driveID)
	if err != nil {
		return nil, err
	}
	// Quota: reject when at cap.
	existing, err := s.store.ListSnapshots(userID, driveID, DefaultMaxSnapshotsPerDrive+1)
	if err != nil {
		return nil, err
	}
	if len(existing) >= DefaultMaxSnapshotsPerDrive {
		return nil, fmt.Errorf("snapshot quota exceeded: max %d per drive", DefaultMaxSnapshotsPerDrive)
	}
	kind := "ai-cloudhub.snapshot.v0"
	payload := map[string]interface{}{
		"kind":        kind,
		"drive":       m,
		"captured_at": time.Now().UTC(),
	}
	if len(in.Manifest) > 0 {
		var man interface{}
		if err := json.Unmarshal(in.Manifest, &man); err != nil {
			return nil, fmt.Errorf("manifest: %w", err)
		}
		payload["manifest"] = man
	}
	if in.IncludeObjects {
		inv, err := s.listObjectInventory(userID, m, in.MaxObjects)
		if err != nil {
			// Soft-fail: still save metadata snapshot, record inventory error.
			payload["objects"] = map[string]interface{}{
				"ok":    false,
				"error": err.Error(),
			}
		} else {
			kind = "ai-cloudhub.snapshot.v0+inventory"
			payload["kind"] = kind
			payload["objects"] = inv
		}
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

func (s *Service) listObjectInventory(userID string, m *Map, maxKeys int) (map[string]interface{}, error) {
	if s.providers == nil {
		return nil, fmt.Errorf("providers not configured")
	}
	resolved, _, err := s.providers.ResolveRecord(userID, m.ProviderID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := s3store.New(resolved.Endpoint, resolved.AccessKey, resolved.SecretKey, resolved.Region, resolved.UseSSL)
	if err != nil {
		return nil, err
	}
	prefix := m.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	entries, truncated, err := st.ListInventory(ctx, m.Bucket, prefix, maxKeys)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	return map[string]interface{}{
		"ok":          true,
		"bucket":      m.Bucket,
		"prefix":      prefix,
		"count":       len(entries),
		"total_bytes": total,
		"truncated":   truncated,
		"entries":     entries,
		"note":        "Metadata inventory only (key/size/etag/mtime). Not object-version restore.",
	}, nil
}

// DiffSnapshots compares object inventories of two snapshots (if present).
func (s *Service) DiffSnapshots(userID, driveID, idA, idB string) (map[string]interface{}, error) {
	a, err := s.store.GetSnapshot(userID, driveID, idA)
	if err != nil {
		return nil, fmt.Errorf("snapshot a not found")
	}
	b, err := s.store.GetSnapshot(userID, driveID, idB)
	if err != nil {
		return nil, fmt.Errorf("snapshot b not found")
	}
	mapA, errA := inventoryMap(a.PayloadJSON)
	mapB, errB := inventoryMap(b.PayloadJSON)
	out := map[string]interface{}{
		"drive_id":    driveID,
		"snapshot_a":  idA,
		"snapshot_b":  idB,
		"a_has_objects": errA == nil,
		"b_has_objects": errB == nil,
	}
	if errA != nil || errB != nil {
		out["error"] = "one or both snapshots lack object inventory; create with include_objects=true"
		if errA != nil {
			out["a_error"] = errA.Error()
		}
		if errB != nil {
			out["b_error"] = errB.Error()
		}
		return out, nil
	}
	var added, removed, changed []map[string]interface{}
	for k, eb := range mapB {
		ea, ok := mapA[k]
		if !ok {
			added = append(added, map[string]interface{}{"key": k, "size": eb.Size, "etag": eb.ETag})
			continue
		}
		if ea.ETag != eb.ETag || ea.Size != eb.Size {
			changed = append(changed, map[string]interface{}{
				"key": k, "size_a": ea.Size, "size_b": eb.Size, "etag_a": ea.ETag, "etag_b": eb.ETag,
			})
		}
	}
	for k, ea := range mapA {
		if _, ok := mapB[k]; !ok {
			removed = append(removed, map[string]interface{}{"key": k, "size": ea.Size, "etag": ea.ETag})
		}
	}
	out["added"] = added
	out["removed"] = removed
	out["changed"] = changed
	out["summary"] = map[string]int{
		"added": len(added), "removed": len(removed), "changed": len(changed),
		"count_a": len(mapA), "count_b": len(mapB),
	}
	return out, nil
}

func inventoryMap(payloadJSON []byte) (map[string]s3store.InventoryEntry, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, err
	}
	obj, ok := payload["objects"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no objects inventory")
	}
	if okFlag, _ := obj["ok"].(bool); !okFlag {
		return nil, fmt.Errorf("inventory not ok")
	}
	raw, err := json.Marshal(obj["entries"])
	if err != nil {
		return nil, err
	}
	var entries []s3store.InventoryEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	m := make(map[string]s3store.InventoryEntry, len(entries))
	for _, e := range entries {
		m[e.Key] = e
	}
	return m, nil
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

// RestoreSnapshot returns the payload for client/runtime rehydration.
// When apply is true, updates mutable drive fields (name, prefix, mount_point, region)
// from the snapshot. Does not change provider/bucket or object bytes.
func (s *Service) RestoreSnapshot(userID, driveID, id string, apply bool) (map[string]interface{}, error) {
	sn, err := s.store.GetSnapshot(userID, driveID, id)
	if err != nil {
		return nil, fmt.Errorf("snapshot not found")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(sn.PayloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("corrupt snapshot payload")
	}
	out := map[string]interface{}{
		"snapshot_id": sn.ID,
		"drive_id":    driveID,
		"label":       sn.Label,
		"note":        sn.Note,
		"created_at":  sn.CreatedAt,
		"payload":     payload,
		"applied":     false,
		"hint":        "Metadata restore. Object bytes are not versioned in v0. Pass apply=true to write name/prefix/mount_point/region from payload.drive.",
	}
	if !apply {
		return out, nil
	}
	driveRaw, ok := payload["drive"]
	if !ok {
		return nil, fmt.Errorf("snapshot has no drive payload")
	}
	b, err := json.Marshal(driveRaw)
	if err != nil {
		return nil, err
	}
	var snapMap Map
	if err := json.Unmarshal(b, &snapMap); err != nil {
		return nil, fmt.Errorf("invalid drive in snapshot: %w", err)
	}
	cur, err := s.Get(userID, driveID)
	if err != nil {
		return nil, err
	}
	// Only restore soft fields; keep id/provider/bucket/user.
	if strings.TrimSpace(snapMap.Name) != "" {
		cur.Name = snapMap.Name
	}
	cur.Prefix = snapMap.Prefix
	if strings.TrimSpace(snapMap.MountPoint) != "" {
		if err := validateMountPoint(snapMap.MountPoint); err != nil {
			return nil, fmt.Errorf("snapshot mount_point: %w", err)
		}
		cur.MountPoint = snapMap.MountPoint
	}
	cur.Region = snapMap.Region
	if err := s.store.UpdateDrive(mapToStore(cur)); err != nil {
		return nil, err
	}
	out["applied"] = true
	out["drive"] = cur
	out["hint"] = "Drive metadata applied. Re-issue STS session and remount. Object storage content is unchanged."
	return out, nil
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
