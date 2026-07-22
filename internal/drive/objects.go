package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/awmbtc/AI-cloudhub/internal/s3store"
)

// ObjectsInventory is a live listing of objects under a drive prefix.
type ObjectsInventory struct {
	DriveID      string                   `json:"drive_id"`
	Bucket       string                   `json:"bucket"`
	Prefix       string                   `json:"prefix"`
	Count        int                      `json:"count"`
	TotalBytes   int64                    `json:"total_bytes"`
	Truncated    bool                     `json:"truncated"`
	WithVersions bool                     `json:"with_versions"`
	Entries      []s3store.InventoryEntry `json:"entries"`
	Note         string                   `json:"note"`
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
	m, resolved, k, err := s.resolveObjectKey(userID, driveID, key)
	if err != nil {
		return nil, err
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

// ObjectPresignGet returns a short-lived GET URL (optional versionId). Bytes go client↔storage.
func (s *Service) ObjectPresignGet(userID, driveID, key, versionID string, ttlMin int) (map[string]interface{}, error) {
	m, resolved, k, err := s.resolveObjectKey(userID, driveID, key)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(ttlMin) * time.Minute
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := s3store.New(resolved.Endpoint, resolved.AccessKey, resolved.SecretKey, resolved.Region, resolved.UseSSL)
	if err != nil {
		return nil, err
	}
	u, err := st.PresignGetVersion(ctx, m.Bucket, k, versionID, ttl)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"drive_id":   driveID,
		"bucket":     m.Bucket,
		"key":        k,
		"version_id": versionID,
		"url":        u.String(),
		"expires_in": int64(ttl.Seconds()),
		"note":       "Presigned GET — download directly from object store, not via control plane.",
	}, nil
}

// ObjectRestoreVersion performs server-side copy version → current key on BYOS.
// Control plane only issues S3 CopyObject using user credentials (no body proxy).
func (s *Service) ObjectRestoreVersion(userID, driveID, key, versionID string) (map[string]interface{}, error) {
	if strings.TrimSpace(versionID) == "" {
		return nil, fmt.Errorf("version_id required")
	}
	m, resolved, k, err := s.resolveObjectKey(userID, driveID, key)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := s3store.New(resolved.Endpoint, resolved.AccessKey, resolved.SecretKey, resolved.Region, resolved.UseSSL)
	if err != nil {
		return nil, err
	}
	if err := st.CopyVersionToCurrent(ctx, m.Bucket, k, versionID); err != nil {
		return nil, fmt.Errorf("copy version: %w (bucket versioning must be enabled)", err)
	}
	return map[string]interface{}{
		"status":     "restored",
		"drive_id":   driveID,
		"bucket":     m.Bucket,
		"key":        k,
		"version_id": versionID,
		"note":       "Server-side copy on object store completed. Remount/refresh to see data.",
	}, nil
}

// ObjectRestorePlan returns multi-path restore guidance (presign + CLI + optional api_restore).
func (s *Service) ObjectRestorePlan(userID, driveID, key, versionID string, ttlMin int) (map[string]interface{}, error) {
	hint, err := s.ObjectVersionHint(userID, driveID, key, versionID)
	if err != nil {
		return nil, err
	}
	presign, perr := s.ObjectPresignGet(userID, driveID, key, versionID, ttlMin)
	out := map[string]interface{}{
		"drive_id":    driveID,
		"key":         hint["key"],
		"version_id":  versionID,
		"cli_hint":    hint["hint"],
		"api_restore": "POST /v1/drives/{id}/objects/restore-version {key, version_id}",
		"note":        "Prefer api_restore (server-side copy) when versioning is enabled; else use presign or CLI on user side.",
	}
	if perr == nil {
		out["presign"] = presign
	} else {
		out["presign_error"] = perr.Error()
	}
	return out, nil
}

// resolveObjectKey loads drive + provider and scopes key under drive prefix.
func (s *Service) resolveObjectKey(userID, driveID, key string) (*Map, *provider.Resolved, string, error) {
	m, err := s.Get(userID, driveID)
	if err != nil {
		return nil, nil, "", err
	}
	if s.providers == nil {
		return nil, nil, "", fmt.Errorf("providers not configured")
	}
	resolved, _, err := s.providers.ResolveRecord(userID, m.ProviderID)
	if err != nil {
		return nil, nil, "", err
	}
	k := strings.TrimLeft(strings.TrimSpace(key), "/")
	if k == "" {
		return nil, nil, "", fmt.Errorf("key required")
	}
	// Prevent path traversal into sibling prefixes.
	if strings.Contains(k, "..") {
		return nil, nil, "", fmt.Errorf("invalid key")
	}
	prefix := strings.Trim(m.Prefix, "/")
	if prefix != "" {
		if k != prefix && !strings.HasPrefix(k, prefix+"/") {
			k = prefix + "/" + k
		}
	}
	return m, resolved, k, nil
}
