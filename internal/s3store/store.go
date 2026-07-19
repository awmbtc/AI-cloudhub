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

// PresignPut returns a time-limited upload URL.
func (s *Store) PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (*url.URL, error) {
	return s.client.Presign(ctx, "PUT", bucket, key, ttl, nil)
}

// PresignGet returns a time-limited download URL.
func (s *Store) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (*url.URL, error) {
	return s.client.Presign(ctx, "GET", bucket, key, ttl, nil)
}

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
