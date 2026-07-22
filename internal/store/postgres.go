package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Postgres implements Store with PostgreSQL (multi-replica friendly).
type Postgres struct {
	db *sql.DB
}

// OpenPostgres opens a Postgres DSN (postgres:// or postgresql://) and migrates.
func OpenPostgres(dsn string) (*Postgres, error) {
	if dsn == "" {
		return nil, fmt.Errorf("postgres dsn required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	p := &Postgres{db: db}
	if err := p.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

// Migrate applies schema (idempotent).
func (p *Postgres) Migrate() error {
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
  created_at TIMESTAMPTZ NOT NULL
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
  updated_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bindings_user ON bindings(user_id);
CREATE INDEX IF NOT EXISTS idx_bindings_device ON bindings(user_id, device_id);
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  last_seen TIMESTAMPTZ NOT NULL
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
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_user ON jobs(user_id);
CREATE INDEX IF NOT EXISTS idx_jobs_user_status ON jobs(user_id, status);
CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  user_id TEXT,
  action TEXT NOT NULL,
  target TEXT,
  detail TEXT,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_events(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_events(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_events(action);
CREATE TABLE IF NOT EXISTS revoked_jtis (
  jti TEXT PRIMARY KEY,
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_revoked_jtis_exp ON revoked_jtis(expires_at);
CREATE TABLE IF NOT EXISTS refresh_tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  revoked BOOLEAN NOT NULL DEFAULT FALSE
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
  created_at TIMESTAMPTZ NOT NULL
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
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_drive ON snapshots(user_id, drive_id, created_at);
`
	if _, err := p.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate postgres: %w", err)
	}
	// Soft migrate older DBs
	_, _ = p.db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user'`)
	_, _ = p.db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS token_version INTEGER NOT NULL DEFAULT 0`)
	_, _ = p.db.Exec(`ALTER TABLE agents ADD COLUMN IF NOT EXISTS allowed_drive_ids TEXT NOT NULL DEFAULT '[]'`)
	_, _ = p.db.Exec(`ALTER TABLE agents ADD COLUMN IF NOT EXISTS read_prefixes TEXT NOT NULL DEFAULT '[]'`)
	_, _ = p.db.Exec(`ALTER TABLE agents ADD COLUMN IF NOT EXISTS write_prefixes TEXT NOT NULL DEFAULT '[]'`)
	_, _ = p.db.Exec(`ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS agent_id TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS agent_id TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS claimed_by_agent_id TEXT`)
	return nil
}

func (p *Postgres) Close() error {
	if p.db == nil {
		return nil
	}
	return p.db.Close()
}

func (p *Postgres) CreateUser(u *User) error {
	role := u.Role
	if role == "" {
		role = "user"
	}
	_, err := p.db.Exec(
		`INSERT INTO users (id, username, password, role, token_version) VALUES ($1,$2,$3,$4,$5)`,
		u.ID, u.Username, u.Password, role, u.TokenVersion,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (p *Postgres) GetUserByUsername(username string) (*User, error) {
	row := p.db.QueryRow(
		`SELECT id, username, password, COALESCE(role,'user'), COALESCE(token_version,0) FROM users WHERE username=$1`,
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

func (p *Postgres) GetUserByID(id string) (*User, error) {
	row := p.db.QueryRow(
		`SELECT id, username, password, COALESCE(role,'user'), COALESCE(token_version,0) FROM users WHERE id=$1`,
		id,
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

func (p *Postgres) CountUsers() (int, error) {
	var n int
	err := p.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

func (p *Postgres) UpdateUserRole(userID, role string) error {
	res, err := p.db.Exec(`UPDATE users SET role=$1 WHERE id=$2`, role, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (p *Postgres) Ping() error {
	return p.db.Ping()
}

func (p *Postgres) ListUsers() ([]*User, error) {
	rows, err := p.db.Query(`SELECT id, username, COALESCE(role,'user') FROM users ORDER BY username`)
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

func (p *Postgres) AppendAudit(e *AuditEvent) error {
	_, err := p.db.Exec(
		`INSERT INTO audit_events (id, user_id, agent_id, action, target, detail, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.ID, e.UserID, e.AgentID, e.Action, e.Target, e.Detail, e.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) ListAudit(f AuditFilter) ([]*AuditEvent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, user_id, COALESCE(agent_id,''), action, target, detail, created_at FROM audit_events WHERE 1=1`
	var args []any
	n := 1
	if f.UserID != "" {
		q += fmt.Sprintf(` AND user_id = $%d`, n)
		args = append(args, f.UserID)
		n++
	}
	if f.AgentID != "" {
		q += fmt.Sprintf(` AND agent_id = $%d`, n)
		args = append(args, f.AgentID)
		n++
	}
	if f.Action != "" {
		q += fmt.Sprintf(` AND action = $%d`, n)
		args = append(args, f.Action)
		n++
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, n)
	args = append(args, limit)
	rows, err := p.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditEvent
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.AgentID, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (p *Postgres) BumpTokenVersion(userID string) (int, error) {
	var ver int
	err := p.db.QueryRow(
		`UPDATE users SET token_version = COALESCE(token_version,0) + 1 WHERE id = $1 RETURNING token_version`,
		userID,
	).Scan(&ver)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("user not found")
	}
	if err != nil {
		return 0, err
	}
	return ver, nil
}

func (p *Postgres) RevokeJTI(jti string, expiresAt time.Time) error {
	if jti == "" {
		return fmt.Errorf("jti required")
	}
	_, _ = p.db.Exec(`DELETE FROM revoked_jtis WHERE expires_at < NOW()`)
	_, err := p.db.Exec(
		`INSERT INTO revoked_jtis (jti, expires_at) VALUES ($1,$2)
		 ON CONFLICT (jti) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		jti, expiresAt.UTC(),
	)
	return err
}

func (p *Postgres) IsJTIRevoked(jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	var exp time.Time
	err := p.db.QueryRow(`SELECT expires_at FROM revoked_jtis WHERE jti = $1`, jti).Scan(&exp)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if time.Now().After(exp) {
		_, _ = p.db.Exec(`DELETE FROM revoked_jtis WHERE jti = $1`, jti)
		return false, nil
	}
	return true, nil
}

func (p *Postgres) CreateRefreshToken(t *RefreshToken) error {
	_, err := p.db.Exec(
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, revoked) VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ID, t.UserID, t.TokenHash, t.ExpiresAt.UTC(), t.CreatedAt.UTC(), t.Revoked,
	)
	return err
}

func (p *Postgres) GetRefreshTokenByHash(tokenHash string) (*RefreshToken, error) {
	row := p.db.QueryRow(
		`SELECT id, user_id, token_hash, expires_at, created_at, revoked FROM refresh_tokens WHERE token_hash = $1`,
		tokenHash,
	)
	var t RefreshToken
	if err := row.Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.CreatedAt, &t.Revoked); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("refresh token not found")
		}
		return nil, err
	}
	if t.Revoked || time.Now().After(t.ExpiresAt) {
		return nil, fmt.Errorf("refresh token invalid")
	}
	return &t, nil
}

func (p *Postgres) RevokeRefreshToken(id string) error {
	res, err := p.db.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("refresh token not found")
	}
	return nil
}

