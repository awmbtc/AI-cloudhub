package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/s3store"
)

// ObjectsInventory is a live listing of objects under a drive prefix.
type ObjectsInventory struct {
	DriveID    string                   `json:"drive_id"`
	Bucket     string                   `json:"bucket"`
	Prefix     string                   `json:"prefix"`
	Count      int                      `json:"count"`
	TotalBytes int64                    `json:"total_bytes"`
	Truncated  bool                     `json:"truncated"`
	WithVersions bool                   `json:"with_versions"`
	Entries    []s3store.InventoryEntry `json:"entries"`
	Note       string                   `json:"note"`
}

// ListObjects lists live object metadata for a drive (BYOS; no body download).
func (s *Service) ListObjects(userID, driveID string, maxKeys int, withVersions bool) (*ObjectsInventory, error) {
	m, err := s.Get(userID, driveID)
	if err != nil {
		return nil, err
	}
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
	var entries []s3store.InventoryEntry
	var truncated bool
	if withVersions {
		entries, truncated, err = st.ListInventoryVersions(ctx, m.Bucket, prefix, maxKeys)
	} else {
		entries, truncated, err = st.ListInventory(ctx, m.Bucket, prefix, maxKeys)
	}
	if err != nil {
		return nil, err
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	note := "Live inventory (metadata only). Control plane does not proxy object bytes."
	if withVersions {
		note += " with_versions=true requires bucket versioning; otherwise version_id may be empty."
	}
	return &ObjectsInventory{
		DriveID:      m.ID,
		Bucket:       m.Bucket,
		Prefix:       prefix,
		Count:        len(entries),
		TotalBytes:   total,
		Truncated:    truncated,
		WithVersions: withVersions,
		Entries:      entries,
		Note:         note,
	}, nil
}

// ObjectVersionHint returns CLI hints to fetch a versioned object from BYOS.
func (s *Service) ObjectVersionHint(userID, driveID, key, versionID string) (map[string]interface{}, error) {
	m, err := s.Get(userID, driveID)
	if err != nil {
		return nil, err
	}
	resolved, _, err := s.providers.ResolveRecord(userID, m.ProviderID)
	if err != nil {
		return nil, err
	}
	// Ensure key is under drive prefix
	prefix := strings.Trim(m.Prefix, "/")
	k := strings.TrimLeft(key, "/")
	if prefix != "" && !strings.HasPrefix(k, prefix+"/") && k != prefix {
		// allow relative to prefix
		k = prefix + "/" + k
	}
	hint := s3store.RestoreHint(resolved.Endpoint, m.Bucket, k, versionID, resolved.UseSSL)
	return map[string]interface{}{
		"drive_id":   driveID,
		"bucket":     m.Bucket,
		"key":        k,
		"version_id": versionID,
		"hint":       hint,
		"note":       "BYOS fetch — platform does not download/restore bytes for you.",
	}, nil
}
