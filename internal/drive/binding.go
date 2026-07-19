package drive

import (
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/manifest"
	"github.com/awmbtc/AI-cloudhub/internal/mountlib"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/awmbtc/AI-cloudhub/internal/sts"
	"github.com/google/uuid"
)

// DesiredState is control-plane intent for a binding.
type DesiredState string

const (
	DesiredMounted   DesiredState = "mounted"
	DesiredUnmounted DesiredState = "unmounted"
)

// ActualState is runtime-reported state.
type ActualState string

const (
	ActualUnknown   ActualState = "unknown"
	ActualMounted   ActualState = "mounted"
	ActualUnmounted ActualState = "unmounted"
	ActualDegraded  ActualState = "degraded"
	ActualError     ActualState = "error"
)

// Binding is a Drive mounted (or desired) on a specific device/runtime.
type Binding struct {
	ID         string       `json:"id"`
	UserID     string       `json:"user_id"`
	DriveID    string       `json:"drive_id"`
	DeviceID   string       `json:"device_id"`
	MountPoint string       `json:"mount_point"`
	Mode       string       `json:"mode"`
	Desired    DesiredState `json:"desired"`
	Actual     ActualState  `json:"actual"`
	LastError  string       `json:"last_error,omitempty"`
	UpdatedAt  time.Time    `json:"updated_at"`
	CreatedAt  time.Time    `json:"created_at"`
}

// BindingCreate input.
type BindingCreate struct {
	DriveID    string `json:"drive_id"`
	DeviceID   string `json:"device_id"`
	MountPoint string `json:"mount_point"`
	Mode       string `json:"mode"`
	Desired    string `json:"desired"` // mounted|unmounted
}

// SetSTS attaches session issuer (optional until wired).
func (s *Service) SetSTS(st *sts.Service) {
	s.sts = st
}

