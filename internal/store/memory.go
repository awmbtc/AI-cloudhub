package store

import (
	"fmt"
	"sync"
	"time"
)

// Memory is an in-memory Store (AI_CLOUDHUB_DB=memory).
type Memory struct {
	mu         sync.RWMutex
	users      map[string]*User     // username -> user
	providers  map[string]*Provider // id -> provider
	drives     map[string]*Drive
	bindings   map[string]*Binding
	devices    map[string]*Device // id -> device
	jobs       map[string]*Job
	audits     []*AuditEvent
	revokedJTI map[string]time.Time // jti -> expiresAt
	refresh    map[string]*RefreshToken // id -> token
	agents     map[string]*Agent        // id -> agent
	snapshots  map[string]*Snapshot     // id -> snapshot
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		users:      make(map[string]*User),
		providers:  make(map[string]*Provider),
		drives:     make(map[string]*Drive),
		bindings:   make(map[string]*Binding),
		devices:    make(map[string]*Device),
		jobs:       make(map[string]*Job),
		audits:     nil,
		revokedJTI: make(map[string]time.Time),
		refresh:    make(map[string]*RefreshToken),
		agents:     make(map[string]*Agent),
		snapshots:  make(map[string]*Snapshot),
	}
}

func (m *Memory) Close() error { return nil }

func (m *Memory) CreateUser(u *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.Username]; ok {
		return fmt.Errorf("user exists")
	}
	cp := *u
	if cp.Role == "" {
		cp.Role = "user"
	}
	m.users[u.Username] = &cp
	return nil
}

func (m *Memory) GetUserByUsername(username string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[username]
	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	cp := *u
	if cp.Role == "" {
		cp.Role = "user"
	}
	return &cp, nil
}

func (m *Memory) GetUserByID(id string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.ID == id {
			cp := *u
			if cp.Role == "" {
				cp.Role = "user"
			}
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (m *Memory) CountUsers() (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users), nil
}

func (m *Memory) UpdateUserRole(userID, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == userID {
			u.Role = role
			return nil
		}
	}
	return fmt.Errorf("user not found")
}

func (m *Memory) Ping() error { return nil }

func (m *Memory) ListUsers() ([]*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		cp := *u
		cp.Password = "" // never return password
		if cp.Role == "" {
			cp.Role = "user"
		}
		out = append(out, &cp)
	}
	return out, nil
}

func (m *Memory) AppendAudit(e *AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *e
	m.audits = append(m.audits, &cp)
	if len(m.audits) > 1000 {
		m.audits = m.audits[len(m.audits)-1000:]
	}
	return nil
}

func (m *Memory) ListAudit(f AuditFilter) ([]*AuditEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Collect matching events from newest (slice is append-order oldest→newest).
	var matched []*AuditEvent
	for i := len(m.audits) - 1; i >= 0; i-- {
		e := m.audits[i]
		if f.UserID != "" && e.UserID != f.UserID {
			continue
		}
		if f.AgentID != "" && e.AgentID != f.AgentID {
			continue
		}
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		cp := *e
		matched = append(matched, &cp)
		if len(matched) >= limit {
			break
		}
	}
	return matched, nil
}

func (m *Memory) BumpTokenVersion(userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == userID {
			u.TokenVersion++
			return u.TokenVersion, nil
		}
	}
	return 0, fmt.Errorf("user not found")
}

