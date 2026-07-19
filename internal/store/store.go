// Package store provides durable (SQLite) and in-memory persistence for
// control-plane entities: users, providers, drives, bindings, and devices.
package store

import (
	"encoding/json"
	"time"
)

// User is a local account record. Password holds a bcrypt hash (or legacy
// plaintext until the next successful login upgrades it).
type User struct {
	ID       string
	Username string
	Password string
	// Role is "user" (default) or "admin".
	Role string
	// TokenVersion is embedded in issued tokens; bumping invalidates all sessions.
	TokenVersion int
}

// Provider is a user-bound storage backend. CredsJSON is a JSON blob of
// credentials; when AI_CLOUDHUB_MASTER_KEY is set, secret_key is empty and
// secret_enc holds NaCl secretbox ciphertext (see internal/crypto/secretbox).
type Provider struct {
	ID             string
	UserID         string
	Name           string
	Type           string
	CredsJSON      []byte
	EndpointPublic string
	Region         string
	AccountID      string
}

// Drive is a logical drive map (provider bucket → path semantics).
type Drive struct {
	ID         string
	UserID     string
	Name       string
	ProviderID string
	Bucket     string
	Prefix     string
	MountPoint string
	// Region is an optional scheduling/locality hint (P2), not the provider S3 region.
	Region    string
	CreatedAt time.Time
}

// Binding is a drive mounted (or desired) on a device/runtime.
type Binding struct {
	ID         string
	UserID     string
	DriveID    string
	DeviceID   string
	MountPoint string
	Mode       string
	Desired    string
	Actual     string
	LastError  string
	UpdatedAt  time.Time
	CreatedAt  time.Time
}

// Device is a hubd/runtime endpoint registered by a user (laptop, runner host).
type Device struct {
	ID       string
	UserID   string
	Name     string
	LastSeen time.Time
}

// AuditEvent is a control-plane audit record (no file contents).
type AuditEvent struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditFilter selects audit rows. Empty fields are ignored.
type AuditFilter struct {
	UserID string
	Action string
	Limit  int
}

// Job is a BYOC work item (compute on user runners only).
type Job struct {
	ID         string
	UserID     string
	DriveID    string
	BindingID  string
	Mode       string
	CommandJSON []byte // JSON array of strings
	Status     string
	RegionHint string
	Note       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Store is the persistence interface for control-plane CRUD.
type Store interface {
	// Users
	CreateUser(u *User) error
	GetUserByUsername(username string) (*User, error)
	GetUserByID(id string) (*User, error)
	// UpdateUserPassword sets the password field (bcrypt hash) for userID.
	UpdateUserPassword(userID, hash string) error
	// UpdateUserRole sets role (admin|user).
	UpdateUserRole(userID, role string) error
	// CountUsers returns total users (for bootstrap first-admin).
	CountUsers() (int, error)
	// ListUsers returns all users (admin; no passwords in practice callers strip).
	ListUsers() ([]*User, error)
	// Ping checks store availability.
	Ping() error

	// Audit
	AppendAudit(e *AuditEvent) error
	// ListAudit returns recent events (newest first). Filters are optional
	// (empty UserID / Action = no filter). limit is clamped to 1..500.
	ListAudit(f AuditFilter) ([]*AuditEvent, error)

	// Session revocation
	// BumpTokenVersion increments users.token_version (invalidates all tokens).
	BumpTokenVersion(userID string) (newVersion int, err error)
	// RevokeJTI marks a single token id as revoked until expiresAt.
	RevokeJTI(jti string, expiresAt time.Time) error
	// IsJTIRevoked reports whether jti is on the denylist (and not expired).
	IsJTIRevoked(jti string) (bool, error)

	// Providers
	CreateProvider(p *Provider) error
	GetProvider(userID, id string) (*Provider, error)
	ListProviders(userID string) ([]*Provider, error)
	DeleteProvider(userID, id string) error

	// Drives
	CreateDrive(d *Drive) error
	GetDrive(userID, id string) (*Drive, error)
	ListDrives(userID string) ([]*Drive, error)
	DeleteDrive(userID, id string) error

	// Bindings
	CreateBinding(b *Binding) error
	GetBinding(userID, id string) (*Binding, error)
	ListBindings(userID, deviceID string) ([]*Binding, error)
	UpdateBinding(b *Binding) error

	// Devices
	UpsertDevice(d *Device) error
	GetDevice(userID, id string) (*Device, error)
	ListDevices(userID string) ([]*Device, error)

	// Jobs (BYOC queue)
	CreateJob(j *Job) error
	GetJob(userID, id string) (*Job, error)
	ListJobs(userID string) ([]*Job, error)
	ListPendingJobs(userID string) ([]*Job, error)
	// ClaimPendingJob atomically sets status to running if still pending/dispatched.
	// Returns the updated job, or an error if not found / not claimable.
	ClaimPendingJob(userID, id string) (*Job, error)
	UpdateJob(j *Job) error

	Close() error
}

// MarshalJSON is a small helper for credential / blob columns.
func MarshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// UnmarshalJSON is a small helper for credential / blob columns.
func UnmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