func (p *Postgres) RevokeRefreshTokensForUser(userID string) error {
	_, err := p.db.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1 AND revoked = FALSE`, userID)
	return err
}

func pgAgentJSON(v []string) string {
	b, err := MarshalJSON(v)
	if err != nil || b == nil {
		return "[]"
	}
	return string(b)
}

func (p *Postgres) CreateAgent(a *Agent) error {
	_, err := p.db.Exec(
		`INSERT INTO agents (id, owner_user_id, name, description, status, default_scopes, allowed_drive_ids, read_prefixes, write_prefixes, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		a.ID, a.OwnerUserID, a.Name, a.Description, a.Status,
		pgAgentJSON(a.DefaultScopes), pgAgentJSON(a.AllowedDriveIDs),
		pgAgentJSON(a.ReadPrefixes), pgAgentJSON(a.WritePrefixes), a.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) scanAgentRow(row interface{ Scan(dest ...any) error }) (*Agent, error) {
	var a Agent
	var scopes, drives, rpref, wpref string
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.Name, &a.Description, &a.Status, &scopes, &drives, &rpref, &wpref, &a.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found")
		}
		return nil, err
	}
	_ = UnmarshalJSON([]byte(scopes), &a.DefaultScopes)
	_ = UnmarshalJSON([]byte(drives), &a.AllowedDriveIDs)
	_ = UnmarshalJSON([]byte(rpref), &a.ReadPrefixes)
	_ = UnmarshalJSON([]byte(wpref), &a.WritePrefixes)
	return &a, nil
}