func (m *Memory) RevokeJTI(jti string, expiresAt time.Time) error {
	if jti == "" {
		return fmt.Errorf("jti required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// prune expired
	now := time.Now()
	for k, exp := range m.revokedJTI {
		if now.After(exp) {
			delete(m.revokedJTI, k)
		}
	}
	m.revokedJTI[jti] = expiresAt
	return nil
}

func (m *Memory) IsJTIRevoked(jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.revokedJTI[jti]
	if !ok {
		return false, nil
	}
	if time.Now().After(exp) {
		delete(m.revokedJTI, jti)
		return false, nil
	}
	return true, nil
}

func (m *Memory) CreateRefreshToken(t *RefreshToken) error {
	if t == nil || t.ID == "" || t.TokenHash == "" {
		return fmt.Errorf("refresh token incomplete")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *t
	m.refresh[t.ID] = &cp
	return nil
}

func (m *Memory) GetRefreshTokenByHash(tokenHash string) (*RefreshToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now()
	for _, t := range m.refresh {
		if t.TokenHash != tokenHash {
			continue
		}
		if t.Revoked || now.After(t.ExpiresAt) {
			return nil, fmt.Errorf("refresh token invalid")
		}
		cp := *t
		return &cp, nil
	}
	return nil, fmt.Errorf("refresh token not found")
}

func (m *Memory) RevokeRefreshToken(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.refresh[id]
	if !ok {
		return fmt.Errorf("refresh token not found")
	}
	t.Revoked = true
	return nil
}

func (m *Memory) RevokeRefreshTokensForUser(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.refresh {
		if t.UserID == userID {
			t.Revoked = true
		}
	}
	return nil
}

func cloneAgent(a *Agent) *Agent {
	cp := *a
	if a.DefaultScopes != nil {
		cp.DefaultScopes = append([]string(nil), a.DefaultScopes...)
	}
	if a.AllowedDriveIDs != nil {
		cp.AllowedDriveIDs = append([]string(nil), a.AllowedDriveIDs...)
	}
	if a.ReadPrefixes != nil {
		cp.ReadPrefixes = append([]string(nil), a.ReadPrefixes...)
	}
	if a.WritePrefixes != nil {
		cp.WritePrefixes = append([]string(nil), a.WritePrefixes...)
	}
	return &cp
}

func (m *Memory) CreateAgent(a *Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[a.ID] = cloneAgent(a)
	return nil
}

func (m *Memory) GetAgent(ownerUserID, id string) (*Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.agents[id]
	if !ok || a.OwnerUserID != ownerUserID {
		return nil, fmt.Errorf("agent not found")
	}
	return cloneAgent(a), nil
}

func (m *Memory) GetAgentByID(id string) (*Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent not found")
	}
	return cloneAgent(a), nil
}

func (m *Memory) ListAgents(ownerUserID string) ([]*Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Agent
	for _, a := range m.agents {
		if a.OwnerUserID != ownerUserID {
			continue
		}
		out = append(out, cloneAgent(a))
	}
	return out, nil
}

func (m *Memory) UpdateAgent(a *Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.agents[a.ID]
	if !ok || cur.OwnerUserID != a.OwnerUserID {
		return fmt.Errorf("agent not found")
	}
	m.agents[a.ID] = cloneAgent(a)
	return nil
}

func (m *Memory) DeleteAgent(ownerUserID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	if !ok || a.OwnerUserID != ownerUserID {
		return fmt.Errorf("agent not found")
	}
	delete(m.agents, id)
	return nil
}

func (m *Memory) UpdateUserPassword(userID, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == userID {
			u.Password = hash
			return nil
		}
	}
	return fmt.Errorf("user not found")
}

func (m *Memory) CreateProvider(p *Provider) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := cloneProvider(p)
	m.providers[p.ID] = cp
	return nil
}

func (m *Memory) GetProvider(userID, id string) (*Provider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[id]
	if !ok || p.UserID != userID {
		return nil, fmt.Errorf("provider not found")
	}
	return cloneProvider(p), nil
}

func (m *Memory) ListProviders(userID string) ([]*Provider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Provider
	for _, p := range m.providers {
		if p.UserID == userID {
			out = append(out, cloneProvider(p))
		}
	}
	return out, nil
}

func (m *Memory) DeleteProvider(userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.providers[id]
	if !ok || p.UserID != userID {
		return fmt.Errorf("provider not found")
	}
	delete(m.providers, id)
	return nil
}

func (m *Memory) CreateDrive(d *Drive) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *d
	m.drives[d.ID] = &cp
	return nil
}

func (m *Memory) GetDrive(userID, id string) (*Drive, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.drives[id]
	if !ok || d.UserID != userID {
		return nil, fmt.Errorf("drive not found")
	}
	cp := *d
	return &cp, nil
}

