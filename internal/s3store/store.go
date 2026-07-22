package s3store

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectInfo is a file or common-prefix entry under a workspace.
type ObjectInfo struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified,omitempty"`
	IsDir        bool      `json:"is_dir"`
}

// Store is an S3-compatible object storage adapter (MinIO today, swappable later).
type Store struct {
	client *minio.Client
	region string
}

// New creates a MinIO/S3 client.
func New(endpoint, accessKey, secretKey, region string, useSSL bool) (*Store, error) {
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &Store{client: cli, region: region}, nil
}

// Probe checks credential reachability with ListBuckets (lightweight health).
// Does not create resources. Caller should use a short context timeout.
func (s *Store) Probe(ctx context.Context) (bucketCount int, err error) {
	buckets, err := s.client.ListBuckets(ctx)
	if err != nil {
		return 0, err
	}
	return len(buckets), nil
}

// EnsureBucket creates the bucket if it does not exist.
func (s *Store) EnsureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s.region})
}

// Put uploads an object from a reader.
func (s *Store) Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

// Get streams an object body. Caller must Close the reader.
func (s *Store) Get(ctx context.Context, bucket, key string) (*minio.Object, error) {
	return s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
}

// Delete removes one object.
func (s *Store) Delete(ctx context.Context, bucket, key string) error {
	return s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

// List returns objects and directory prefixes under prefix (delimiter /).
func (s *Store) List(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	prefix = normalizePrefix(prefix)
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	}

	var out []ObjectInfo
	for obj := range s.client.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		// Common prefixes end with /
		if strings.HasSuffix(obj.Key, "/") {
			out = append(out, ObjectInfo{
				Key:   obj.Key,
				IsDir: true,
			})
			continue
		}
		// minio-go may return prefixes via Key when using non-recursive list
		if obj.Size == 0 && strings.HasSuffix(obj.Key, "/") {
			out = append(out, ObjectInfo{Key: obj.Key, IsDir: true})
			continue
		}
		out = append(out, ObjectInfo{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			IsDir:        false,
		})
	}
	return out, nil
}

// InventoryEntry is a recursive object listing row (metadata only).
type InventoryEntry struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"last_modified,omitempty"`
	// VersionID set when listing with WithVersions (bucket versioning enabled).
	VersionID string `json:"version_id,omitempty"`
	IsLatest  bool   `json:"is_latest,omitempty"`
	// DeleteMarker when version is a delete marker.
	DeleteMarker bool `json:"delete_marker,omitempty"`
}

// ListInventory recursively lists current objects under prefix up to maxKeys
// (default 1000, hard max 5000). Does not download bodies.
func (s *Store) ListInventory(ctx context.Context, bucket, prefix string, maxKeys int) (entries []InventoryEntry, truncated bool, err error) {
	return s.listInventory(ctx, bucket, prefix, maxKeys, false)
}

// ListInventoryVersions lists object versions when the bucket has versioning.
// If versioning is disabled, behavior falls back to current objects (no version_id).
func (s *Store) ListInventoryVersions(ctx context.Context, bucket, prefix string, maxKeys int) (entries []InventoryEntry, truncated bool, err error) {
	return s.listInventory(ctx, bucket, prefix, maxKeys, true)
}

func (s *Store) listInventory(ctx context.Context, bucket, prefix string, maxKeys int, withVersions bool) (entries []InventoryEntry, truncated bool, err error) {
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	if maxKeys > 5000 {
		maxKeys = 5000
	}
	prefix = normalizePrefix(prefix)
	opts := minio.ListObjectsOptions{
		Prefix:       prefix,
		Recursive:    true,
		WithVersions: withVersions,
	}
	for obj := range s.client.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return entries, false, obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") && obj.Size == 0 && obj.VersionID == "" {
			continue
		}
		entries = append(entries, InventoryEntry{
			Key:          obj.Key,
			Size:         obj.Size,
			ETag:         strings.Trim(obj.ETag, `"`),
			LastModified: obj.LastModified,
			VersionID:    obj.VersionID,
			IsLatest:     obj.IsLatest,
			DeleteMarker: obj.IsDeleteMarker,
		})
		if len(entries) >= maxKeys {
			return entries, true, nil
		}
	}
	return entries, false, nil
}

// RestoreHint returns a copy-paste rclone/aws CLI hint to fetch a specific version (BYOS).
func RestoreHint(endpoint, bucket, key, versionID string, useSSL bool) string {
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	if versionID == "" {
		return fmt.Sprintf("# current object: %s://%s/%s/%s", scheme, endpoint, bucket, key)
	}
	return fmt.Sprintf(
		"# Fetch version (BYOS; control plane does not proxy bytes):\n"+
			"# aws s3api get-object --bucket %s --key %s --version-id %s out.bin\n"+
			"# or rclone with version-aware remote if configured",
		bucket, key, versionID,
	)
}

// PresignPut returns a time-limited upload URL.
func (s *Store) PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (*url.URL, error) {
	return s.client.Presign(ctx, "PUT", bucket, key, ttl, nil)
}

// PresignGet returns a time-limited download URL.
func (s *Store) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (*url.URL, error) {
	return s.PresignGetVersion(ctx, bucket, key, "", ttl)
}

// PresignGetVersion returns a time-limited GET URL; versionID optional (S3 versioning).
func (s *Store) PresignGetVersion(ctx context.Context, bucket, key, versionID string, ttl time.Duration) (*url.URL, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	params := url.Values{}
	if versionID != "" {
		params.Set("versionId", versionID)
	}
	return s.client.Presign(ctx, httpMethodGet, bucket, key, ttl, params)
}

// CopyVersionToCurrent performs a server-side copy of versionID → current key
// on the object store (no bytes through control plane). Requires versioning.
func (s *Store) CopyVersionToCurrent(ctx context.Context, bucket, key, versionID string) error {
	if versionID == "" {
		return fmt.Errorf("version_id required")
	}
	_, err := s.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: bucket, Object: key},
		minio.CopySrcOptions{Bucket: bucket, Object: key, VersionID: versionID},
	)
	return err
}

// httpMethodGet avoids importing net/http just for the string in some trees.
const httpMethodGet = "GET"

func normalizePrefix(p string) string {
	p = strings.TrimLeft(p, "/")
	if p == "" {
		return ""
	}
	if !strings.HasSuffix(p, "/") {
		// listing a "folder": keep as-is if caller wants exact prefix;
		// API layer decides whether to append /
	}
	return p
}
