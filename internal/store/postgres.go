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
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ
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
  public BOOLEAN NOT NULL DEFAULT TRUE,
  price_cents BIGINT NOT NULL DEFAULT 0,
  currency TEXT,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_market_pub ON marketplace_items(publisher_user_id);
CREATE TABLE IF NOT EXISTS lineage_events (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, actor_id TEXT, action TEXT NOT NULL,
  entity TEXT NOT NULL, parent TEXT, detail TEXT, created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lineage_user ON lineage_events(user_id, created_at);
CREATE TABLE IF NOT EXISTS graph_edges (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, subject TEXT NOT NULL, relation TEXT NOT NULL,
  object TEXT NOT NULL, meta_json TEXT, created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS purchases (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, item_id TEXT NOT NULL, amount_cents BIGINT NOT NULL,
  currency TEXT, status TEXT NOT NULL, provider TEXT, provider_ref TEXT, created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS connectors (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, type TEXT NOT NULL, name TEXT NOT NULL,
  config_json TEXT, status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS job_webhook_outbox (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  event TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL,
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  delivered_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_webhook_outbox_due ON job_webhook_outbox(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_webhook_outbox_job ON job_webhook_outbox(job_id);
CREATE INDEX IF NOT EXISTS idx_webhook_outbox_user ON job_webhook_outbox(user_id);
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
	_, _ = p.db.Exec(`ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS embedding_json TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE marketplace_items ADD COLUMN IF NOT EXISTS price_cents BIGINT NOT NULL DEFAULT 0`)
	_, _ = p.db.Exec(`ALTER TABLE marketplace_items ADD COLUMN IF NOT EXISTS currency TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS connector_id TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS exit_code INTEGER`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS duration_ms BIGINT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS stdout TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS stderr TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS timeout_sec INTEGER`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS stdout_truncated BOOLEAN`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS stderr_truncated BOOLEAN`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS attempt_count INTEGER`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS max_attempts INTEGER`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS priority INTEGER`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS claimed_by_runner_id TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS labels_json TEXT`)
	_, _ = p.db.Exec(`ALTER TABLE jobs ADD COLUMN IF NOT EXISTS idempotency_key TEXT`)
	_, _ = p.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_user_idempotency ON jobs(user_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key <> ''`)
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
		 COALESCE(agent_id,''), COALESCE(claimed_by_agent_id,''), COALESCE(connector_id,''),
		 COALESCE(claimed_by_runner_id,''), COALESCE(priority,0), COALESCE(labels_json,''),
		 COALESCE(idempotency_key,''),
		 exit_code, COALESCE(duration_ms,0), heartbeat_at, claimed_at, COALESCE(timeout_sec,0),
		 COALESCE(attempt_count,0), COALESCE(max_attempts,0),
		 COALESCE(stdout,''), COALESCE(stderr,''), COALESCE(stdout_truncated,false), COALESCE(stderr_truncated,false),
		 created_at, updated_at`

func (p *Postgres) CreateJob(j *Job) error {
	var hb, ca interface{}
	if !j.HeartbeatAt.IsZero() {
		hb = j.HeartbeatAt.UTC()
	}
	if !j.ClaimedAt.IsZero() {
		ca = j.ClaimedAt.UTC()
	}
	_, err := p.db.Exec(
		`INSERT INTO jobs (id,user_id,drive_id,binding_id,mode,command_json,status,region_hint,note,agent_id,claimed_by_agent_id,connector_id,claimed_by_runner_id,priority,labels_json,idempotency_key,exit_code,duration_ms,heartbeat_at,claimed_at,timeout_sec,attempt_count,max_attempts,stdout,stderr,stdout_truncated,stderr_truncated,created_at,updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)`,
		j.ID, j.UserID, j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
		j.AgentID, j.ClaimedByAgentID, j.ConnectorID, j.ClaimedByRunnerID, j.Priority, string(j.LabelsJSON), j.IdempotencyKey,
		nullInt(j.ExitCode), j.DurationMs, hb, ca, j.TimeoutSec,
		j.AttemptCount, j.MaxAttempts, j.Stdout, j.Stderr, j.StdoutTruncated, j.StderrTruncated,
		j.CreatedAt.UTC(), j.UpdatedAt.UTC(),
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

func (p *Postgres) GetJobByID(id string) (*Job, error) {
	row := p.db.QueryRow(
		`SELECT `+jobSelectColsPG+` FROM jobs WHERE id=$1`, id,
	)
	return scanJobPG(row)
}

func (p *Postgres) ListJobsAdmin(f AdminJobFilter) ([]*Job, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	// 501 allows service-layer limit+1 with user max 500
	if limit > 501 {
		limit = 501
	}
	q := `SELECT ` + jobSelectColsPG + ` FROM jobs WHERE 1=1`
	var args []interface{}
	n := 1
	if f.UserID != "" {
		q += fmt.Sprintf(` AND user_id=$%d`, n)
		args = append(args, f.UserID)
		n++
	}
	if f.Status != "" {
		q += fmt.Sprintf(` AND status=$%d`, n)
		args = append(args, f.Status)
		n++
	}
	if !f.CursorCreated.IsZero() && f.CursorID != "" {
		// keyset: (created_at, id) < cursor in DESC order
		ca := f.CursorCreated.UTC()
		q += fmt.Sprintf(` AND (created_at < $%d OR (created_at = $%d AND id < $%d))`, n, n+1, n+2)
		args = append(args, ca, ca, f.CursorID)
		n += 3
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, n)
	args = append(args, limit)
	rows, err := p.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobRowsPG(rows)
}

func (p *Postgres) GetJobByIdempotencyKey(userID, key string) (*Job, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("job not found")
	}
	row := p.db.QueryRow(
		`SELECT `+jobSelectColsPG+` FROM jobs WHERE user_id=$1 AND idempotency_key=$2`,
		userID, key,
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

func (p *Postgres) CountJobsByStatus(userID string) (*JobStatusCounts, error) {
	var (
		rows *sql.Rows
		err  error
	)
	userID = strings.TrimSpace(userID)
	if userID != "" {
		rows, err = p.db.Query(`SELECT status, COUNT(*)::int FROM jobs WHERE user_id=$1 GROUP BY status`, userID)
	} else {
		rows, err = p.db.Query(`SELECT status, COUNT(*)::int FROM jobs GROUP BY status`)
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

func (p *Postgres) ListPendingJobs(userID string) ([]*Job, error) {
	rows, err := p.db.Query(
		`SELECT `+jobSelectColsPG+` FROM jobs
		 WHERE user_id=$1 AND status IN ('pending','dispatched')
		 ORDER BY COALESCE(priority,0) DESC, created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobRowsPG(rows)
}

// ClaimPendingJob atomically claims via UPDATE ... WHERE status still claimable RETURNING.
func (p *Postgres) ClaimPendingJob(userID, id, claimedByAgentID, claimedByRunnerID string) (*Job, error) {
	now := time.Now().UTC()
	row := p.db.QueryRow(
		`UPDATE jobs SET status='running', claimed_by_agent_id=$1, claimed_by_runner_id=$2,
		 heartbeat_at=$3, claimed_at=$4,
		 attempt_count=COALESCE(attempt_count,0)+1, updated_at=$5
		 WHERE id=$6 AND user_id=$7 AND status IN ('pending','dispatched')
		 RETURNING `+jobSelectColsPG,
		claimedByAgentID, claimedByRunnerID, now, now, now, id, userID,
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
	var hb, ca interface{}
	if !j.HeartbeatAt.IsZero() {
		hb = j.HeartbeatAt.UTC()
	}
	if !j.ClaimedAt.IsZero() {
		ca = j.ClaimedAt.UTC()
	}
	res, err := p.db.Exec(
		`UPDATE jobs SET drive_id=$1,binding_id=$2,mode=$3,command_json=$4,status=$5,region_hint=$6,note=$7,
		 agent_id=$8,claimed_by_agent_id=$9,connector_id=$10,claimed_by_runner_id=$11,priority=$12,labels_json=$13,
		 idempotency_key=$14,
		 exit_code=$15,duration_ms=$16,heartbeat_at=$17,
		 claimed_at=$18,timeout_sec=$19,attempt_count=$20,max_attempts=$21,stdout=$22,stderr=$23,
		 stdout_truncated=$24,stderr_truncated=$25,updated_at=$26
		 WHERE id=$27 AND user_id=$28`,
		j.DriveID, j.BindingID, j.Mode, string(j.CommandJSON), j.Status, j.RegionHint, j.Note,
		j.AgentID, j.ClaimedByAgentID, j.ConnectorID, j.ClaimedByRunnerID, j.Priority, string(j.LabelsJSON),
		j.IdempotencyKey,
		nullInt(j.ExitCode), j.DurationMs, hb,
		ca, j.TimeoutSec, j.AttemptCount, j.MaxAttempts, j.Stdout, j.Stderr,
		j.StdoutTruncated, j.StderrTruncated, j.UpdatedAt.UTC(), j.ID, j.UserID,
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
	var exitCode sql.NullInt64
	var hb, ca sql.NullTime
	var labels, idem string
	if err := row.Scan(
		&j.ID, &j.UserID, &j.DriveID, &j.BindingID, &j.Mode, &cmd, &j.Status, &j.RegionHint, &j.Note,
		&j.AgentID, &j.ClaimedByAgentID, &j.ConnectorID, &j.ClaimedByRunnerID, &j.Priority, &labels, &idem,
		&exitCode, &j.DurationMs, &hb, &ca, &j.TimeoutSec,
		&j.AttemptCount, &j.MaxAttempts, &j.Stdout, &j.Stderr, &j.StdoutTruncated, &j.StderrTruncated,
		&j.CreatedAt, &j.UpdatedAt,
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
	if strings.TrimSpace(labels) != "" {
		j.LabelsJSON = []byte(labels)
	}
	j.IdempotencyKey = idem
	if hb.Valid {
		j.HeartbeatAt = hb.Time.UTC()
	}
	if ca.Valid {
		j.ClaimedAt = ca.Time.UTC()
	}
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

func (p *Postgres) CreateMemory(e *MemoryEntry) error {
	var exp interface{}
	if !e.ExpiresAt.IsZero() {
		exp = e.ExpiresAt.UTC()
	}
	_, err := p.db.Exec(
		`INSERT INTO memory_entries (id, user_id, agent_id, drive_id, layer, key, content, meta_json, embedding_json, created_at, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		e.ID, e.UserID, e.AgentID, e.DriveID, e.Layer, e.Key, e.Content, string(e.MetaJSON), string(e.EmbeddingJSON), e.CreatedAt.UTC(), exp,
	)
	return err
}

func (p *Postgres) GetMemory(userID, id string) (*MemoryEntry, error) {
	row := p.db.QueryRow(
		`SELECT id, user_id, agent_id, drive_id, layer, key, content, meta_json, COALESCE(embedding_json,''), created_at, expires_at
		 FROM memory_entries WHERE id=$1 AND user_id=$2`, id, userID)
	return scanMemoryPG(row)
}

func (p *Postgres) ListMemory(f MemoryFilter) ([]*MemoryEntry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := p.db.Query(
		`SELECT id, user_id, agent_id, drive_id, layer, key, content, meta_json, COALESCE(embedding_json,''), created_at, expires_at
		 FROM memory_entries WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, f.UserID, f.Limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MemoryEntry
	now := time.Now()
	for rows.Next() {
		e, err := scanMemoryPG(rows)
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

func (p *Postgres) DeleteMemory(userID, id string) error {
	res, err := p.db.Exec(`DELETE FROM memory_entries WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory not found")
	}
	return nil
}

func scanMemoryPG(row interface{ Scan(dest ...any) error }) (*MemoryEntry, error) {
	var e MemoryEntry
	var meta, emb string
	var exp *time.Time
	if err := row.Scan(&e.ID, &e.UserID, &e.AgentID, &e.DriveID, &e.Layer, &e.Key, &e.Content, &meta, &emb, &e.CreatedAt, &exp); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("memory not found")
		}
		return nil, err
	}
	e.MetaJSON = []byte(meta)
	if emb != "" {
		e.EmbeddingJSON = []byte(emb)
	}
	if exp != nil {
		e.ExpiresAt = *exp
	}
	return &e, nil
}

func (p *Postgres) CreateMarketplaceItem(m *MarketplaceItem) error {
	_, err := p.db.Exec(
		`INSERT INTO marketplace_items (id, publisher_user_id, name, description, kind, version, payload_json, public, price_cents, currency, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		m.ID, m.PublisherUserID, m.Name, m.Description, m.Kind, m.Version, string(m.PayloadJSON), m.Public, m.PriceCents, m.Currency, m.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) GetMarketplaceItem(id string) (*MarketplaceItem, error) {
	row := p.db.QueryRow(
		`SELECT id, publisher_user_id, name, description, kind, version, payload_json, public, COALESCE(price_cents,0), COALESCE(currency,''), created_at
		 FROM marketplace_items WHERE id=$1`, id)
	return scanMarketPG(row)
}

func (p *Postgres) ListMarketplaceItems(publicOnly bool, publisherUserID string) ([]*MarketplaceItem, error) {
	rows, err := p.db.Query(
		`SELECT id, publisher_user_id, name, description, kind, version, payload_json, public, COALESCE(price_cents,0), COALESCE(currency,''), created_at
		 FROM marketplace_items ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MarketplaceItem
	for rows.Next() {
		it, err := scanMarketPG(rows)
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

func (p *Postgres) DeleteMarketplaceItem(publisherUserID, id string) error {
	res, err := p.db.Exec(`DELETE FROM marketplace_items WHERE id=$1 AND publisher_user_id=$2`, id, publisherUserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("marketplace item not found")
	}
	return nil
}

func scanMarketPG(row interface{ Scan(dest ...any) error }) (*MarketplaceItem, error) {
	var m MarketplaceItem
	var payload string
	if err := row.Scan(&m.ID, &m.PublisherUserID, &m.Name, &m.Description, &m.Kind, &m.Version, &payload, &m.Public, &m.PriceCents, &m.Currency, &m.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("marketplace item not found")
		}
		return nil, err
	}
	m.PayloadJSON = []byte(payload)
	return &m, nil
}

func (p *Postgres) AppendLineage(e *LineageEvent) error {
	_, err := p.db.Exec(
		`INSERT INTO lineage_events (id, user_id, actor_id, action, entity, parent, detail, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.ID, e.UserID, e.ActorID, e.Action, e.Entity, e.Parent, e.Detail, e.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) ListLineage(userID, entity string, limit int) ([]*LineageEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.db.Query(
		`SELECT id, user_id, actor_id, action, entity, parent, detail, created_at FROM lineage_events WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`,
		userID, limit*2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LineageEvent
	for rows.Next() {
		var e LineageEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.ActorID, &e.Action, &e.Entity, &e.Parent, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
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

func (p *Postgres) UpsertGraphEdge(e *GraphEdge) error {
	_, err := p.db.Exec(
		`INSERT INTO graph_edges (id, user_id, subject, relation, object, meta_json, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (id) DO UPDATE SET subject=EXCLUDED.subject, relation=EXCLUDED.relation, object=EXCLUDED.object, meta_json=EXCLUDED.meta_json`,
		e.ID, e.UserID, e.Subject, e.Relation, e.Object, string(e.MetaJSON), e.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) ListGraphEdges(userID, subject, object string, limit int) ([]*GraphEdge, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.db.Query(
		`SELECT id, user_id, subject, relation, object, meta_json, created_at FROM graph_edges WHERE user_id=$1 LIMIT $2`, userID, limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*GraphEdge
	for rows.Next() {
		var e GraphEdge
		var meta string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Subject, &e.Relation, &e.Object, &meta, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.MetaJSON = []byte(meta)
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

func (p *Postgres) CreatePurchase(pu *Purchase) error {
	_, err := p.db.Exec(
		`INSERT INTO purchases (id, user_id, item_id, amount_cents, currency, status, provider, provider_ref, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		pu.ID, pu.UserID, pu.ItemID, pu.AmountCents, pu.Currency, pu.Status, pu.Provider, pu.ProviderRef, pu.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) GetPurchase(userID, id string) (*Purchase, error) {
	row := p.db.QueryRow(
		`SELECT id, user_id, item_id, amount_cents, currency, status, provider, provider_ref, created_at FROM purchases WHERE id=$1 AND user_id=$2`, id, userID)
	var pu Purchase
	if err := row.Scan(&pu.ID, &pu.UserID, &pu.ItemID, &pu.AmountCents, &pu.Currency, &pu.Status, &pu.Provider, &pu.ProviderRef, &pu.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("purchase not found")
		}
		return nil, err
	}
	return &pu, nil
}

func (p *Postgres) ListPurchases(userID string, limit int) ([]*Purchase, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := p.db.Query(
		`SELECT id, user_id, item_id, amount_cents, currency, status, provider, provider_ref, created_at FROM purchases WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Purchase
	for rows.Next() {
		var pu Purchase
		if err := rows.Scan(&pu.ID, &pu.UserID, &pu.ItemID, &pu.AmountCents, &pu.Currency, &pu.Status, &pu.Provider, &pu.ProviderRef, &pu.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &pu)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdatePurchase(pu *Purchase) error {
	res, err := p.db.Exec(`UPDATE purchases SET status=$1, provider=$2, provider_ref=$3 WHERE id=$4 AND user_id=$5`,
		pu.Status, pu.Provider, pu.ProviderRef, pu.ID, pu.UserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("purchase not found")
	}
	return nil
}

func (p *Postgres) CreateConnector(c *ConnectorBinding) error {
	_, err := p.db.Exec(
		`INSERT INTO connectors (id, user_id, type, name, config_json, status, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.ID, c.UserID, c.Type, c.Name, string(c.ConfigJSON), c.Status, c.CreatedAt.UTC(),
	)
	return err
}

func (p *Postgres) GetConnector(userID, id string) (*ConnectorBinding, error) {
	row := p.db.QueryRow(`SELECT id, user_id, type, name, config_json, status, created_at FROM connectors WHERE id=$1 AND user_id=$2`, id, userID)
	var c ConnectorBinding
	var cfg string
	if err := row.Scan(&c.ID, &c.UserID, &c.Type, &c.Name, &cfg, &c.Status, &c.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("connector not found")
		}
		return nil, err
	}
	c.ConfigJSON = []byte(cfg)
	return &c, nil
}

func (p *Postgres) ListConnectors(userID string) ([]*ConnectorBinding, error) {
	rows, err := p.db.Query(`SELECT id, user_id, type, name, config_json, status, created_at FROM connectors WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ConnectorBinding
	for rows.Next() {
		var c ConnectorBinding
		var cfg string
		if err := rows.Scan(&c.ID, &c.UserID, &c.Type, &c.Name, &cfg, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.ConfigJSON = []byte(cfg)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteConnector(userID, id string) error {
	res, err := p.db.Exec(`DELETE FROM connectors WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("connector not found")
	}
	return nil
}

func (p *Postgres) EnqueueWebhookOutbox(e *WebhookOutbox) error {
	if e == nil || strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("webhook outbox id required")
	}
	var delivered interface{}
	if !e.DeliveredAt.IsZero() {
		delivered = e.DeliveredAt.UTC()
	}
	_, err := p.db.Exec(
		`INSERT INTO job_webhook_outbox
		 (id, job_id, user_id, event, payload_json, status, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		e.ID, e.JobID, e.UserID, e.Event, string(e.PayloadJSON), e.Status, e.Attempts,
		e.NextAttemptAt.UTC(), e.LastError, e.CreatedAt.UTC(), e.UpdatedAt.UTC(), delivered,
	)
	if err != nil {
		return fmt.Errorf("enqueue webhook outbox: %w", err)
	}
	return nil
}

func (p *Postgres) ListDueWebhookOutbox(now time.Time, limit int) ([]*WebhookOutbox, error) {
	if limit <= 0 {
		limit = 32
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := p.db.Query(
		`SELECT id, job_id, user_id, event, payload_json, status, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at
		 FROM job_webhook_outbox
		 WHERE status = 'pending' AND next_attempt_at <= $1
		 ORDER BY next_attempt_at ASC LIMIT $2`,
		now.UTC(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebhookOutbox
	for rows.Next() {
		e, err := scanWebhookOutboxPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateWebhookOutbox(e *WebhookOutbox) error {
	if e == nil || strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("webhook outbox id required")
	}
	var delivered interface{}
	if !e.DeliveredAt.IsZero() {
		delivered = e.DeliveredAt.UTC()
	}
	res, err := p.db.Exec(
		`UPDATE job_webhook_outbox SET job_id=$1, user_id=$2, event=$3, payload_json=$4, status=$5, attempts=$6,
		 next_attempt_at=$7, last_error=$8, updated_at=$9, delivered_at=$10 WHERE id=$11`,
		e.JobID, e.UserID, e.Event, string(e.PayloadJSON), e.Status, e.Attempts,
		e.NextAttemptAt.UTC(), e.LastError, e.UpdatedAt.UTC(), delivered, e.ID,
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

func scanWebhookOutboxPG(row interface{ Scan(dest ...any) error }) (*WebhookOutbox, error) {
	var e WebhookOutbox
	var payload string
	var lastErr sql.NullString
	var delivered sql.NullTime
	if err := row.Scan(
		&e.ID, &e.JobID, &e.UserID, &e.Event, &payload, &e.Status, &e.Attempts,
		&e.NextAttemptAt, &lastErr, &e.CreatedAt, &e.UpdatedAt, &delivered,
	); err != nil {
		return nil, err
	}
	e.PayloadJSON = []byte(payload)
	if lastErr.Valid {
		e.LastError = lastErr.String
	}
	if delivered.Valid {
		e.DeliveredAt = delivered.Time.UTC()
	}
	e.NextAttemptAt = e.NextAttemptAt.UTC()
	e.CreatedAt = e.CreatedAt.UTC()
	e.UpdatedAt = e.UpdatedAt.UTC()
	return &e, nil
}

func (p *Postgres) GetWebhookOutbox(id string) (*WebhookOutbox, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("webhook outbox not found")
	}
	row := p.db.QueryRow(
		`SELECT id, job_id, user_id, event, payload_json, status, attempts, next_attempt_at, last_error, created_at, updated_at, delivered_at
		 FROM job_webhook_outbox WHERE id=$1`, id,
	)
	e, err := scanWebhookOutboxPG(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("webhook outbox not found")
		}
		return nil, err
	}
	return e, nil
}

func (p *Postgres) ListWebhookOutbox(f WebhookOutboxFilter) ([]*WebhookOutbox, error) {
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
	n := 1
	if strings.TrimSpace(f.Status) != "" {
		q += fmt.Sprintf(` AND status=$%d`, n)
		args = append(args, strings.TrimSpace(f.Status))
		n++
	}
	if strings.TrimSpace(f.JobID) != "" {
		q += fmt.Sprintf(` AND job_id=$%d`, n)
		args = append(args, strings.TrimSpace(f.JobID))
		n++
	}
	if strings.TrimSpace(f.UserID) != "" {
		q += fmt.Sprintf(` AND user_id=$%d`, n)
		args = append(args, strings.TrimSpace(f.UserID))
		n++
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, n)
	args = append(args, limit)
	rows, err := p.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebhookOutbox
	for rows.Next() {
		e, err := scanWebhookOutboxPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) PurgeWebhookOutbox(olderThan time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	cut := olderThan.UTC()
	// CTE delete of oldest matching terminal rows.
	res, err := p.db.Exec(
		`WITH doomed AS (
		   SELECT id FROM job_webhook_outbox
		   WHERE (status = 'delivered' AND COALESCE(delivered_at, updated_at) < $1)
		      OR (status = 'dead' AND updated_at < $1)
		   ORDER BY COALESCE(delivered_at, updated_at) ASC
		   LIMIT $2
		 )
		 DELETE FROM job_webhook_outbox WHERE id IN (SELECT id FROM doomed)`,
		cut, limit,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// IsPostgresDSN reports whether path is a postgres URL.
func IsPostgresDSN(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://")
}
