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
	AgentID   string    `json:"agent_id,omitempty"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditFilter selects audit rows. Empty fields are ignored.
type AuditFilter struct {
	UserID  string
	AgentID string
	Action  string
	Limit   int
}

// RefreshToken is a long-lived opaque credential (hash stored only).
type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash string // sha256 hex of raw token
	ExpiresAt time.Time
	CreatedAt time.Time
	Revoked   bool
}

// Agent is a user-owned agent principal (not a human login).
type Agent struct {
	ID              string
	OwnerUserID     string
	Name            string
	Description     string
	Status          string // active | disabled
	DefaultScopes   []string
	// AllowedDriveIDs restricts which drives this agent may access.
	// Empty = all drives owned by the user (backward-compatible default).
	AllowedDriveIDs []string
	// ReadPrefixes / WritePrefixes are optional path prefixes under mount (Manifest 2.0).
	// Empty = full workspace (AllowedPaths = mount root).
	ReadPrefixes  []string
	WritePrefixes []string
	CreatedAt     time.Time
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
	// ConnectorID optional Stage C connector (e.g. git) for runner materialization.
	ConnectorID string
	// AgentID is the agent that created the job (empty = human/API).
	AgentID string
	// ClaimedByAgentID is the agent that last claimed the job (empty = human runner).
	ClaimedByAgentID string
	// ExitCode set on complete when runner reports process exit (nil = not reported).
	ExitCode *int
	// DurationMs wall time of runner execution in milliseconds (0 = not reported).
	DurationMs int64
	// HeartbeatAt last claim or heartbeat while running (zero if not running / never claimed).
	HeartbeatAt time.Time
	// Stdout / Stderr capped process output from runner complete (empty if not reported).
	Stdout    string
	Stderr    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Snapshot is a metadata snapshot of a drive workspace (ROADMAP B6 — not full object versioning).
type Snapshot struct {
	ID           string
	UserID       string
	DriveID      string
	AgentID      string
	Label        string
	Note         string
	// PayloadJSON holds drive map + optional manifest snapshot (client/runtime rehydration).
	PayloadJSON  []byte
	CreatedAt    time.Time
}

// MemoryEntry is Stage C Memory Kernel v0 (metadata + small text; optional embedding).
// Layer: working | episodic | semantic
type MemoryEntry struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	DriveID   string    `json:"drive_id,omitempty"`
	Layer     string    `json:"layer"`
	Key       string    `json:"key,omitempty"`
	Content   string    `json:"content"`
	MetaJSON  []byte    `json:"meta,omitempty"`
	// EmbeddingJSON is optional float32 vector as JSON array for vector search v0.
	EmbeddingJSON []byte    `json:"embedding,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

// MemoryFilter selects memory rows.
type MemoryFilter struct {
	UserID  string
	AgentID string
	DriveID string
	Layer   string
	Key     string
	Limit   int
}

