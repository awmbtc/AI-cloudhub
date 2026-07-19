package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agents_owner ON agents(owner_user_id);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Soft migrations for existing DBs (ignore "duplicate column" errors).
	for _, stmt := range []string{
		`ALTER TABLE drives ADD COLUMN region TEXT`,
		`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user'`,
		`ALTER TABLE users ADD COLUMN token_version INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			// Column already exists on upgraded installs — safe to ignore.
			_ = err
		}
	}
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
		`INSERT INTO audit_events (id, user_id, action, target, detail, created_at) VALUES (?,?,?,?,?,?)`,
		e.ID, e.UserID, e.Action, e.Target, e.Detail, e.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) ListAudit(f AuditFilter) ([]*AuditEvent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, user_id, action, target, detail, created_at FROM audit_events WHERE 1=1`
	var args []any
	if f.UserID != "" {
		q += ` AND user_id = ?`
		args = append(args, f.UserID)
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
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &e.Target, &e.Detail, &created); err != nil {
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

func (s *SQLite) CreateAgent(a *Agent) error {
	scopes, err := MarshalJSON(a.DefaultScopes)
	if err != nil {
		return err
	}
	if scopes == nil {
		scopes = []byte("[]")
	}
	_, err = s.db.Exec(
		`INSERT INTO agents (id, owner_user_id, name, description, status, default_scopes, created_at) VALUES (?,?,?,?,?,?,?)`,
		a.ID, a.OwnerUserID, a.Name, a.Description, a.Status, string(scopes),
		a.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) GetAgent(ownerUserID, id string) (*Agent, error) {
	row := s.db.QueryRow(
		`SELECT id, owner_user_id, name, description, status, default_scopes, created_at FROM agents WHERE id = ? AND owner_user_id = ?`,
		id, ownerUserID,
	)
	return scanAgent(row)
}

func (s *SQLite) ListAgents(ownerUserID string) ([]*Agent, error) {
	rows, err := s.db.Query(
		`SELECT id, owner_user_id, name, description, status, default_scopes, created_at FROM agents WHERE owner_user_id = ? ORDER BY created_at DESC`,
		ownerUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		a, err := scanAgentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
	var scopes string
	var created string
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.Name, &a.Description, &a.Status, &scopes, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found")
		}
		return nil, err
	}
	_ = UnmarshalJSON([]byte(scopes), &a.DefaultScopes)
	a.CreatedAt = parseTime(created)
	return &a, nil
}

func scanAgentRows(rows *sql.Rows) (*Agent, error) {
	return scanAgent(rows)
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

func (s *SQLite) CreateJob(j *Job) error {
	_, err := s.db.Exec(
		`INSERT INTO jobs (id, user_id, drive_id, binding_id, mode, command_json, status, region_hint, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.UserID, j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
		j.CreatedAt.UTC().Format(time.RFC3339Nano), j.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

func (s *SQLite) GetJob(userID, id string) (*Job, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, drive_id, binding_id, mode, command_json, status, region_hint, note, created_at, updated_at
		 FROM jobs WHERE id = ? AND user_id = ?`, id, userID,
	)
	return scanJob(row)
}

func (s *SQLite) ListJobs(userID string) ([]*Job, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, drive_id, binding_id, mode, command_json, status, region_hint, note, created_at, updated_at
		 FROM jobs WHERE user_id = ? ORDER BY created_at DESC`, userID,
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

func (s *SQLite) ListPendingJobs(userID string) ([]*Job, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, drive_id, binding_id, mode, command_json, status, region_hint, note, created_at, updated_at
		 FROM jobs WHERE user_id = ? AND status IN ('pending','dispatched') ORDER BY created_at ASC`, userID,
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
func (s *SQLite) ClaimPendingJob(userID, id string) (*Job, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := s.db.QueryRow(
		`UPDATE jobs SET status = 'running', updated_at = ?
		 WHERE id = ? AND user_id = ? AND status IN ('pending','dispatched')
		 RETURNING id, user_id, drive_id, binding_id, mode, command_json, status, region_hint, note, created_at, updated_at`,
		now, id, userID,
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
	res, err := s.db.Exec(
		`UPDATE jobs SET drive_id=?, binding_id=?, mode=?, command_json=?, status=?, region_hint=?, note=?, updated_at=?
		 WHERE id=? AND user_id=?`,
		j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
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

func scanJob(row scannable) (*Job, error) {
	var j Job
	var cmd, created, updated string
	if err := row.Scan(
		&j.ID, &j.UserID, &j.DriveID, &j.BindingID, &j.Mode, &cmd, &j.Status, &j.RegionHint, &j.Note, &created, &updated,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job not found")
		}
		return nil, err
	}
	j.CommandJSON = []byte(cmd)
	j.CreatedAt = parseTime(created)
	j.UpdatedAt = parseTime(updated)
	return &j, nil
}
