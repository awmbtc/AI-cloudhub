package drive

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/mountlib"
	"github.com/awmbtc/AI-cloudhub/internal/policy"
	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/awmbtc/AI-cloudhub/internal/sts"
	"github.com/google/uuid"
)

// Map is a logical drive (provider bucket → path semantics).
type Map struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Name       string    `json:"name"`
	ProviderID string    `json:"provider_id"`
	Bucket     string    `json:"bucket"`
	Prefix     string    `json:"prefix,omitempty"`
	MountPoint string    `json:"mount_point"`      // default hint
	Region     string    `json:"region,omitempty"` // optional P2 scheduling/locality hint
	CreatedAt  time.Time `json:"created_at"`
}

// Service manages drives and bindings.
type Service struct {
	providers *provider.Service
	sts       *sts.Service
	store     store.Store
	quota     policy.Quota

	// barriers remain process-local (not P1 durable).
	mu       sync.RWMutex
	barriers map[string]*Barrier
}

// NewService creates a drive-map service backed by store.
func NewService(providers *provider.Service, st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{
		providers: providers,
		store:     st,
		quota:     policy.DefaultQuota,
		barriers:  make(map[string]*Barrier),
	}
}

// SetQuota overrides the per-user binding quota (optional; default is policy.DefaultQuota).
func (s *Service) SetQuota(q policy.Quota) {
	s.quota = q
}

// CreateInput is the body for defining a drive.
type CreateInput struct {
	Name       string `json:"name"`
	ProviderID string `json:"provider_id"`
	Bucket     string `json:"bucket"`
	Prefix     string `json:"prefix"`
	MountPoint string `json:"mount_point"`
	Region     string `json:"region"` // optional P2 scheduling/locality hint
}

// Create registers a logical drive.
func (s *Service) Create(userID string, in CreateInput) (*Map, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Bucket = strings.TrimSpace(in.Bucket)
	in.ProviderID = strings.TrimSpace(in.ProviderID)
	if in.Name == "" || in.Bucket == "" || in.ProviderID == "" {
		return nil, fmt.Errorf("name, provider_id, bucket required")
	}
	if _, err := s.providers.Get(userID, in.ProviderID); err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	// Per-user drive quota (default 20).
	existing, err := s.store.ListDrives(userID)
	if err != nil {
		return nil, err
	}
	if err := s.quota.CheckDrives(len(existing)); err != nil {
		return nil, err
	}
	mp := strings.TrimSpace(in.MountPoint)
	if mp == "" {
		mp = defaultMountPoint()
	}
	if err := validateMountPoint(mp); err != nil {
		return nil, err
	}
	prefix := strings.Trim(strings.TrimSpace(in.Prefix), "/")
	region := strings.TrimSpace(in.Region)

	m := &Map{
		ID:         uuid.NewString(),
		UserID:     userID,
		Name:       in.Name,
		ProviderID: in.ProviderID,
		Bucket:     in.Bucket,
		Prefix:     prefix,
		MountPoint: mp,
		Region:     region,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.store.CreateDrive(mapToStore(m)); err != nil {
		return nil, err
	}
	return m, nil
}

// Get returns a drive map owned by user.
func (s *Service) Get(userID, id string) (*Map, error) {
	d, err := s.store.GetDrive(userID, id)
	if err != nil {
		return nil, fmt.Errorf("drive not found")
	}
	return mapFromStore(d), nil
}

// List returns drives for user.
func (s *Service) List(userID string) []*Map {
	list, err := s.store.ListDrives(userID)
	if err != nil {
		return nil
	}
	out := make([]*Map, 0, len(list))
	for _, d := range list {
		out = append(out, mapFromStore(d))
	}
	return out
}

// Delete removes a drive map.
func (s *Service) Delete(userID, id string) error {
	if err := s.store.DeleteDrive(userID, id); err != nil {
		return fmt.Errorf("drive not found")
	}
	return nil
}

// MountBundle legacy-compatible response for GET /drives/{id}/mount.
type MountBundle struct {
	Drive      *Map                   `json:"drive"`
	Provider   map[string]interface{} `json:"provider"`
	RcloneConf string                 `json:"rclone_conf"`
	RemoteName string                 `json:"remote_name"`
	RemotePath string                 `json:"remote_path"`
	Spec       mountlib.Spec          `json:"spec"`
	Commands   MountCommands          `json:"commands"`
	Note       string                 `json:"note"`
}

// MountCommands are copy-paste shell lines (fallback if hubd not used).
type MountCommands struct {
	WriteConf string `json:"write_conf"`
	Mount     string `json:"mount"`
	Unmount   string `json:"unmount,omitempty"`
}

// MountConfig builds rclone config (prefer IssueSession for Runtime).
func (s *Service) MountConfig(userID, driveID string) (*MountBundle, error) {
	m, err := s.Get(userID, driveID)
	if err != nil {
		return nil, err
	}
	resolved, rec, err := s.providers.ResolveRecord(userID, m.ProviderID)
	if err != nil {
		return nil, err
	}
	spec := mountlib.NewSpec(resolved, m.Bucket, m.Prefix, m.MountPoint, "mount")
	cmds := MountCommands{
		WriteConf: "# write rclone_conf to a local file; prefer hubd/runner + STS session",
		Mount:     fmt.Sprintf("rclone %s", strings.Join(mountlib.MountArgs(spec, "/tmp/aihub-rclone.conf"), " ")),
		Unmount:   mountlib.UnmountHint(m.MountPoint),
	}
	return &MountBundle{
		Drive:      m,
		Provider:   rec.Public(),
		RcloneConf: spec.RcloneConf,
		RemoteName: spec.RemoteName,
		RemotePath: spec.RemotePath,
		Spec:       spec,
		Commands:   cmds,
		Note:       "Prefer POST /v1/drives/{id}/session for STS. hubd/runner consume session automatically.",
	}, nil
}

func defaultMountPoint() string {
	if runtime.GOOS == "windows" {
		return "G:"
	}
	return "/workspace"
}

func validateMountPoint(mp string) error {
	if mp == "" {
		return fmt.Errorf("mount_point required")
	}
	if len(mp) == 2 && mp[1] == ':' {
		c := mp[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return nil
		}
	}
	if strings.HasPrefix(mp, "/") || strings.HasPrefix(mp, "~/") {
		return nil
	}
	if len(mp) >= 2 && mp[1] == ':' {
		return nil
	}
	return fmt.Errorf("mount_point should look like G: or /workspace")
}

func mapToStore(m *Map) *store.Drive {
	return &store.Drive{
		ID:         m.ID,
		UserID:     m.UserID,
		Name:       m.Name,
		ProviderID: m.ProviderID,
		Bucket:     m.Bucket,
		Prefix:     m.Prefix,
		MountPoint: m.MountPoint,
		Region:     m.Region,
		CreatedAt:  m.CreatedAt,
	}
}

func mapFromStore(d *store.Drive) *Map {
	return &Map{
		ID:         d.ID,
		UserID:     d.UserID,
		Name:       d.Name,
		ProviderID: d.ProviderID,
		Bucket:     d.Bucket,
		Prefix:     d.Prefix,
		MountPoint: d.MountPoint,
		Region:     d.Region,
		CreatedAt:  d.CreatedAt,
	}
}
