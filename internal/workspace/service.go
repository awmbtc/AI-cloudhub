package workspace

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/s3store"
	"github.com/google/uuid"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// Meta is control-plane metadata for a workspace (in-memory for MVP).
type Meta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`
	Bucket    string    `json:"bucket"`
	CreatedAt time.Time `json:"created_at"`
}

// Service maps workspaces to S3 buckets and exposes file operations.
type Service struct {
	store        *s3store.Store
	bucketPrefix string
	presignTTL   time.Duration

	mu   sync.RWMutex
	byID map[string]*Meta
}

// NewService constructs a workspace service.
func NewService(store *s3store.Store, bucketPrefix string, presignTTL time.Duration) *Service {
	return &Service{
		store:        store,
		bucketPrefix: bucketPrefix,
		presignTTL:   presignTTL,
		byID:         make(map[string]*Meta),
	}
}

// Create provisions a workspace bucket for owner.
func (s *Service) Create(ctx context.Context, ownerID, name string) (*Meta, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name required")
	}
	id := uuid.NewString()
	// MinIO bucket names: 3-63 chars, lowercase, dns-compatible
	bucket := s.bucketPrefix + shortID(id)
	if !slugRe.MatchString(bucket) {
		// fallback safe name
		bucket = s.bucketPrefix + strings.ReplaceAll(id, "-", "")[:12]
	}

	if err := s.store.EnsureBucket(ctx, bucket); err != nil {
		return nil, fmt.Errorf("ensure bucket: %w", err)
	}

	m := &Meta{
		ID:        id,
		Name:      name,
		OwnerID:   ownerID,
		Bucket:    bucket,
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.byID[id] = m
	s.mu.Unlock()
	return m, nil
}

// Get returns workspace metadata.
func (s *Service) Get(id string) (*Meta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("workspace not found")
	}
	return m, nil
}

// ListByOwner returns workspaces for a user.
func (s *Service) ListByOwner(ownerID string) []*Meta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Meta
	for _, m := range s.byID {
		if m.OwnerID == ownerID {
			out = append(out, m)
		}
	}
	return out
}

// ListFiles lists objects under prefix in the workspace.
func (s *Service) ListFiles(ctx context.Context, wsID, prefix string) ([]s3store.ObjectInfo, error) {
	m, err := s.Get(wsID)
	if err != nil {
		return nil, err
	}
	prefix = strings.TrimLeft(prefix, "/")
	return s.store.List(ctx, m.Bucket, prefix)
}

// PutFile streams a file into the workspace at key (relative path).
func (s *Service) PutFile(ctx context.Context, wsID, key string, r io.Reader, size int64, contentType string) error {
	m, err := s.Get(wsID)
	if err != nil {
		return err
	}
	key, err = cleanKey(key)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, m.Bucket, key, r, size, contentType)
}

// GetFile opens an object for reading.
func (s *Service) GetFile(ctx context.Context, wsID, key string) (io.ReadCloser, error) {
	m, err := s.Get(wsID)
	if err != nil {
		return nil, err
	}
	key, err = cleanKey(key)
	if err != nil {
		return nil, err
	}
	return s.store.Get(ctx, m.Bucket, key)
}

// DeleteFile removes one object.
func (s *Service) DeleteFile(ctx context.Context, wsID, key string) error {
	m, err := s.Get(wsID)
	if err != nil {
		return err
	}
	key, err = cleanKey(key)
	if err != nil {
		return err
	}
	return s.store.Delete(ctx, m.Bucket, key)
}

// PresignUpload returns a PUT URL for direct-to-MinIO upload.
func (s *Service) PresignUpload(ctx context.Context, wsID, key string) (*url.URL, string, error) {
	m, err := s.Get(wsID)
	if err != nil {
		return nil, "", err
	}
	key, err = cleanKey(key)
	if err != nil {
		return nil, "", err
	}
	u, err := s.store.PresignPut(ctx, m.Bucket, key, s.presignTTL)
	if err != nil {
		return nil, "", err
	}
	return u, m.Bucket, nil
}

// PresignDownload returns a GET URL for direct download.
func (s *Service) PresignDownload(ctx context.Context, wsID, key string) (*url.URL, error) {
	m, err := s.Get(wsID)
	if err != nil {
		return nil, err
	}
	key, err = cleanKey(key)
	if err != nil {
		return nil, err
	}
	return s.store.PresignGet(ctx, m.Bucket, key, s.presignTTL)
}

// AgentMountHint returns rclone-oriented hints for mounting a workspace as a local path.
func (s *Service) AgentMountHint(wsID, endpoint, accessKey, secretKey string, useSSL bool) (map[string]string, error) {
	m, err := s.Get(wsID)
	if err != nil {
		return nil, err
	}
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	remote := fmt.Sprintf("%s://%s", scheme, endpoint)
	return map[string]string{
		"workspace_id": m.ID,
		"bucket":       m.Bucket,
		"rclone_remote_type": "s3",
		"rclone_env_example": fmt.Sprintf(
			"RCLONE_CONFIG_MYS3_TYPE=s3\nRCLONE_CONFIG_MYS3_PROVIDER=Minio\nRCLONE_CONFIG_MYS3_ENDPOINT=%s\nRCLONE_CONFIG_MYS3_ACCESS_KEY_ID=%s\nRCLONE_CONFIG_MYS3_SECRET_ACCESS_KEY=%s\nRCLONE_CONFIG_MYS3_FORCE_PATH_STYLE=true",
			remote, accessKey, secretKey,
		),
		"mount_command": fmt.Sprintf(
			"rclone mount mys3:%s /workspace --vfs-cache-mode full --daemon",
			m.Bucket,
		),
		"agent_workdir": "/workspace",
		"note":          "Point Agent working_directory to /workspace. Writes go to MinIO without a separate upload step.",
	}, nil
}

func cleanKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	key = strings.TrimLeft(key, "/")
	if key == "" {
		return "", fmt.Errorf("key required")
	}
	if strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid key")
	}
	return key, nil
}

func shortID(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