func (m *Memory) ListDrives(userID string) ([]*Drive, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Drive
	for _, d := range m.drives {
		if d.UserID == userID {
			cp := *d
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *Memory) UpdateDrive(d *Drive) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.drives[d.ID]
	if !ok || cur.UserID != d.UserID {
		return fmt.Errorf("drive not found")
	}
	// preserve identity fields
	d.ProviderID = cur.ProviderID
	d.Bucket = cur.Bucket
	d.CreatedAt = cur.CreatedAt
	cp := *d
	m.drives[d.ID] = &cp
	return nil
}

func (m *Memory) DeleteDrive(userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drives[id]
	if !ok || d.UserID != userID {
		return fmt.Errorf("drive not found")
	}
	delete(m.drives, id)
	return nil
}

func (m *Memory) CreateBinding(b *Binding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *b
	m.bindings[b.ID] = &cp
	return nil
}

func (m *Memory) GetBinding(userID, id string) (*Binding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.bindings[id]
	if !ok || b.UserID != userID {
		return nil, fmt.Errorf("binding not found")
	}
	cp := *b
	return &cp, nil
}

func (m *Memory) ListBindings(userID, deviceID string) ([]*Binding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Binding
	for _, b := range m.bindings {
		if b.UserID != userID {
			continue
		}
		if deviceID != "" && b.DeviceID != deviceID {
			continue
		}
		cp := *b
		out = append(out, &cp)
	}
	return out, nil
}

func (m *Memory) UpdateBinding(b *Binding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.bindings[b.ID]
	if !ok || cur.UserID != b.UserID {
		return fmt.Errorf("binding not found")
	}
	cp := *b
	m.bindings[b.ID] = &cp
	return nil
}

func (m *Memory) UpsertDevice(d *Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.devices[d.ID]; ok && cur.UserID != d.UserID {
		return fmt.Errorf("device id conflict")
	}
	cp := *d
	m.devices[d.ID] = &cp
	return nil
}

func (m *Memory) GetDevice(userID, id string) (*Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	if !ok || d.UserID != userID {
		return nil, fmt.Errorf("device not found")
	}
	cp := *d
	return &cp, nil
}

func (m *Memory) ListDevices(userID string) ([]*Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Device
	for _, d := range m.devices {
		if d.UserID == userID {
			cp := *d
			out = append(out, &cp)
		}
	}
	return out, nil
}

func cloneProvider(p *Provider) *Provider {
	cp := *p
	if p.CredsJSON != nil {
		cp.CredsJSON = append([]byte(nil), p.CredsJSON...)
	}
	return &cp
}

func cloneJob(j *Job) *Job {
	cp := *j
	if j.CommandJSON != nil {
		cp.CommandJSON = append([]byte(nil), j.CommandJSON...)
	}
	return &cp
}

func (m *Memory) CreateJob(j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = cloneJob(j)
	return nil
}

func (m *Memory) GetJob(userID, id string) (*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok || j.UserID != userID {
		return nil, fmt.Errorf("job not found")
	}
	return cloneJob(j), nil
}

func (m *Memory) ListJobs(userID string) ([]*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Job
	for _, j := range m.jobs {
		if j.UserID == userID {
			out = append(out, cloneJob(j))
		}
	}
	return out, nil
}

func (m *Memory) ListPendingJobs(userID string) ([]*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Job
	for _, j := range m.jobs {
		if j.UserID == userID && (j.Status == "pending" || j.Status == "dispatched") {
			out = append(out, cloneJob(j))
		}
	}
	return out, nil
}

// ClaimPendingJob claims under the write mutex so only one caller wins.
func (m *Memory) ClaimPendingJob(userID, id string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.UserID != userID {
		return nil, fmt.Errorf("job not found")
	}
	if j.Status != "pending" && j.Status != "dispatched" {
		return nil, fmt.Errorf("job not claimable: %s", j.Status)
	}
	j.Status = "running"
	j.UpdatedAt = time.Now().UTC()
	return cloneJob(j), nil
}

func (m *Memory) UpdateJob(j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.jobs[j.ID]
	if !ok || cur.UserID != j.UserID {
		return fmt.Errorf("job not found")
	}
	m.jobs[j.ID] = cloneJob(j)
	return nil
}

func (m *Memory) CreateSnapshot(s *Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	if s.PayloadJSON != nil {
		cp.PayloadJSON = append([]byte(nil), s.PayloadJSON...)
	}
	m.snapshots[s.ID] = &cp
	return nil
}

func (m *Memory) GetSnapshot(userID, driveID, id string) (*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.snapshots[id]
	if !ok || s.UserID != userID || s.DriveID != driveID {
		return nil, fmt.Errorf("snapshot not found")
	}
	cp := *s
	if s.PayloadJSON != nil {
		cp.PayloadJSON = append([]byte(nil), s.PayloadJSON...)
	}
	return &cp, nil
}

func (m *Memory) ListSnapshots(userID, driveID string, limit int) ([]*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var all []*Snapshot
	for _, s := range m.snapshots {
		if s.UserID == userID && s.DriveID == driveID {
			cp := *s
			if s.PayloadJSON != nil {
				cp.PayloadJSON = append([]byte(nil), s.PayloadJSON...)
			}
			all = append(all, &cp)
		}
	}
	// newest first
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].CreatedAt.After(all[i].CreatedAt) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (m *Memory) DeleteSnapshot(userID, driveID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[id]
	if !ok || s.UserID != userID || s.DriveID != driveID {
		return fmt.Errorf("snapshot not found")
	}
	delete(m.snapshots, id)
	return nil
}
