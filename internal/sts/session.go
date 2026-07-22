package sts

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/manifest"
	"github.com/awmbtc/AI-cloudhub/internal/mountlib"
	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/google/uuid"
)

// Session is a short-lived mount grant for a runtime (hubd/runner).
// MVP: embeds resolved S3 creds with hard expiry; Runtime must refresh before ExpiresAt.
// Optional native / S3-compatible STS (best-effort, always falls back to embedded short session):
//   - minio → minio_sts; AWS s3 → aws_sts; OSS native → aliyun_sts; COS native → tencent_sts
//   - AI_CLOUDHUB_S3_STS / per-vendor → s3_sts for S3-compatible endpoints
// See Session.Note and docs/STS.md.
type Session struct {
	ID         string            `json:"id"`
	UserID     string            `json:"user_id"`
	DriveID    string            `json:"drive_id"`
	DeviceID   string            `json:"device_id,omitempty"`
	MountPoint string            `json:"mount_point"`
	Mode       string            `json:"mode"`
	ExpiresAt  time.Time         `json:"expires_at"`
	Spec       mountlib.Spec     `json:"spec"`
	Manifest   manifest.Document `json:"manifest"`
	Token      string            `json:"token"` // opaque; Runtime presents on refresh/report
	// Source: embedded|refresh|minio_sts|aws_sts|s3_sts|aliyun_sts|tencent_sts
	Source string `json:"source,omitempty"`
	// Note is an optional human-readable hint (e.g. why native STS was skipped/failed).
	Note string `json:"note,omitempty"`
}

// Service issues and validates mount sessions.
type Service struct {
	ttl     time.Duration
	apiBase string

	mu      sync.RWMutex
	byToken map[string]*Session
	byID    map[string]*Session
}

// New creates an STS/session service.
func New(ttl time.Duration, apiBase string) *Service {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if apiBase == "" {
		apiBase = "http://127.0.0.1:8080"
	}
	return &Service{
		ttl:     ttl,
		apiBase: apiBase,
		byToken: make(map[string]*Session),
		byID:    make(map[string]*Session),
	}
}

// IssueInput for a new session.
type IssueInput struct {
	UserID        string
	DriveID       string
	DeviceID      string
	MountPoint    string
	Mode          string
	Bucket        string
	Prefix        string
	Resolved      *provider.Resolved
	AgentID       string
	ReadPrefixes  []string
	WritePrefixes []string
}

// Issue creates a time-bounded mount session + manifest.
func (s *Service) Issue(in IssueInput) (*Session, error) {
	if in.Resolved == nil {
		return nil, fmt.Errorf("resolved provider required")
	}
	if in.MountPoint == "" {
		return nil, fmt.Errorf("mount_point required")
	}
	if in.Mode == "" {
		in.Mode = "mount"
	}
	resolved, source, note := applyOptionalSTS(in.Resolved, s.ttl, SourceEmbedded)
	spec := mountlib.NewSpec(resolved, in.Bucket, in.Prefix, in.MountPoint, in.Mode)
	exp := time.Now().UTC().Add(s.ttl)
	tok, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	man := manifest.Build(manifest.Input{
		DriveID:       in.DriveID,
		MountPoint:    in.MountPoint,
		Mode:          in.Mode,
		APIBase:       s.apiBase,
		TTL:           s.ttl,
		AgentID:       in.AgentID,
		ReadPrefixes:  in.ReadPrefixes,
		WritePrefixes: in.WritePrefixes,
	})
	sess := &Session{
		ID:         uuid.NewString(),
		UserID:     in.UserID,
		DriveID:    in.DriveID,
		DeviceID:   in.DeviceID,
		MountPoint: in.MountPoint,
		Mode:       in.Mode,
		ExpiresAt:  exp,
		Spec:       spec,
		Manifest:   man,
		Token:      tok,
		Source:     source,
		Note:       note,
	}
	s.mu.Lock()
	s.byToken[tok] = sess
	s.byID[sess.ID] = sess
	s.mu.Unlock()
	return sess, nil
}

// GetByToken returns a non-expired session.
func (s *Service) GetByToken(token string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.byToken[token]
	if !ok {
		return nil, fmt.Errorf("invalid session token")
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return nil, fmt.Errorf("session expired")
	}
	return sess, nil
}

// Revoke removes a session.
func (s *Service) Revoke(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.byID[id]; ok {
		delete(s.byToken, sess.Token)
		delete(s.byID, id)
	}
}

// Refresh extends a session by re-resolving provider credentials (same mount point).
// Rotates token; old token becomes invalid.
// Re-runs multi-vendor optional STS (best-effort) with fallback source "refresh".
func (s *Service) Refresh(oldToken string, resolved *provider.Resolved, bucket, prefix string) (*Session, error) {
	if resolved == nil {
		return nil, fmt.Errorf("resolved provider required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.byToken[oldToken]
	if !ok {
		return nil, fmt.Errorf("invalid session token")
	}
	// allow refresh shortly after expiry (grace 2m) so runtimes can recover
	if time.Now().UTC().After(old.ExpiresAt.Add(2 * time.Minute)) {
		return nil, fmt.Errorf("session expired beyond grace")
	}
	resolved, source, note := applyOptionalSTS(resolved, s.ttl, SourceRefresh)
	spec := mountlib.NewSpec(resolved, bucket, prefix, old.MountPoint, old.Mode)
	tok, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	man := manifest.Build(manifest.Input{
		DriveID:    old.DriveID,
		MountPoint: old.MountPoint,
		Mode:       old.Mode,
		APIBase:    s.apiBase,
		TTL:        s.ttl,
	})
	// drop old token mapping
	delete(s.byToken, old.Token)
	old.Token = tok
	old.ExpiresAt = time.Now().UTC().Add(s.ttl)
	old.Spec = spec
	old.Manifest = man
	old.Source = source
	old.Note = note
	s.byToken[tok] = old
	return old, nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