func (p *Postgres) GetAgent(ownerUserID, id string) (*Agent, error) {
	row := p.db.QueryRow(
		`SELECT id, owner_user_id, name, description, status, default_scopes,
		 COALESCE(allowed_drive_ids,'[]'), COALESCE(read_prefixes,'[]'), COALESCE(write_prefixes,'[]'), created_at
		 FROM agents WHERE id = $1 AND owner_user_id = $2`,
		id, ownerUserID,
	)
	return p.scanAgentRow(row)
}

func (p *Postgres) GetAgentByID(id string) (*Agent, error) {
	row := p.db.QueryRow(
		`SELECT id, owner_user_id, name, description, status, default_scopes,
		 COALESCE(allowed_drive_ids,'[]'), COALESCE(read_prefixes,'[]'), COALESCE(write_prefixes,'[]'), created_at
		 FROM agents WHERE id = $1`,
		id,
	)
	return p.scanAgentRow(row)
}

func (p *Postgres) ListAgents(ownerUserID string) ([]*Agent, error) {
	rows, err := p.db.Query(
		`SELECT id, owner_user_id, name, description, status, default_scopes,
		 COALESCE(allowed_drive_ids,'[]'), COALESCE(read_prefixes,'[]'), COALESCE(write_prefixes,'[]'), created_at
		 FROM agents WHERE owner_user_id = $1 ORDER BY created_at DESC`,
		ownerUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		a, err := p.scanAgentRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateAgent(a *Agent) error {
	res, err := p.db.Exec(
		`UPDATE agents SET name=$1, description=$2, status=$3, default_scopes=$4, allowed_drive_ids=$5, read_prefixes=$6, write_prefixes=$7
		 WHERE id=$8 AND owner_user_id=$9`,
		a.Name, a.Description, a.Status,
		pgAgentJSON(a.DefaultScopes), pgAgentJSON(a.AllowedDriveIDs),
		pgAgentJSON(a.ReadPrefixes), pgAgentJSON(a.WritePrefixes),
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

func (p *Postgres) DeleteAgent(ownerUserID, id string) error {
	res, err := p.db.Exec(`DELETE FROM agents WHERE id = $1 AND owner_user_id = $2`, id, ownerUserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func (p *Postgres) UpdateUserPassword(userID, hash string) error {
	res, err := p.db.Exec(`UPDATE users SET password=$1 WHERE id=$2`, hash, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// EnsurePostgresRoleColumn soft-migrates older PG installs.
func (p *Postgres) EnsurePostgresRoleColumn() {
	_, _ = p.db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user'`)
}

func (p *Postgres) CreateProvider(pr *Provider) error {
	_, err := p.db.Exec(
		`INSERT INTO providers (id,user_id,name,type,creds_json,endpoint_public,region,account_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		pr.ID, pr.UserID, pr.Name, pr.Type, string(pr.CredsJSON), pr.EndpointPublic, pr.Region, pr.AccountID,
	)
	return err
}

func (p *Postgres) GetProvider(userID, id string) (*Provider, error) {
	row := p.db.QueryRow(
		`SELECT id,user_id,name,type,creds_json,endpoint_public,region,account_id FROM providers WHERE id=$1 AND user_id=$2`,
		id, userID,
	)
	var pr Provider
	var creds string
	if err := row.Scan(&pr.ID, &pr.UserID, &pr.Name, &pr.Type, &creds, &pr.EndpointPublic, &pr.Region, &pr.AccountID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("provider not found")
		}
		return nil, err
	}
	pr.CredsJSON = []byte(creds)
	return &pr, nil
}

func (p *Postgres) ListProviders(userID string) ([]*Provider, error) {
	rows, err := p.db.Query(
		`SELECT id,user_id,name,type,creds_json,endpoint_public,region,account_id FROM providers WHERE user_id=$1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Provider
	for rows.Next() {
		var pr Provider
		var creds string
		if err := rows.Scan(&pr.ID, &pr.UserID, &pr.Name, &pr.Type, &creds, &pr.EndpointPublic, &pr.Region, &pr.AccountID); err != nil {
			return nil, err
		}
		pr.CredsJSON = []byte(creds)
		out = append(out, &pr)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteProvider(userID, id string) error {
	res, err := p.db.Exec(`DELETE FROM providers WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("provider not found")
	}
	return nil
}

func (p *Postgres) CreateDrive(d *Drive) error {
	_, err := p.db.Exec(
		`INSERT INTO drives (id,user_id,name,provider_id,bucket,prefix,mount_point,region,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		d.ID, d.UserID, d.Name, d.ProviderID, d.Bucket, d.Prefix, d.MountPoint, d.Region, d.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) GetDrive(userID, id string) (*Drive, error) {
	row := p.db.QueryRow(
		`SELECT id,user_id,name,provider_id,bucket,prefix,mount_point,region,created_at FROM drives WHERE id=$1 AND user_id=$2`,
		id, userID,
	)
	var d Drive
	var region sql.NullString
	if err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.ProviderID, &d.Bucket, &d.Prefix, &d.MountPoint, &region, &d.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("drive not found")
		}
		return nil, err
	}
	if region.Valid {
		d.Region = region.String
	}
	return &d, nil
}

func (p *Postgres) UpdateDrive(d *Drive) error {
	res, err := p.db.Exec(
		`UPDATE drives SET name=$1, prefix=$2, mount_point=$3, region=$4 WHERE id=$5 AND user_id=$6`,
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

func (p *Postgres) ListDrives(userID string) ([]*Drive, error) {
	rows, err := p.db.Query(
		`SELECT id,user_id,name,provider_id,bucket,prefix,mount_point,region,created_at FROM drives WHERE user_id=$1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Drive
	for rows.Next() {
		var d Drive
		var region sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.ProviderID, &d.Bucket, &d.Prefix, &d.MountPoint, &region, &d.CreatedAt); err != nil {
			return nil, err
		}
		if region.Valid {
			d.Region = region.String
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteDrive(userID, id string) error {
	res, err := p.db.Exec(`DELETE FROM drives WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("drive not found")
	}
	return nil
}

func (p *Postgres) CreateBinding(b *Binding) error {
	_, err := p.db.Exec(
		`INSERT INTO bindings (id,user_id,drive_id,device_id,mount_point,mode,desired,actual,last_error,updated_at,created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		b.ID, b.UserID, b.DriveID, b.DeviceID, b.MountPoint, b.Mode, b.Desired, b.Actual, b.LastError, b.UpdatedAt.UTC(), b.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) GetBinding(userID, id string) (*Binding, error) {
	row := p.db.QueryRow(
		`SELECT id,user_id,drive_id,device_id,mount_point,mode,desired,actual,last_error,updated_at,created_at
		 FROM bindings WHERE id=$1 AND user_id=$2`, id, userID,
	)
	var b Binding
	if err := row.Scan(&b.ID, &b.UserID, &b.DriveID, &b.DeviceID, &b.MountPoint, &b.Mode, &b.Desired, &b.Actual, &b.LastError, &b.UpdatedAt, &b.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("binding not found")
		}
		return nil, err
	}
	return &b, nil
}

func (p *Postgres) ListBindings(userID, deviceID string) ([]*Binding, error) {
	var rows *sql.Rows
	var err error
	if deviceID == "" {
		rows, err = p.db.Query(
			`SELECT id,user_id,drive_id,device_id,mount_point,mode,desired,actual,last_error,updated_at,created_at FROM bindings WHERE user_id=$1`, userID,
		)
	} else {
		rows, err = p.db.Query(
			`SELECT id,user_id,drive_id,device_id,mount_point,mode,desired,actual,last_error,updated_at,created_at FROM bindings WHERE user_id=$1 AND device_id=$2`,
			userID, deviceID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Binding
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.ID, &b.UserID, &b.DriveID, &b.DeviceID, &b.MountPoint, &b.Mode, &b.Desired, &b.Actual, &b.LastError, &b.UpdatedAt, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateBinding(b *Binding) error {
	res, err := p.db.Exec(
		`UPDATE bindings SET drive_id=$1,device_id=$2,mount_point=$3,mode=$4,desired=$5,actual=$6,last_error=$7,updated_at=$8 WHERE id=$9 AND user_id=$10`,
		b.DriveID, b.DeviceID, b.MountPoint, b.Mode, b.Desired, b.Actual, b.LastError, b.UpdatedAt.UTC(), b.ID, b.UserID,
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

func (p *Postgres) UpsertDevice(d *Device) error {
	_, err := p.db.Exec(
		`INSERT INTO devices (id,user_id,name,last_seen) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, last_seen=EXCLUDED.last_seen
		 WHERE devices.user_id=EXCLUDED.user_id`,
		d.ID, d.UserID, d.Name, d.LastSeen.UTC(),
	)
	return err
}

func (p *Postgres) GetDevice(userID, id string) (*Device, error) {
	row := p.db.QueryRow(`SELECT id,user_id,name,last_seen FROM devices WHERE id=$1 AND user_id=$2`, id, userID)
	var d Device
	if err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.LastSeen); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("device not found")
		}
		return nil, err
	}
	return &d, nil
}

func (p *Postgres) ListDevices(userID string) ([]*Device, error) {
	rows, err := p.db.Query(`SELECT id,user_id,name,last_seen FROM devices WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

const jobSelectColsPG = `id,user_id,drive_id,binding_id,mode,command_json,status,region_hint,note,
		 COALESCE(agent_id,''), COALESCE(claimed_by_agent_id,''), created_at, updated_at`

func (p *Postgres) CreateJob(j *Job) error {
	_, err := p.db.Exec(
		`INSERT INTO jobs (id,user_id,drive_id,binding_id,mode,command_json,status,region_hint,note,agent_id,claimed_by_agent_id,created_at,updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		j.ID, j.UserID, j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
		j.AgentID, j.ClaimedByAgentID, j.CreatedAt.UTC(), j.UpdatedAt.UTC(),
	)
	return err
}

func (p *Postgres) GetJob(userID, id string) (*Job, error) {
	row := p.db.QueryRow(
		`SELECT `+jobSelectColsPG+` FROM jobs WHERE id=$1 AND user_id=$2`,
		id, userID,
	)
	return scanJobPG(row)
}

func (p *Postgres) ListJobs(userID string) ([]*Job, error) {
	rows, err := p.db.Query(
		`SELECT `+jobSelectColsPG+` FROM jobs WHERE user_id=$1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobRowsPG(rows)
}

func (p *Postgres) ListPendingJobs(userID string) ([]*Job, error) {
	rows, err := p.db.Query(
		`SELECT `+jobSelectColsPG+` FROM jobs
		 WHERE user_id=$1 AND status IN ('pending','dispatched') ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobRowsPG(rows)
}

// ClaimPendingJob atomically claims via UPDATE ... WHERE status still claimable RETURNING.
func (p *Postgres) ClaimPendingJob(userID, id, claimedByAgentID string) (*Job, error) {
	now := time.Now().UTC()
	row := p.db.QueryRow(
		`UPDATE jobs SET status='running', claimed_by_agent_id=$1, updated_at=$2
		 WHERE id=$3 AND user_id=$4 AND status IN ('pending','dispatched')
		 RETURNING `+jobSelectColsPG,
		claimedByAgentID, now, id, userID,
	)
	j, err := scanJobPG(row)
	if err != nil {
		cur, gerr := p.GetJob(userID, id)
		if gerr != nil {
			return nil, fmt.Errorf("job not found")
		}
		return nil, fmt.Errorf("job not claimable: %s", cur.Status)
	}
	return j, nil
}

func (p *Postgres) UpdateJob(j *Job) error {
	res, err := p.db.Exec(
		`UPDATE jobs SET drive_id=$1,binding_id=$2,mode=$3,command_json=$4,status=$5,region_hint=$6,note=$7,
		 agent_id=$8,claimed_by_agent_id=$9,updated_at=$10 WHERE id=$11 AND user_id=$12`,
		j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
		j.AgentID, j.ClaimedByAgentID, j.UpdatedAt.UTC(), j.ID, j.UserID,
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

func (p *Postgres) CreateSnapshot(sn *Snapshot) error {
	_, err := p.db.Exec(
		`INSERT INTO snapshots (id, user_id, drive_id, agent_id, label, note, payload_json, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		sn.ID, sn.UserID, sn.DriveID, sn.AgentID, sn.Label, sn.Note, string(sn.PayloadJSON), sn.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) GetSnapshot(userID, driveID, id string) (*Snapshot, error) {
	row := p.db.QueryRow(
		`SELECT id, user_id, drive_id, COALESCE(agent_id,''), COALESCE(label,''), COALESCE(note,''), payload_json, created_at
		 FROM snapshots WHERE id=$1 AND user_id=$2 AND drive_id=$3`,
		id, userID, driveID,
	)
	return scanSnapshotPG(row)
}

func (p *Postgres) ListSnapshots(userID, driveID string, limit int) ([]*Snapshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := p.db.Query(
		`SELECT id, user_id, drive_id, COALESCE(agent_id,''), COALESCE(label,''), COALESCE(note,''), payload_json, created_at
		 FROM snapshots WHERE user_id=$1 AND drive_id=$2 ORDER BY created_at DESC LIMIT $3`,
		userID, driveID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Snapshot
	for rows.Next() {
		sn, err := scanSnapshotPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteSnapshot(userID, driveID, id string) error {
	res, err := p.db.Exec(`DELETE FROM snapshots WHERE id=$1 AND user_id=$2 AND drive_id=$3`, id, userID, driveID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("snapshot not found")
	}
	return nil
}

func scanSnapshotPG(row interface{ Scan(dest ...any) error }) (*Snapshot, error) {
	var sn Snapshot
	var payload string
	if err := row.Scan(&sn.ID, &sn.UserID, &sn.DriveID, &sn.AgentID, &sn.Label, &sn.Note, &payload, &sn.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("snapshot not found")
		}
		return nil, err
	}
	sn.PayloadJSON = []byte(payload)
	return &sn, nil
}

func scanJobPG(row scannable) (*Job, error) {
	var j Job
	var cmd string
	if err := row.Scan(
		&j.ID, &j.UserID, &j.DriveID, &j.BindingID, &j.Mode, &cmd, &j.Status, &j.RegionHint, &j.Note,
		&j.AgentID, &j.ClaimedByAgentID, &j.CreatedAt, &j.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job not found")
		}
		return nil, err
	}
	j.CommandJSON = []byte(cmd)
	return &j, nil
}

func scanJobRowsPG(rows *sql.Rows) ([]*Job, error) {
	var out []*Job
	for rows.Next() {
		j, err := scanJobPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// IsPostgresDSN reports whether path is a postgres URL.
func IsPostgresDSN(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://")
}