// CreateBinding registers desired mount state for a device.
func (s *Service) CreateBinding(userID string, in BindingCreate) (*Binding, error) {
	if _, err := s.Get(userID, in.DriveID); err != nil {
		return nil, err
	}
	// Per-user concurrent binding quota (default 10).
	existing, err := s.store.ListBindings(userID, "")
	if err != nil {
		return nil, err
	}
	if err := s.quota.CheckBindings(len(existing)); err != nil {
		return nil, err
	}
	in.DeviceID = strings.TrimSpace(in.DeviceID)
	if in.DeviceID == "" {
		in.DeviceID = "default"
	}
	mp := strings.TrimSpace(in.MountPoint)
	if mp == "" {
		mp = defaultMountPoint()
	}
	if err := validateMountPoint(mp); err != nil {
		return nil, err
	}
	mode := in.Mode
	if mode == "" {
		mode = "mount"
	}
	des := DesiredMounted
	if in.Desired == string(DesiredUnmounted) {
		des = DesiredUnmounted
	}
	now := time.Now().UTC()
	b := &Binding{
		ID:         uuid.NewString(),
		UserID:     userID,
		DriveID:    in.DriveID,
		DeviceID:   in.DeviceID,
		MountPoint: mp,
		Mode:       mode,
		Desired:    des,
		Actual:     ActualUnknown,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.store.CreateBinding(bindingToStore(b)); err != nil {
		return nil, err
	}
	return b, nil
}

// ListBindings for user, optional device filter.
func (s *Service) ListBindings(userID, deviceID string) []*Binding {
	list, err := s.store.ListBindings(userID, deviceID)
	if err != nil {
		return nil
	}
	out := make([]*Binding, 0, len(list))
	for _, b := range list {
		out = append(out, bindingFromStore(b))
	}
	return out
}

// GetBinding by id.
func (s *Service) GetBinding(userID, id string) (*Binding, error) {
	b, err := s.store.GetBinding(userID, id)
	if err != nil {
		return nil, fmt.Errorf("binding not found")
	}
	return bindingFromStore(b), nil
}

// SetDesired updates desired state.
func (s *Service) SetDesired(userID, id string, desired DesiredState) (*Binding, error) {
	b, err := s.GetBinding(userID, id)
	if err != nil {
		return nil, err
	}
	b.Desired = desired
	b.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateBinding(bindingToStore(b)); err != nil {
		return nil, err
	}
	return b, nil
}

// ReportActual is called by hubd/runner.
func (s *Service) ReportActual(userID, id string, actual ActualState, lastErr string) (*Binding, error) {
	b, err := s.GetBinding(userID, id)
	if err != nil {
		return nil, err
	}
	b.Actual = actual
	b.LastError = lastErr
	b.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateBinding(bindingToStore(b)); err != nil {
		return nil, err
	}
	return b, nil
}

// SessionBundle is what Runtime needs to mount (STS session + drive meta).
type SessionBundle struct {
	Binding  *Binding          `json:"binding"`
	Drive    *Map              `json:"drive"`
	Session  *sts.Session      `json:"session"`
	Manifest manifest.Document `json:"manifest"`
	Spec     mountlib.Spec     `json:"spec"`
	Note     string            `json:"note"`
}

// SessionOpts optional Agent context for Manifest 2.0.
type SessionOpts struct {
	AgentID       string
	ReadPrefixes  []string
	WritePrefixes []string
}

// IssueSession creates a short-lived mount session for a binding or drive.
func (s *Service) IssueSession(userID, driveID, deviceID, mountPoint, mode string) (*SessionBundle, error) {
	return s.IssueSessionOpts(userID, driveID, deviceID, mountPoint, mode, SessionOpts{})
}

// IssueSessionOpts is IssueSession with agent permissions for Manifest 2.0.
func (s *Service) IssueSessionOpts(userID, driveID, deviceID, mountPoint, mode string, opts SessionOpts) (*SessionBundle, error) {
	if s.sts == nil {
		return nil, fmt.Errorf("sts not configured")
	}
	m, err := s.Get(userID, driveID)
	if err != nil {
		return nil, err
	}
	if mountPoint == "" {
		mountPoint = m.MountPoint
	}
	if mode == "" {
		mode = "mount"
	}
	resolved, _, err := s.providers.ResolveRecord(userID, m.ProviderID)
	if err != nil {
		return nil, err
	}
	sess, err := s.sts.Issue(sts.IssueInput{
		UserID:        userID,
		DriveID:       driveID,
		DeviceID:      deviceID,
		MountPoint:    mountPoint,
		Mode:          mode,
		Bucket:        m.Bucket,
		Prefix:        m.Prefix,
		Resolved:      resolved,
		AgentID:       opts.AgentID,
		ReadPrefixes:  opts.ReadPrefixes,
		WritePrefixes: opts.WritePrefixes,
	})
	if err != nil {
		return nil, err
	}
	note := "Refresh session before expires_at. Runtime must not persist secrets beyond session TTL. BYOC: run on user compute only."
	// Optional P2 region scheduling hint for runtimes / agents.
	if m.Region != "" {
		if sess.Manifest.Env == nil {
			sess.Manifest.Env = make(map[string]string)
		}
		sess.Manifest.Env["AI_CLOUDHUB_STORAGE_REGION"] = m.Region
		note += " AI_CLOUDHUB_STORAGE_REGION=" + m.Region
	}
	if sess.Note != "" {
		note += " " + sess.Note
	}
	return &SessionBundle{
		Drive:    m,
		Session:  sess,
		Manifest: sess.Manifest,
		Spec:     sess.Spec,
		Note:     note,
	}, nil
}

// IssueSessionForBinding issues STS for an existing binding.
func (s *Service) IssueSessionForBinding(userID, bindingID string) (*SessionBundle, error) {
	b, err := s.GetBinding(userID, bindingID)
	if err != nil {
		return nil, err
	}
	bundle, err := s.IssueSession(userID, b.DriveID, b.DeviceID, b.MountPoint, b.Mode)
	if err != nil {
		return nil, err
	}
	bundle.Binding = b
	return bundle, nil
}

// RefreshSessionInput for extending an existing mount session.
type RefreshSessionInput struct {
	SessionToken string `json:"session_token"`
	DriveID      string `json:"drive_id"`
}

// RefreshSession rotates STS for a drive without changing mount point intent.
func (s *Service) RefreshSession(userID string, in RefreshSessionInput) (*SessionBundle, error) {
	if s.sts == nil {
		return nil, fmt.Errorf("sts not configured")
	}
	if in.SessionToken == "" || in.DriveID == "" {
		return nil, fmt.Errorf("session_token and drive_id required")
	}
	m, err := s.Get(userID, in.DriveID)
	if err != nil {
		return nil, err
	}
	resolved, _, err := s.providers.ResolveRecord(userID, m.ProviderID)
	if err != nil {
		return nil, err
	}
	sess, err := s.sts.Refresh(in.SessionToken, resolved, m.Bucket, m.Prefix)
	if err != nil {
		return nil, err
	}
	if sess.UserID != userID || sess.DriveID != in.DriveID {
		return nil, fmt.Errorf("session mismatch")
	}
	if m.Region != "" {
		if sess.Manifest.Env == nil {
			sess.Manifest.Env = map[string]string{}
		}
		sess.Manifest.Env["AI_CLOUDHUB_STORAGE_REGION"] = m.Region
	}
	note := "session refreshed; update local rclone conf if credentials rotated"
	if sess.Note != "" {
		note += " " + sess.Note
	}
	return &SessionBundle{
		Drive:    m,
		Session:  sess,
		Manifest: sess.Manifest,
		Spec:     sess.Spec,
		Note:     note,
	}, nil
}

func bindingToStore(b *Binding) *store.Binding {
	return &store.Binding{
		ID:         b.ID,
		UserID:     b.UserID,
		DriveID:    b.DriveID,
		DeviceID:   b.DeviceID,
		MountPoint: b.MountPoint,
		Mode:       b.Mode,
		Desired:    string(b.Desired),
		Actual:     string(b.Actual),
		LastError:  b.LastError,
		UpdatedAt:  b.UpdatedAt,
		CreatedAt:  b.CreatedAt,
	}
}

func bindingFromStore(b *store.Binding) *Binding {
	return &Binding{
		ID:         b.ID,
		UserID:     b.UserID,
		DriveID:    b.DriveID,
		DeviceID:   b.DeviceID,
		MountPoint: b.MountPoint,
		Mode:       b.Mode,
		Desired:    DesiredState(b.Desired),
		Actual:     ActualState(b.Actual),
		LastError:  b.LastError,
		UpdatedAt:  b.UpdatedAt,
		CreatedAt:  b.CreatedAt,
	}
}