// MarketplaceItem is a publishable template (agent_template | skill | manifest).
// PublisherUserID empty = system catalog item.
type MarketplaceItem struct {
	ID              string    `json:"id"`
	PublisherUserID string    `json:"publisher_user_id,omitempty"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	Kind            string    `json:"kind"`
	Version         string    `json:"version,omitempty"`
	PayloadJSON     []byte    `json:"payload,omitempty"`
	Public          bool      `json:"public"`
	// PriceCents / Currency: payment-grade listing (0 = free). Checkout is webhook-stubbed.
	PriceCents int64  `json:"price_cents"`
	Currency   string `json:"currency,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// LineageEvent is Data Lineage v0 (append-only activity edge).
type LineageEvent struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ActorID   string    `json:"actor_id,omitempty"` // user or agent
	Action    string    `json:"action"`
	Entity    string    `json:"entity"` // type:id e.g. drive:uuid
	Parent    string    `json:"parent,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// GraphEdge is Identity Graph v0 (subject --rel--> object).
type GraphEdge struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Subject   string    `json:"subject"` // e.g. agent:uuid
	Relation  string    `json:"relation"`
	Object    string    `json:"object"`
	MetaJSON  []byte    `json:"meta,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Purchase is a marketplace purchase record (payment-grade skeleton).
type Purchase struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	ItemID          string    `json:"item_id"`
	AmountCents     int64     `json:"amount_cents"`
	Currency        string    `json:"currency"`
	Status          string    `json:"status"` // pending|paid|failed|refunded
	Provider        string    `json:"provider,omitempty"` // stripe_stub|manual
	ProviderRef     string    `json:"provider_ref,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// ConnectorBinding is a Git/DB/SaaS connector registration (not full sync engine).
type ConnectorBinding struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Type   string `json:"type"` // git|postgres|mysql|notion|slack|…
	Name   string `json:"name"`
	// ConfigJSON is non-secret JSON object (RawMessage so API encodes object, not base64).
	ConfigJSON json.RawMessage `json:"config,omitempty"`
	Status     string          `json:"status"` // registered|disabled
	CreatedAt  time.Time       `json:"created_at"`
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

	// Refresh tokens (opaque; store only SHA-256 hash of secret).
	CreateRefreshToken(t *RefreshToken) error
	// GetRefreshTokenByHash returns non-revoked, non-expired token by hash.
	GetRefreshTokenByHash(tokenHash string) (*RefreshToken, error)
	RevokeRefreshToken(id string) error
	RevokeRefreshTokensForUser(userID string) error

	// Agents (Agent Identity)
	CreateAgent(a *Agent) error
	GetAgent(ownerUserID, id string) (*Agent, error)
	// GetAgentByID looks up agent by id only (for policy when owner already known via token).
	GetAgentByID(id string) (*Agent, error)
	ListAgents(ownerUserID string) ([]*Agent, error)
	UpdateAgent(a *Agent) error
	DeleteAgent(ownerUserID, id string) error

	// Providers
	CreateProvider(p *Provider) error
	GetProvider(userID, id string) (*Provider, error)
	ListProviders(userID string) ([]*Provider, error)
	DeleteProvider(userID, id string) error

	// Drives
	CreateDrive(d *Drive) error
	GetDrive(userID, id string) (*Drive, error)
	ListDrives(userID string) ([]*Drive, error)
	// UpdateDrive updates mutable drive fields (name, prefix, mount_point, region).
	UpdateDrive(d *Drive) error
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
	// claimedByAgentID is stored on the job (empty for human runners).
	// Returns the updated job, or an error if not found / not claimable.
	ClaimPendingJob(userID, id, claimedByAgentID string) (*Job, error)
	UpdateJob(j *Job) error

	// Snapshots (metadata only)
	CreateSnapshot(s *Snapshot) error
	GetSnapshot(userID, driveID, id string) (*Snapshot, error)
	ListSnapshots(userID, driveID string, limit int) ([]*Snapshot, error)
	DeleteSnapshot(userID, driveID, id string) error

	// Memory Kernel v0 (Stage C)
	CreateMemory(e *MemoryEntry) error
	GetMemory(userID, id string) (*MemoryEntry, error)
	ListMemory(f MemoryFilter) ([]*MemoryEntry, error)
	DeleteMemory(userID, id string) error

	// Marketplace v0 (Stage C) — user-published items (system catalog is code-defined)
	CreateMarketplaceItem(m *MarketplaceItem) error
	GetMarketplaceItem(id string) (*MarketplaceItem, error)
	ListMarketplaceItems(publicOnly bool, publisherUserID string) ([]*MarketplaceItem, error)
	DeleteMarketplaceItem(publisherUserID, id string) error

	// Lineage v0
	AppendLineage(e *LineageEvent) error
	ListLineage(userID, entity string, limit int) ([]*LineageEvent, error)

	// Identity Graph v0
	UpsertGraphEdge(e *GraphEdge) error
	ListGraphEdges(userID, subject, object string, limit int) ([]*GraphEdge, error)

	// Purchases (marketplace payment skeleton)
	CreatePurchase(p *Purchase) error
	GetPurchase(userID, id string) (*Purchase, error)
	ListPurchases(userID string, limit int) ([]*Purchase, error)
	UpdatePurchase(p *Purchase) error

	// Connectors (Git/DB/SaaS registry)
	CreateConnector(c *ConnectorBinding) error
	GetConnector(userID, id string) (*ConnectorBinding, error)
	ListConnectors(userID string) ([]*ConnectorBinding, error)
	DeleteConnector(userID, id string) error

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
