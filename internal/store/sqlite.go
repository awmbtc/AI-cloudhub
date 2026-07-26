package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite implements Store with a pure-Go SQLite driver (CGO-free).
type SQLite struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path and runs migrations.
// Parent directories are created as needed. path may be absolute or relative.
func Open(path string) (*SQLite, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	// modernc.org/sqlite DSN; busy_timeout helps concurrent hubd/api.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single writer is safer for default SQLite; enough for control-plane MVP.
	db.SetMaxOpenConns(1)

	s := &SQLite{db: db}
	if err := s.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Migrate applies the control-plane schema (idempotent).
func (s *SQLite) Migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'user',
  token_version INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS providers (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  creds_json TEXT NOT NULL,
  endpoint_public TEXT,
  region TEXT,
  account_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_providers_user ON providers(user_id);

CREATE TABLE IF NOT EXISTS drives (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  bucket TEXT NOT NULL,
  prefix TEXT,
  mount_point TEXT NOT NULL,
  region TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_drives_user ON drives(user_id);

CREATE TABLE IF NOT EXISTS bindings (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  drive_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  mount_point TEXT NOT NULL,
  mode TEXT NOT NULL,
  desired TEXT NOT NULL,
  actual TEXT NOT NULL,
  last_error TEXT,
  updated_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bindings_user ON bindings(user_id);
CREATE INDEX IF NOT EXISTS idx_bindings_device ON bindings(user_id, device_id);

CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  last_seen TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id);

CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  drive_id TEXT NOT NULL,
  binding_id TEXT,
  mode TEXT NOT NULL,
  command_json TEXT NOT NULL,
  status TEXT NOT NULL,
  region_hint TEXT,
  note TEXT,
  agent_id TEXT,
  claimed_by_agent_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_user ON jobs(user_id);
CREATE INDEX IF NOT EXISTS idx_jobs_user_status ON jobs(user_id, status);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  user_id TEXT,
  action TEXT NOT NULL,
  target TEXT,
  detail TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_events(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_events(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_events(action);

CREATE TABLE IF NOT EXISTS revoked_jtis (
  jti TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_revoked_jtis_exp ON revoked_jtis(expires_at);

CREATE TABLE IF NOT EXISTS refresh_tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  revoked INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_refresh_user ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_hash ON refresh_tokens(token_hash);

CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  owner_user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  default_scopes TEXT NOT NULL DEFAULT '[]',
  allowed_drive_ids TEXT NOT NULL DEFAULT '[]',
  read_prefixes TEXT NOT NULL DEFAULT '[]',
  write_prefixes TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agents_owner ON agents(owner_user_id);

CREATE TABLE IF NOT EXISTS snapshots (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  drive_id TEXT NOT NULL,
  agent_id TEXT,
  label TEXT,
  note TEXT,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_drive ON snapshots(user_id, drive_id, created_at);

CREATE TABLE IF NOT EXISTS memory_entries (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  agent_id TEXT,
  drive_id TEXT,
  layer TEXT NOT NULL,
  key TEXT,
  content TEXT NOT NULL,
  meta_json TEXT,
  embedding_json TEXT,
  created_at TEXT NOT NULL,
  expires_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_memory_user ON memory_entries(user_id, created_at);

CREATE TABLE IF NOT EXISTS marketplace_items (
  id TEXT PRIMARY KEY,
  publisher_user_id TEXT,
  name TEXT NOT NULL,
  description TEXT,
  kind TEXT NOT NULL,
  version TEXT,
  payload_json TEXT NOT NULL,
  public INTEGER NOT NULL DEFAULT 1,
  price_cents INTEGER NOT NULL DEFAULT 0,
  currency TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_market_pub ON marketplace_items(publisher_user_id);

CREATE TABLE IF NOT EXISTS lineage_events (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  actor_id TEXT,
  action TEXT NOT NULL,
  entity TEXT NOT NULL,
  parent TEXT,
  detail TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lineage_user ON lineage_events(user_id, created_at);

CREATE TABLE IF NOT EXISTS graph_edges (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  subject TEXT NOT NULL,
  relation TEXT NOT NULL,
  object TEXT NOT NULL,
  meta_json TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_graph_user ON graph_edges(user_id);

CREATE TABLE IF NOT EXISTS purchases (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  amount_cents INTEGER NOT NULL,
  currency TEXT,
  status TEXT NOT NULL,
  provider TEXT,
  provider_ref TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_purchases_user ON purchases(user_id, created_at);

CREATE TABLE IF NOT EXISTS connectors (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  config_json TEXT,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_connectors_user ON connectors(user_id);

CREATE TABLE IF NOT EXISTS job_webhook_outbox (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  event TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_webhook_outbox_due ON job_webhook_outbox(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_webhook_outbox_job ON job_webhook_outbox(job_id);
CREATE INDEX IF NOT EXISTS idx_webhook_outbox_user ON job_webhook_outbox(user_id);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Soft migrations for existing DBs (ignore "duplicate column" errors).
	for _, stmt := range []string{
		`ALTER TABLE drives ADD COLUMN region TEXT`,
		`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user'`,
		`ALTER TABLE users ADD COLUMN token_version INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN allowed_drive_ids TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agents ADD COLUMN read_prefixes TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agents ADD COLUMN write_prefixes TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE audit_events ADD COLUMN agent_id TEXT`,
		`ALTER TABLE jobs ADD COLUMN agent_id TEXT`,
		`ALTER TABLE jobs ADD COLUMN claimed_by_agent_id TEXT`,
		`ALTER TABLE memory_entries ADD COLUMN embedding_json TEXT`,
		`ALTER TABLE marketplace_items ADD COLUMN price_cents INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE marketplace_items ADD COLUMN currency TEXT`,
		`ALTER TABLE jobs ADD COLUMN connector_id TEXT`,
		`ALTER TABLE jobs ADD COLUMN exit_code INTEGER`,
		`ALTER TABLE jobs ADD COLUMN duration_ms INTEGER`,
		`ALTER TABLE jobs ADD COLUMN heartbeat_at TEXT`,
		`ALTER TABLE jobs ADD COLUMN stdout TEXT`,
		`ALTER TABLE jobs ADD COLUMN stderr TEXT`,
		`ALTER TABLE jobs ADD COLUMN claimed_at TEXT`,
		`ALTER TABLE jobs ADD COLUMN timeout_sec INTEGER`,
		`ALTER TABLE jobs ADD COLUMN stdout_truncated INTEGER`,
		`ALTER TABLE jobs ADD COLUMN stderr_truncated INTEGER`,
		`ALTER TABLE jobs ADD COLUMN attempt_count INTEGER`,
		`ALTER TABLE jobs ADD COLUMN max_attempts INTEGER`,
		`ALTER TABLE jobs ADD COLUMN priority INTEGER`,
		`ALTER TABLE jobs ADD COLUMN claimed_by_runner_id TEXT`,
		`ALTER TABLE jobs ADD COLUMN labels_json TEXT`,
		`ALTER TABLE jobs ADD COLUMN idempotency_key TEXT`,
	} {

		if _, err := s.db.Exec(stmt); err != nil {
			// Column already exists on upgraded installs — safe to ignore.
			_ = err
		}
	}
	// Best-effort unique idempotency per user (empty keys excluded).
	_, _ = s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_user_idempotency
		ON jobs(user_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`)
	return nil
}

// Close closes the database.
func (s *SQLite) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLite) CreateUser(u *User) error {
	role := u.Role
	if role == "" {
		role = "user"
	}
	_, err := s.db.Exec(
		`INSERT INTO users (id, username, password, role, token_version) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Password, role, u.TokenVersion,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (s *SQLite) GetUserByUsername(username string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password, COALESCE(role,'user'), COALESCE(token_version,0) FROM users WHERE username = ?`,
		username,
	)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.TokenVersion); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	return &u, nil
}

func (s *SQLite) GetUserByID(id string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password, COALESCE(role,'user'), COALESCE(token_version,0) FROM users WHERE id = ?`, id,
	)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.TokenVersion); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	return &u, nil
}

func (s *SQLite) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

func (s *SQLite) UpdateUserRole(userID, role string) error {
	res, err := s.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *SQLite) Ping() error {
	return s.db.Ping()
}

func (s *SQLite) ListUsers() ([]*User, error) {
	rows, err := s.db.Query(`SELECT id, username, COALESCE(role,'user') FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

func (s *SQLite) AppendAudit(e *AuditEvent) error {
	_, err := s.db.Exec(
		`INSERT INTO audit_events (id, user_id, agent_id, action, target, detail, created_at) VALUES (?,?,?,?,?,?,?)`,
		e.ID, e.UserID, e.AgentID, e.Action, e.Target, e.Detail, e.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) ListAudit(f AuditFilter) ([]*AuditEvent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, user_id, COALESCE(agent_id,''), action, target, detail, created_at FROM audit_events WHERE 1=1`
	var args []any
	if f.UserID != "" {
		q += ` AND user_id = ?`
		args = append(args, f.UserID)
	}
	if f.AgentID != "" {
		q += ` AND agent_id = ?`
		args = append(args, f.AgentID)
	}
	if f.Action != "" {
		q += ` AND action = ?`
		args = append(args, f.Action)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditEvent
	for rows.Next() {
		var e AuditEvent
		var created string
		if err := rows.Scan(&e.ID, &e.UserID, &e.AgentID, &e.Action, &e.Target, &e.Detail, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *SQLite) BumpTokenVersion(userID string) (int, error) {
	res, err := s.db.Exec(`UPDATE users SET token_version = COALESCE(token_version,0) + 1 WHERE id = ?`, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, fmt.Errorf("user not found")
	}
	var ver int
	if err := s.db.QueryRow(`SELECT COALESCE(token_version,0) FROM users WHERE id = ?`, userID).Scan(&ver); err != nil {
		return 0, err
	}
	return ver, nil
}

func (s *SQLite) RevokeJTI(jti string, expiresAt time.Time) error {
	if jti == "" {
		return fmt.Errorf("jti required")
	}
	// best-effort prune
	_, _ = s.db.Exec(`DELETE FROM revoked_jtis WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339Nano))
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO revoked_jtis (jti, expires_at) VALUES (?, ?)`,
		jti, expiresAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) IsJTIRevoked(jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	var expStr string
	err := s.db.QueryRow(`SELECT expires_at FROM revoked_jtis WHERE jti = ?`, jti).Scan(&expStr)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	exp := parseTime(expStr)
	if time.Now().After(exp) {
		_, _ = s.db.Exec(`DELETE FROM revoked_jtis WHERE jti = ?`, jti)
		return false, nil
	}
	return true, nil
}

func (s *SQLite) CreateRefreshToken(t *RefreshToken) error {
	rev := 0
	if t.Revoked {
		rev = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, revoked) VALUES (?,?,?,?,?,?)`,
		t.ID, t.UserID, t.TokenHash,
		t.ExpiresAt.UTC().Format(time.RFC3339Nano),
		t.CreatedAt.UTC().Format(time.RFC3339Nano),
		rev,
	)
	return err
}

func (s *SQLite) GetRefreshTokenByHash(tokenHash string) (*RefreshToken, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, token_hash, expires_at, created_at, revoked FROM refresh_tokens WHERE token_hash = ?`,
		tokenHash,
	)
	var t RefreshToken
	var exp, created string
	var rev int
	if err := row.Scan(&t.ID, &t.UserID, &t.TokenHash, &exp, &created, &rev); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("refresh token not found")
		}
		return nil, err
	}
	t.ExpiresAt = parseTime(exp)
	t.CreatedAt = parseTime(created)
	t.Revoked = rev != 0
	if t.Revoked || time.Now().After(t.ExpiresAt) {
		return nil, fmt.Errorf("refresh token invalid")
	}
	return &t, nil
}

func (s *SQLite) RevokeRefreshToken(id string) error {
	res, err := s.db.Exec(`UPDATE refresh_tokens SET revoked = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("refresh token not found")
	}
	return nil
}

func (s *SQLite) RevokeRefreshTokensForUser(userID string) error {
	_, err := s.db.Exec(`UPDATE refresh_tokens SET revoked = 1 WHERE user_id = ? AND revoked = 0`, userID)
	return err
}

func agentJSONField(v []string) string {
	b, err := MarshalJSON(v)
	if err != nil || b == nil {
		return "[]"
	}
	return string(b)
}

func (s *SQLite) CreateAgent(a *Agent) error {
	_, err := s.db.Exec(
		`INSERT INTO agents (id, owner_user_id, name, description, status, default_scopes, allowed_drive_ids, read_prefixes, write_prefixes, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.OwnerUserID, a.Name, a.Description, a.Status,
		agentJSONField(a.DefaultScopes), agentJSONField(a.AllowedDriveIDs),
		agentJSONField(a.ReadPrefixes), agentJSONField(a.WritePrefixes),
		a.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) GetAgent(ownerUserID, id string) (*Agent, error) {
	row := s.db.QueryRow(
		`SELECT id, owner_user_id, name, description, status, default_scopes,
		 COALESCE(allowed_drive_ids,'[]'), COALESCE(read_prefixes,'[]'), COALESCE(write_prefixes,'[]'), created_at
		 FROM agents WHERE id = ? AND owner_user_id = ?`,
		id, ownerUserID,
	)
	return scanAgent(row)
}

func (s *SQLite) GetAgentByID(id string) (*Agent, error) {
	row := s.db.QueryRow(
		`SELECT id, owner_user_id, name, description, status, default_scopes,
		 COALESCE(allowed_drive_ids,'[]'), COALESCE(read_prefixes,'[]'), COALESCE(write_prefixes,'[]'), created_at
		 FROM agents WHERE id = ?`,
		id,
	)
	return scanAgent(row)
}

func (s *SQLite) ListAgents(ownerUserID string) ([]*Agent, error) {
	rows, err := s.db.Query(
		`SELECT id, owner_user_id, name, description, status, default_scopes,
		 COALESCE(allowed_drive_ids,'[]'), COALESCE(read_prefixes,'[]'), COALESCE(write_prefixes,'[]'), created_at
		 FROM agents WHERE owner_user_id = ? ORDER BY created_at DESC`,
		ownerUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLite) UpdateAgent(a *Agent) error {
	res, err := s.db.Exec(
		`UPDATE agents SET name=?, description=?, status=?, default_scopes=?, allowed_drive_ids=?, read_prefixes=?, write_prefixes=?
		 WHERE id=? AND owner_user_id=?`,
		a.Name, a.Description, a.Status,
		agentJSONField(a.DefaultScopes), agentJSONField(a.AllowedDriveIDs),
		agentJSONField(a.ReadPrefixes), agentJSONField(a.WritePrefixes),
		a.ID, a.OwnerUserID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func (s *SQLite) DeleteAgent(ownerUserID, id string) error {
	res, err := s.db.Exec(`DELETE FROM agents WHERE id = ? AND owner_user_id = ?`, id, ownerUserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func scanAgent(row interface{ Scan(dest ...any) error }) (*Agent, error) {
	var a Agent
	var scopes, drives, rpref, wpref, created string
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.Name, &a.Description, &a.Status, &scopes, &drives, &rpref, &wpref, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found")
		}
		return nil, err
	}
	_ = UnmarshalJSON([]byte(scopes), &a.DefaultScopes)
	_ = UnmarshalJSON([]byte(drives), &a.AllowedDriveIDs)
	_ = UnmarshalJSON([]byte(rpref), &a.ReadPrefixes)
	_ = UnmarshalJSON([]byte(wpref), &a.WritePrefixes)
	a.CreatedAt = parseTime(created)
	return &a, nil
}

func (s *SQLite) UpdateUserPassword(userID, hash string) error {
	res, err := s.db.Exec(
		`UPDATE users SET password = ? WHERE id = ?`,
		hash, userID,
	)
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *SQLite) CreateProvider(p *Provider) error {
	creds := string(p.CredsJSON)
	if creds == "" {
		creds = "{}"
	}
	_, err := s.db.Exec(
		`INSERT INTO providers (id, user_id, name, type, creds_json, endpoint_public, region, account_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.UserID, p.Name, p.Type, creds, p.EndpointPublic, p.Region, p.AccountID,
	)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	return nil
}

func (s *SQLite) GetProvider(userID, id string) (*Provider, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, name, type, creds_json, endpoint_public, region, account_id
		 FROM providers WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	return scanProvider(row)
}

func (s *SQLite) ListProviders(userID string) ([]*Provider, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, type, creds_json, endpoint_public, region, account_id
		 FROM providers WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Provider
	for rows.Next() {
		p, err := scanProviderRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteProvider(userID, id string) error {
	res, err := s.db.Exec(`DELETE FROM providers WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("provider not found")
	}
	return nil
}

func (s *SQLite) CreateDrive(d *Drive) error {
	_, err := s.db.Exec(
		`INSERT INTO drives (id, user_id, name, provider_id, bucket, prefix, mount_point, region, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.UserID, d.Name, d.ProviderID, d.Bucket, d.Prefix, d.MountPoint, d.Region,
		d.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create drive: %w", err)
	}
	return nil
}

func (s *SQLite) GetDrive(userID, id string) (*Drive, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, name, provider_id, bucket, prefix, mount_point, region, created_at
		 FROM drives WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	return scanDrive(row)
}

func (s *SQLite) ListDrives(userID string) ([]*Drive, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, provider_id, bucket, prefix, mount_point, region, created_at
		 FROM drives WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Drive
	for rows.Next() {
		d, err := scanDriveRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *SQLite) UpdateDrive(d *Drive) error {
	res, err := s.db.Exec(
		`UPDATE drives SET name=?, prefix=?, mount_point=?, region=? WHERE id=? AND user_id=?`,
		d.Name, d.Prefix, d.MountPoint, d.Region, d.ID, d.UserID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("drive not found")
	}
	return nil
}

func (s *SQLite) DeleteDrive(userID, id string) error {
	res, err := s.db.Exec(`DELETE FROM drives WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("drive not found")
	}
	return nil
}

func (s *SQLite) CreateBinding(b *Binding) error {
	_, err := s.db.Exec(
		`INSERT INTO bindings
		 (id, user_id, drive_id, device_id, mount_point, mode, desired, actual, last_error, updated_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.UserID, b.DriveID, b.DeviceID, b.MountPoint, b.Mode,
		b.Desired, b.Actual, b.LastError,
		b.UpdatedAt.UTC().Format(time.RFC3339Nano),
		b.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create binding: %w", err)
	}
	return nil
}

func (s *SQLite) GetBinding(userID, id string) (*Binding, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, drive_id, device_id, mount_point, mode, desired, actual, last_error, updated_at, created_at
		 FROM bindings WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	return scanBinding(row)
}

func (s *SQLite) ListBindings(userID, deviceID string) ([]*Binding, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if deviceID == "" {
		rows, err = s.db.Query(
			`SELECT id, user_id, drive_id, device_id, mount_point, mode, desired, actual, last_error, updated_at, created_at
			 FROM bindings WHERE user_id = ?`,
			userID,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, user_id, drive_id, device_id, mount_point, mode, desired, actual, last_error, updated_at, created_at
			 FROM bindings WHERE user_id = ? AND device_id = ?`,
			userID, deviceID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Binding
	for rows.Next() {
		b, err := scanBindingRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *SQLite) UpdateBinding(b *Binding) error {
	res, err := s.db.Exec(
		`UPDATE bindings SET
		 drive_id = ?, device_id = ?, mount_point = ?, mode = ?,
		 desired = ?, actual = ?, last_error = ?, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		b.DriveID, b.DeviceID, b.MountPoint, b.Mode,
		b.Desired, b.Actual, b.LastError,
		b.UpdatedAt.UTC().Format(time.RFC3339Nano),
		b.ID, b.UserID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("binding not found")
	}
	return nil
}

func (s *SQLite) UpsertDevice(d *Device) error {
	// Reject id owned by another user.
	var existingUser string
	err := s.db.QueryRow(`SELECT user_id FROM devices WHERE id = ?`, d.ID).Scan(&existingUser)
	if err == nil && existingUser != d.UserID {
		return fmt.Errorf("device id conflict")
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO devices (id, user_id, name, last_seen) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   last_seen = excluded.last_seen
		 WHERE devices.user_id = excluded.user_id`,
		d.ID, d.UserID, d.Name, d.LastSeen.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert device: %w", err)
	}
	return nil
}

func (s *SQLite) GetDevice(userID, id string) (*Device, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, name, last_seen FROM devices WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	return scanDevice(row)
}

func (s *SQLite) ListDevices(userID string) ([]*Device, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, last_seen FROM devices WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- scanners ---

type scannable interface {
	Scan(dest ...any) error
}

func scanProvider(row scannable) (*Provider, error) {
	var p Provider
	var creds string
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Type, &creds, &p.EndpointPublic, &p.Region, &p.AccountID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("provider not found")
		}
		return nil, err
	}
	p.CredsJSON = []byte(creds)
	return &p, nil
}

func scanProviderRows(rows *sql.Rows) (*Provider, error) {
	return scanProvider(rows)
}

func scanDrive(row scannable) (*Drive, error) {
	var d Drive
	var created string
	var region sql.NullString
	if err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.ProviderID, &d.Bucket, &d.Prefix, &d.MountPoint, &region, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("drive not found")
		}
		return nil, err
	}
	if region.Valid {
		d.Region = region.String
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, created)
	}
	d.CreatedAt = t
	return &d, nil
}

func scanDriveRows(rows *sql.Rows) (*Drive, error) {
	return scanDrive(rows)
}

func scanBinding(row scannable) (*Binding, error) {
	var b Binding
	var updated, created string
	if err := row.Scan(
		&b.ID, &b.UserID, &b.DriveID, &b.DeviceID, &b.MountPoint, &b.Mode,
		&b.Desired, &b.Actual, &b.LastError, &updated, &created,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("binding not found")
		}
		return nil, err
	}
	b.UpdatedAt = parseTime(updated)
	b.CreatedAt = parseTime(created)
	return &b, nil
}

func scanBindingRows(rows *sql.Rows) (*Binding, error) {
	return scanBinding(rows)
}

func scanDevice(row scannable) (*Device, error) {
	var d Device
	var lastSeen string
	if err := row.Scan(&d.ID, &d.UserID, &d.Name, &lastSeen); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("device not found")
		}
		return nil, err
	}
	d.LastSeen = parseTime(lastSeen)
	return &d, nil
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

const jobSelectCols = `id, user_id, drive_id, binding_id, mode, command_json, status, region_hint, note,
		 COALESCE(agent_id,''), COALESCE(claimed_by_agent_id,''), COALESCE(connector_id,''),
		 COALESCE(claimed_by_runner_id,''), COALESCE(priority,0), COALESCE(labels_json,''),
		 COALESCE(idempotency_key,''),
		 exit_code, COALESCE(duration_ms,0), COALESCE(heartbeat_at,''), COALESCE(claimed_at,''),
		 COALESCE(timeout_sec,0), COALESCE(attempt_count,0), COALESCE(max_attempts,0),
		 COALESCE(stdout,''), COALESCE(stderr,''),
		 COALESCE(stdout_truncated,0), COALESCE(stderr_truncated,0),
		 created_at, updated_at`

func (s *SQLite) CreateJob(j *Job) error {
	hb := ""
	if !j.HeartbeatAt.IsZero() {
		hb = j.HeartbeatAt.UTC().Format(time.RFC3339Nano)
	}
	ca := ""
	if !j.ClaimedAt.IsZero() {
		ca = j.ClaimedAt.UTC().Format(time.RFC3339Nano)
	}
	labels := string(j.LabelsJSON)
	_, err := s.db.Exec(
		`INSERT INTO jobs (id, user_id, drive_id, binding_id, mode, command_json, status, region_hint, note, agent_id, claimed_by_agent_id, connector_id, claimed_by_runner_id, priority, labels_json, idempotency_key, exit_code, duration_ms, heartbeat_at, claimed_at, timeout_sec, attempt_count, max_attempts, stdout, stderr, stdout_truncated, stderr_truncated, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.UserID, j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
		j.AgentID, j.ClaimedByAgentID, j.ConnectorID, j.ClaimedByRunnerID, j.Priority, labels, j.IdempotencyKey,
		nullInt(j.ExitCode), j.DurationMs, hb, ca, j.TimeoutSec,
		j.AttemptCount, j.MaxAttempts, j.Stdout, j.Stderr, boolInt(j.StdoutTruncated), boolInt(j.StderrTruncated),
		j.CreatedAt.UTC().Format(time.RFC3339Nano), j.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

func (s *SQLite) GetJob(userID, id string) (*Job, error) {
	row := s.db.QueryRow(
		`SELECT `+jobSelectCols+` FROM jobs WHERE id = ? AND user_id = ?`, id, userID,
	)
	return scanJob(row)
}

func (s *SQLite) GetJobByID(id string) (*Job, error) {
	row := s.db.QueryRow(
		`SELECT `+jobSelectCols+` FROM jobs WHERE id = ?`, id,
	)
	return scanJob(row)
}

func (s *SQLite) ListJobsAdmin(f AdminJobFilter) ([]*Job, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	// 501 allows service-layer limit+1 with user max 500
	if limit > 501 {
		limit = 501
	}
	q := `SELECT ` + jobSelectCols + ` FROM jobs WHERE 1=1`
	var args []interface{}
	if f.UserID != "" {
		q += ` AND user_id = ?`
		args = append(args, f.UserID)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	if !f.CursorCreated.IsZero() && f.CursorID != "" {
		// keyset: (created_at, id) < cursor in DESC order
		ca := f.CursorCreated.UTC().Format(time.RFC3339Nano)
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, ca, ca, f.CursorID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *SQLite) GetJobByIdempotencyKey(userID, key string) (*Job, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("job not found")
	}
	row := s.db.QueryRow(
		`SELECT `+jobSelectCols+` FROM jobs WHERE user_id = ? AND idempotency_key = ?`, userID, key,
	)
	return scanJob(row)
}

func (s *SQLite) ListJobs(userID string) ([]*Job, error) {
	rows, err := s.db.Query(
		`SELECT `+jobSelectCols+` FROM jobs WHERE user_id = ? ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *SQLite) ListJobsPage(f JobListFilter) ([]*Job, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 501 {
		limit = 501
	}
	q := `SELECT ` + jobSelectCols + ` FROM jobs WHERE user_id = ?`
	args := []interface{}{strings.TrimSpace(f.UserID)}
	if aid := strings.TrimSpace(f.AgentID); aid != "" {
		q += ` AND agent_id = ?`
		args = append(args, aid)
	}
	if cid := strings.TrimSpace(f.ClaimedByAgentID); cid != "" {
		q += ` AND claimed_by_agent_id = ?`
		args = append(args, cid)
	}
	if st := strings.TrimSpace(f.Status); st != "" {
		q += ` AND status = ?`
		args = append(args, st)
	}
	for k, v := range f.Labels {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		// SQLite JSON1: treat NULL/empty labels_json as {}
		path := "$." + k
		if strings.ContainsAny(k, ".-") {
			path = "$." + `"` + strings.ReplaceAll(k, `"`, ``) + `"`
		}
		q += ` AND json_extract(CASE WHEN labels_json IS NULL OR labels_json = '' THEN '{}' ELSE labels_json END, ?) = ?`
		args = append(args, path, v)
	}
	if !f.CursorCreated.IsZero() && f.CursorID != "" {
		ca := f.CursorCreated.UTC().Format(time.RFC3339Nano)
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, ca, ca, f.CursorID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *SQLite) CountJobsByStatus(userID string) (*JobStatusCounts, error) {
	var (
		rows *sql.Rows
		err  error
	)
	userID = strings.TrimSpace(userID)
	if userID != "" {
		rows, err = s.db.Query(`SELECT status, COUNT(*) FROM jobs WHERE user_id = ? GROUP BY status`, userID)
	} else {
		rows, err = s.db.Query(`SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var c JobStatusCounts
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		// AddStatus increments by 1; fold n times via Total math
		switch status {
		case "pending":
			c.Pending += n
		case "dispatched":
			c.Dispatched += n
		case "running":
			c.Running += n
		case "succeeded":
			c.Succeeded += n
		case "failed":
			c.Failed += n
		case "cancelled":
			c.Cancelled += n
		}
		c.Total += n
	}
	return &c, rows.Err()
}

func (s *SQLite) ListPendingJobs(userID string) ([]*Job, error) {
	rows, err := s.db.Query(
		`SELECT `+jobSelectCols+` FROM jobs WHERE user_id = ? AND status IN ('pending','dispatched')
		 ORDER BY COALESCE(priority,0) DESC, created_at ASC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ClaimPendingJob atomically claims via UPDATE ... WHERE status still claimable RETURNING.
func (s *SQLite) ClaimPendingJob(userID, id, claimedByAgentID, claimedByRunnerID string) (*Job, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := s.db.QueryRow(
		`UPDATE jobs SET status = 'running', claimed_by_agent_id = ?, claimed_by_runner_id = ?,
		 heartbeat_at = ?, claimed_at = ?,
		 attempt_count = COALESCE(attempt_count,0) + 1, updated_at = ?
		 WHERE id = ? AND user_id = ? AND status IN ('pending','dispatched')
		 RETURNING `+jobSelectCols,
		claimedByAgentID, claimedByRunnerID, now, now, now, id, userID,
	)
	j, err := scanJob(row)
	if err != nil {
		// Distinguish missing vs already claimed.
		cur, gerr := s.GetJob(userID, id)
		if gerr != nil {
			return nil, fmt.Errorf("job not found")
		}
		return nil, fmt.Errorf("job not claimable: %s", cur.Status)
	}
	return j, nil
}

func (s *SQLite) UpdateJob(j *Job) error {
	hb := ""
	if !j.HeartbeatAt.IsZero() {
		hb = j.HeartbeatAt.UTC().Format(time.RFC3339Nano)
	}
	ca := ""
	if !j.ClaimedAt.IsZero() {
		ca = j.ClaimedAt.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.Exec(
		`UPDATE jobs SET drive_id=?, binding_id=?, mode=?, command_json=?, status=?, region_hint=?, note=?,
		 agent_id=?, claimed_by_agent_id=?, connector_id=?, claimed_by_runner_id=?, priority=?, labels_json=?,
		 idempotency_key=?,
		 exit_code=?, duration_ms=?, heartbeat_at=?,
		 claimed_at=?, timeout_sec=?, attempt_count=?, max_attempts=?, stdout=?, stderr=?,
		 stdout_truncated=?, stderr_truncated=?, updated_at=?
		 WHERE id=? AND user_id=?`,
		j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
		j.AgentID, j.ClaimedByAgentID, j.ConnectorID, j.ClaimedByRunnerID, j.Priority, string(j.LabelsJSON),
		j.IdempotencyKey,
		nullInt(j.ExitCode), j.DurationMs, hb,
		ca, j.TimeoutSec, j.AttemptCount, j.MaxAttempts, j.Stdout, j.Stderr,
		boolInt(j.StdoutTruncated), boolInt(j.StderrTruncated),
		j.UpdatedAt.UTC().Format(time.RFC3339Nano), j.ID, j.UserID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job not found")
	}
	return nil
}

func (s *SQLite) CreateSnapshot(sn *Snapshot) error {
	_, err := s.db.Exec(
		`INSERT INTO snapshots (id, user_id, drive_id, agent_id, label, note, payload_json, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		sn.ID, sn.UserID, sn.DriveID, sn.AgentID, sn.Label, sn.Note, string(sn.PayloadJSON),
		sn.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) GetSnapshot(userID, driveID, id string) (*Snapshot, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, drive_id, COALESCE(agent_id,''), COALESCE(label,''), COALESCE(note,''), payload_json, created_at
		 FROM snapshots WHERE id=? AND user_id=? AND drive_id=?`,
		id, userID, driveID,
	)
	return scanSnapshot(row)
}

func (s *SQLite) ListSnapshots(userID, driveID string, limit int) ([]*Snapshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, drive_id, COALESCE(agent_id,''), COALESCE(label,''), COALESCE(note,''), payload_json, created_at
		 FROM snapshots WHERE user_id=? AND drive_id=? ORDER BY created_at DESC LIMIT ?`,
		userID, driveID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Snapshot
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteSnapshot(userID, driveID, id string) error {
	res, err := s.db.Exec(`DELETE FROM snapshots WHERE id=? AND user_id=? AND drive_id=?`, id, userID, driveID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("snapshot not found")
	}
	return nil
}

func (s *SQLite) CreateMemory(e *MemoryEntry) error {
	exp := ""
	if !e.ExpiresAt.IsZero() {
		exp = e.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(
		`INSERT INTO memory_entries (id, user_id, agent_id, drive_id, layer, key, content, meta_json, embedding_json, created_at, expires_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.UserID, e.AgentID, e.DriveID, e.Layer, e.Key, e.Content, string(e.MetaJSON), string(e.EmbeddingJSON),
		e.CreatedAt.UTC().Format(time.RFC3339Nano), exp,
	)
	return err
}

func (s *SQLite) GetMemory(userID, id string) (*MemoryEntry, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, agent_id, drive_id, layer, key, content, meta_json, COALESCE(embedding_json,''), created_at, expires_at
		 FROM memory_entries WHERE id=? AND user_id=?`, id, userID)
	return scanMemory(row)
}

func (s *SQLite) ListMemory(f MemoryFilter) ([]*MemoryEntry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, agent_id, drive_id, layer, key, content, meta_json, COALESCE(embedding_json,''), created_at, expires_at
		 FROM memory_entries WHERE user_id=? ORDER BY created_at DESC LIMIT ?`, f.UserID, f.Limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MemoryEntry
	now := time.Now()
	for rows.Next() {
		e, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if f.AgentID != "" && e.AgentID != f.AgentID {
			continue
		}
		if f.DriveID != "" && e.DriveID != f.DriveID {
			continue
		}
		if f.Layer != "" && e.Layer != f.Layer {
			continue
		}
		if f.Key != "" && e.Key != f.Key {
			continue
		}
		if !e.ExpiresAt.IsZero() && e.ExpiresAt.Before(now) {
			continue
		}
		out = append(out, e)
		if len(out) >= f.Limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteMemory(userID, id string) error {
	res, err := s.db.Exec(`DELETE FROM memory_entries WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory not found")
	}
	return nil
}

func scanMemory(row interface{ Scan(dest ...any) error }) (*MemoryEntry, error) {
	var e MemoryEntry
	var meta, emb, created, exp string
	if err := row.Scan(&e.ID, &e.UserID, &e.AgentID, &e.DriveID, &e.Layer, &e.Key, &e.Content, &meta, &emb, &created, &exp); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("memory not found")
		}
		return nil, err
	}
	e.MetaJSON = []byte(meta)
	if emb != "" {
		e.EmbeddingJSON = []byte(emb)
	}
	e.CreatedAt = parseTime(created)
	if exp != "" {
		e.ExpiresAt = parseTime(exp)
	}
	return &e, nil
}

func (s *SQLite) CreateMarketplaceItem(m *MarketplaceItem) error {
	pub := 0
	if m.Public {
		pub = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO marketplace_items (id, publisher_user_id, name, description, kind, version, payload_json, public, price_cents, currency, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.PublisherUserID, m.Name, m.Description, m.Kind, m.Version, string(m.PayloadJSON), pub, m.PriceCents, m.Currency,
		m.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) GetMarketplaceItem(id string) (*MarketplaceItem, error) {
	row := s.db.QueryRow(
		`SELECT id, publisher_user_id, name, description, kind, version, payload_json, public, COALESCE(price_cents,0), COALESCE(currency,''), created_at
		 FROM marketplace_items WHERE id=?`, id)
	return scanMarket(row)
}

func (s *SQLite) ListMarketplaceItems(publicOnly bool, publisherUserID string) ([]*MarketplaceItem, error) {
	rows, err := s.db.Query(
		`SELECT id, publisher_user_id, name, description, kind, version, payload_json, public, COALESCE(price_cents,0), COALESCE(currency,''), created_at
		 FROM marketplace_items ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MarketplaceItem
	for rows.Next() {
		it, err := scanMarket(rows)
		if err != nil {
			return nil, err
		}
		if publicOnly && !it.Public {
			continue
		}
		if publisherUserID != "" && it.PublisherUserID != publisherUserID {
			continue
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteMarketplaceItem(publisherUserID, id string) error {
	res, err := s.db.Exec(`DELETE FROM marketplace_items WHERE id=? AND publisher_user_id=?`, id, publisherUserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("marketplace item not found")
	}
	return nil
}

func scanMarket(row interface{ Scan(dest ...any) error }) (*MarketplaceItem, error) {
	var m MarketplaceItem
	var payload, created, cur string
	var pub int
	if err := row.Scan(&m.ID, &m.PublisherUserID, &m.Name, &m.Description, &m.Kind, &m.Version, &payload, &pub, &m.PriceCents, &cur, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("marketplace item not found")
		}
		return nil, err
	}
	m.PayloadJSON = []byte(payload)
	m.Public = pub != 0
	m.Currency = cur
	m.CreatedAt = parseTime(created)
	return &m, nil
}

func (s *SQLite) AppendLineage(e *LineageEvent) error {
	_, err := s.db.Exec(
		`INSERT INTO lineage_events (id, user_id, actor_id, action, entity, parent, detail, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		e.ID, e.UserID, e.ActorID, e.Action, e.Entity, e.Parent, e.Detail, e.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) ListLineage(userID, entity string, limit int) ([]*LineageEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, actor_id, action, entity, parent, detail, created_at FROM lineage_events
		 WHERE user_id=? ORDER BY created_at DESC LIMIT ?`, userID, limit*2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LineageEvent
	for rows.Next() {
		var e LineageEvent
		var created string
		if err := rows.Scan(&e.ID, &e.UserID, &e.ActorID, &e.Action, &e.Entity, &e.Parent, &e.Detail, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		if entity != "" && e.Entity != entity && e.Parent != entity {
			continue
		}
		out = append(out, &e)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *SQLite) UpsertGraphEdge(e *GraphEdge) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO graph_edges (id, user_id, subject, relation, object, meta_json, created_at) VALUES (?,?,?,?,?,?,?)`,
		e.ID, e.UserID, e.Subject, e.Relation, e.Object, string(e.MetaJSON), e.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) ListGraphEdges(userID, subject, object string, limit int) ([]*GraphEdge, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, subject, relation, object, meta_json, created_at FROM graph_edges WHERE user_id=? LIMIT ?`,
		userID, limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*GraphEdge
	for rows.Next() {
		var e GraphEdge
		var meta, created string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Subject, &e.Relation, &e.Object, &meta, &created); err != nil {
			return nil, err
		}
		e.MetaJSON = []byte(meta)
		e.CreatedAt = parseTime(created)
		if subject != "" && e.Subject != subject {
			continue
		}
		if object != "" && e.Object != object {
			continue
		}
		out = append(out, &e)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *SQLite) CreatePurchase(p *Purchase) error {
	_, err := s.db.Exec(
		`INSERT INTO purchases (id, user_id, item_id, amount_cents, currency, status, provider, provider_ref, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		p.ID, p.UserID, p.ItemID, p.AmountCents, p.Currency, p.Status, p.Provider, p.ProviderRef, p.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) GetPurchase(userID, id string) (*Purchase, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, item_id, amount_cents, currency, status, provider, provider_ref, created_at FROM purchases WHERE id=? AND user_id=?`,
		id, userID)
	var p Purchase
	var created string
	if err := row.Scan(&p.ID, &p.UserID, &p.ItemID, &p.AmountCents, &p.Currency, &p.Status, &p.Provider, &p.ProviderRef, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("purchase not found")
		}
		return nil, err
	}
	p.CreatedAt = parseTime(created)
	return &p, nil
}

func (s *SQLite) ListPurchases(userID string, limit int) ([]*Purchase, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, item_id, amount_cents, currency, status, provider, provider_ref, created_at FROM purchases WHERE user_id=? ORDER BY created_at DESC LIMIT ?`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Purchase
	for rows.Next() {
		var p Purchase
		var created string
		if err := rows.Scan(&p.ID, &p.UserID, &p.ItemID, &p.AmountCents, &p.Currency, &p.Status, &p.Provider, &p.ProviderRef, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(created)
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *SQLite) UpdatePurchase(p *Purchase) error {
	res, err := s.db.Exec(
		`UPDATE purchases SET status=?, provider=?, provider_ref=? WHERE id=? AND user_id=?`,
		p.Status, p.Provider, p.ProviderRef, p.ID, p.UserID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("purchase not found")
	}
	return nil
}

func (s *SQLite) CreateConnector(c *ConnectorBinding) error {
	_, err := s.db.Exec(
		`INSERT INTO connectors (id, user_id, type, name, config_json, status, created_at) VALUES (?,?,?,?,?,?,?)`,
		c.ID, c.UserID, c.Type, c.Name, string(c.ConfigJSON), c.Status, c.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) GetConnector(userID, id string) (*ConnectorBinding, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, type, name, config_json, status, created_at FROM connectors WHERE id=? AND user_id=?`, id, userID)
	var c ConnectorBinding
	var cfg, created string
	if err := row.Scan(&c.ID, &c.UserID, &c.Type, &c.Name, &cfg, &c.Status, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("connector not found")
		}
		return nil, err
	}
	c.ConfigJSON = []byte(cfg)
	c.CreatedAt = parseTime(created)
	return &c, nil
}

func (s *SQLite) ListConnectors(userID string) ([]*ConnectorBinding, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, type, name, config_json, status, created_at FROM connectors WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ConnectorBinding
	for rows.Next() {
		var c ConnectorBinding
		var cfg, created string
		if err := rows.Scan(&c.ID, &c.UserID, &c.Type, &c.Name, &cfg, &c.Status, &created); err != nil {
			return nil, err
		}
		c.ConfigJSON = []byte(cfg)
		c.CreatedAt = parseTime(created)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteConnector(userID, id string) error {
	res, err := s.db.Exec(`DELETE FROM connectors WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("connector not found")
	}
	return nil
}

func (s *SQLite) EnqueueWebhookOutbox(e *WebhookOutbox) error {
	if e == nil || strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("webhook outbox id required")
	}
	delivered := ""
	if !e.DeliveredAt.IsZero() {
		delivered = e.DeliveredAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(
		`INSERT INTO job_webhook_outbox
		 (id, job_id, user_id, event, payload_json, status, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.JobID, e.UserID, e.Event, string(e.PayloadJSON), e.Status, e.Attempts,
		e.NextAttemptAt.UTC().Format(time.RFC3339Nano), e.LastError,
		e.CreatedAt.UTC().Format(time.RFC3339Nano), e.UpdatedAt.UTC().Format(time.RFC3339Nano), delivered,
	)
	if err != nil {
		return fmt.Errorf("enqueue webhook outbox: %w", err)
	}
	return nil
}

func (s *SQLite) ListDueWebhookOutbox(now time.Time, limit int) ([]*WebhookOutbox, error) {
	if limit <= 0 {
		limit = 32
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT id, job_id, user_id, event, payload_json, status, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at
		 FROM job_webhook_outbox
		 WHERE status = 'pending' AND next_attempt_at <= ?
		 ORDER BY next_attempt_at ASC LIMIT ?`,
		now.UTC().Format(time.RFC3339Nano), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebhookOutbox
	for rows.Next() {
		e, err := scanWebhookOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQLite) UpdateWebhookOutbox(e *WebhookOutbox) error {
	if e == nil || strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("webhook outbox id required")
	}
	delivered := ""
	if !e.DeliveredAt.IsZero() {
		delivered = e.DeliveredAt.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.Exec(
		`UPDATE job_webhook_outbox SET job_id=?, user_id=?, event=?, payload_json=?, status=?, attempts=?,
		 next_attempt_at=?, last_error=?, updated_at=?, delivered_at=? WHERE id=?`,
		e.JobID, e.UserID, e.Event, string(e.PayloadJSON), e.Status, e.Attempts,
		e.NextAttemptAt.UTC().Format(time.RFC3339Nano), e.LastError,
		e.UpdatedAt.UTC().Format(time.RFC3339Nano), delivered, e.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("webhook outbox not found")
	}
	return nil
}

func scanWebhookOutbox(row scannable) (*WebhookOutbox, error) {
	var e WebhookOutbox
	var payload, nextAt, created, updated, delivered string
	if err := row.Scan(
		&e.ID, &e.JobID, &e.UserID, &e.Event, &payload, &e.Status, &e.Attempts,
		&nextAt, &e.LastError, &created, &updated, &delivered,
	); err != nil {
		return nil, err
	}
	e.PayloadJSON = []byte(payload)
	e.NextAttemptAt = parseTime(nextAt)
	e.CreatedAt = parseTime(created)
	e.UpdatedAt = parseTime(updated)
	if delivered != "" {
		e.DeliveredAt = parseTime(delivered)
	}
	return &e, nil
}

func (s *SQLite) GetWebhookOutbox(id string) (*WebhookOutbox, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("webhook outbox not found")
	}
	row := s.db.QueryRow(
		`SELECT id, job_id, user_id, event, payload_json, status, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at
		 FROM job_webhook_outbox WHERE id = ?`, id,
	)
	e, err := scanWebhookOutbox(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("webhook outbox not found")
		}
		// scanWebhookOutbox may wrap or return other errors
		if strings.Contains(err.Error(), "no rows") {
			return nil, fmt.Errorf("webhook outbox not found")
		}
		return nil, err
	}
	return e, nil
}

func (s *SQLite) ListWebhookOutbox(f WebhookOutboxFilter) ([]*WebhookOutbox, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	q := `SELECT id, job_id, user_id, event, payload_json, status, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at
		 FROM job_webhook_outbox WHERE 1=1`
	var args []interface{}
	if strings.TrimSpace(f.Status) != "" {
		q += ` AND status = ?`
		args = append(args, strings.TrimSpace(f.Status))
	}
	if strings.TrimSpace(f.JobID) != "" {
		q += ` AND job_id = ?`
		args = append(args, strings.TrimSpace(f.JobID))
	}
	if strings.TrimSpace(f.UserID) != "" {
		q += ` AND user_id = ?`
		args = append(args, strings.TrimSpace(f.UserID))
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebhookOutbox
	for rows.Next() {
		e, err := scanWebhookOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQLite) PurgeWebhookOutbox(olderThan time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	cut := olderThan.UTC().Format(time.RFC3339Nano)
	// Select ids then delete (SQLite DELETE ... LIMIT is fine on 3.x).
	rows, err := s.db.Query(
		`SELECT id FROM job_webhook_outbox
		 WHERE (status = 'delivered' AND (
		          (delivered_at IS NOT NULL AND delivered_at != '' AND delivered_at < ?)
		          OR ((delivered_at IS NULL OR delivered_at = '') AND updated_at < ?)
		       ))
		    OR (status = 'dead' AND updated_at < ?)
		 ORDER BY COALESCE(NULLIF(delivered_at,''), updated_at) ASC
		 LIMIT ?`,
		cut, cut, cut, limit,
	)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		res, err := s.db.Exec(`DELETE FROM job_webhook_outbox WHERE id = ?`, id)
		if err != nil {
			return n, err
		}
		aff, _ := res.RowsAffected()
		n += int(aff)
	}
	return n, nil
}

func scanSnapshot(row interface{ Scan(dest ...any) error }) (*Snapshot, error) {
	var sn Snapshot
	var payload, created string
	if err := row.Scan(&sn.ID, &sn.UserID, &sn.DriveID, &sn.AgentID, &sn.Label, &sn.Note, &payload, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("snapshot not found")
		}
		return nil, err
	}
	sn.PayloadJSON = []byte(payload)
	sn.CreatedAt = parseTime(created)
	return &sn, nil
}

func scanJob(row scannable) (*Job, error) {
	var j Job
	var cmd, created, updated, heartbeat, claimedAt, stdout, stderr string
	var exitCode sql.NullInt64
	var durationMs int64
	var timeoutSec, attemptCount, maxAttempts, priority, outTrunc, errTrunc int
	var labels, idem string
	if err := row.Scan(
		&j.ID, &j.UserID, &j.DriveID, &j.BindingID, &j.Mode, &cmd, &j.Status, &j.RegionHint, &j.Note,
		&j.AgentID, &j.ClaimedByAgentID, &j.ConnectorID, &j.ClaimedByRunnerID, &priority, &labels, &idem,
		&exitCode, &durationMs, &heartbeat, &claimedAt,
		&timeoutSec, &attemptCount, &maxAttempts, &stdout, &stderr, &outTrunc, &errTrunc,
		&created, &updated,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job not found")
		}
		return nil, err
	}
	j.CommandJSON = []byte(cmd)
	if exitCode.Valid {
		v := int(exitCode.Int64)
		j.ExitCode = &v
	}
	j.DurationMs = durationMs
	if strings.TrimSpace(heartbeat) != "" {
		j.HeartbeatAt = parseTime(heartbeat)
	}
	if strings.TrimSpace(claimedAt) != "" {
		j.ClaimedAt = parseTime(claimedAt)
	}
	j.Priority = priority
	if strings.TrimSpace(labels) != "" {
		j.LabelsJSON = []byte(labels)
	}
	j.IdempotencyKey = idem
	j.TimeoutSec = timeoutSec
	j.AttemptCount = attemptCount
	j.MaxAttempts = maxAttempts
	j.Stdout = stdout
	j.Stderr = stderr
	j.StdoutTruncated = outTrunc != 0
	j.StderrTruncated = errTrunc != 0
	j.CreatedAt = parseTime(created)
	j.UpdatedAt = parseTime(updated)
	return &j, nil
}

func nullInt(p *int) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
