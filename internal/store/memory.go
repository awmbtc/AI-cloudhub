package store

import (
	"fmt"
	"strings"
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
	memories   map[string]*MemoryEntry  // id -> memory
	market     map[string]*MarketplaceItem
	lineage    []*LineageEvent
	graph      map[string]*GraphEdge
	purchases  map[string]*Purchase
	connectors map[string]*ConnectorBinding
	webhooks   map[string]*WebhookOutbox // id -> outbox
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
		memories:   make(map[string]*MemoryEntry),
		market:     make(map[string]*MarketplaceItem),
		lineage:    nil,
		graph:      make(map[string]*GraphEdge),
		purchases:  make(map[string]*Purchase),
		connectors: make(map[string]*ConnectorBinding),
		webhooks:   make(map[string]*WebhookOutbox),
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
	if j.LabelsJSON != nil {
		cp.LabelsJSON = append([]byte(nil), j.LabelsJSON...)
	}
	if j.ExitCode != nil {
		v := *j.ExitCode
		cp.ExitCode = &v
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

func (m *Memory) GetJobByID(id string) (*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found")
	}
	return cloneJob(j), nil
}

func (m *Memory) ListJobsAdmin(f AdminJobFilter) ([]*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	// 501 allows service-layer limit+1 with user max 500
	if limit > 501 {
		limit = 501
	}
	var out []*Job
	for _, j := range m.jobs {
		if f.UserID != "" && j.UserID != f.UserID {
			continue
		}
		if f.Status != "" && j.Status != f.Status {
			continue
		}
		if !f.CursorCreated.IsZero() && f.CursorID != "" {
			// keyset DESC: keep rows strictly older than (CursorCreated, CursorID)
			if j.CreatedAt.After(f.CursorCreated) {
				continue
			}
			if j.CreatedAt.Equal(f.CursorCreated) && j.ID >= f.CursorID {
				continue
			}
		}
		out = append(out, cloneJob(j))
	}
	// newest first: created_at DESC, id DESC
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].CreatedAt.After(out[i].CreatedAt) ||
				(out[k].CreatedAt.Equal(out[i].CreatedAt) && out[k].ID > out[i].ID) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) GetJobByIdempotencyKey(userID, key string) (*Job, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("job not found")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, j := range m.jobs {
		if j.UserID == userID && j.IdempotencyKey == key {
			return cloneJob(j), nil
		}
	}
	return nil, fmt.Errorf("job not found")
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

func (m *Memory) CountJobsByStatus(userID string) (*JobStatusCounts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var c JobStatusCounts
	for _, j := range m.jobs {
		if userID != "" && j.UserID != userID {
			continue
		}
		c.AddStatus(j.Status)
	}
	return &c, nil
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
func (m *Memory) ClaimPendingJob(userID, id, claimedByAgentID, claimedByRunnerID string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.UserID != userID {
		return nil, fmt.Errorf("job not found")
	}
	if j.Status != "pending" && j.Status != "dispatched" {
		return nil, fmt.Errorf("job not claimable: %s", j.Status)
	}
	now := time.Now().UTC()
	j.Status = "running"
	j.ClaimedByAgentID = claimedByAgentID
	j.ClaimedByRunnerID = claimedByRunnerID
	j.HeartbeatAt = now
	j.ClaimedAt = now
	j.AttemptCount++
	j.UpdatedAt = now
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

func (m *Memory) CreateMemory(e *MemoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e == nil || e.ID == "" {
		return fmt.Errorf("memory id required")
	}
	cp := *e
	if e.MetaJSON != nil {
		cp.MetaJSON = append([]byte(nil), e.MetaJSON...)
	}
	if e.EmbeddingJSON != nil {
		cp.EmbeddingJSON = append([]byte(nil), e.EmbeddingJSON...)
	}
	m.memories[e.ID] = &cp
	return nil
}

func (m *Memory) GetMemory(userID, id string) (*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.memories[id]
	if !ok || e.UserID != userID {
		return nil, fmt.Errorf("memory not found")
	}
	cp := *e
	if e.MetaJSON != nil {
		cp.MetaJSON = append([]byte(nil), e.MetaJSON...)
	}
	if e.EmbeddingJSON != nil {
		cp.EmbeddingJSON = append([]byte(nil), e.EmbeddingJSON...)
	}
	return &cp, nil
}

func (m *Memory) ListMemory(f MemoryFilter) ([]*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	now := time.Now()
	var out []*MemoryEntry
	for _, e := range m.memories {
		if f.UserID != "" && e.UserID != f.UserID {
			continue
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
		cp := *e
		if e.MetaJSON != nil {
			cp.MetaJSON = append([]byte(nil), e.MetaJSON...)
		}
		if e.EmbeddingJSON != nil {
			cp.EmbeddingJSON = append([]byte(nil), e.EmbeddingJSON...)
		}
		out = append(out, &cp)
	}
	// newest first
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *Memory) DeleteMemory(userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.memories[id]
	if !ok || e.UserID != userID {
		return fmt.Errorf("memory not found")
	}
	delete(m.memories, id)
	return nil
}

func (m *Memory) CreateMarketplaceItem(it *MarketplaceItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it == nil || it.ID == "" {
		return fmt.Errorf("marketplace id required")
	}
	cp := *it
	if it.PayloadJSON != nil {
		cp.PayloadJSON = append([]byte(nil), it.PayloadJSON...)
	}
	m.market[it.ID] = &cp
	return nil
}

func (m *Memory) GetMarketplaceItem(id string) (*MarketplaceItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	it, ok := m.market[id]
	if !ok {
		return nil, fmt.Errorf("marketplace item not found")
	}
	cp := *it
	if it.PayloadJSON != nil {
		cp.PayloadJSON = append([]byte(nil), it.PayloadJSON...)
	}
	return &cp, nil
}

func (m *Memory) ListMarketplaceItems(publicOnly bool, publisherUserID string) ([]*MarketplaceItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*MarketplaceItem
	for _, it := range m.market {
		if publicOnly && !it.Public {
			continue
		}
		if publisherUserID != "" && it.PublisherUserID != publisherUserID {
			continue
		}
		cp := *it
		if it.PayloadJSON != nil {
			cp.PayloadJSON = append([]byte(nil), it.PayloadJSON...)
		}
		out = append(out, &cp)
	}
	return out, nil
}

func (m *Memory) DeleteMarketplaceItem(publisherUserID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.market[id]
	if !ok || it.PublisherUserID != publisherUserID {
		return fmt.Errorf("marketplace item not found")
	}
	delete(m.market, id)
	return nil
}

func (m *Memory) AppendLineage(e *LineageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e == nil || e.ID == "" {
		return fmt.Errorf("lineage id required")
	}
	cp := *e
	m.lineage = append(m.lineage, &cp)
	return nil
}

func (m *Memory) ListLineage(userID, entity string, limit int) ([]*LineageEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []*LineageEvent
	for i := len(m.lineage) - 1; i >= 0; i-- {
		e := m.lineage[i]
		if e.UserID != userID {
			continue
		}
		if entity != "" && e.Entity != entity && e.Parent != entity {
			continue
		}
		cp := *e
		out = append(out, &cp)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *Memory) UpsertGraphEdge(e *GraphEdge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e == nil || e.ID == "" {
		return fmt.Errorf("edge id required")
	}
	cp := *e
	if e.MetaJSON != nil {
		cp.MetaJSON = append([]byte(nil), e.MetaJSON...)
	}
	m.graph[e.ID] = &cp
	return nil
}

func (m *Memory) ListGraphEdges(userID, subject, object string, limit int) ([]*GraphEdge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []*GraphEdge
	for _, e := range m.graph {
		if e.UserID != userID {
			continue
		}
		if subject != "" && e.Subject != subject {
			continue
		}
		if object != "" && e.Object != object {
			continue
		}
		cp := *e
		if e.MetaJSON != nil {
			cp.MetaJSON = append([]byte(nil), e.MetaJSON...)
		}
		out = append(out, &cp)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *Memory) CreatePurchase(p *Purchase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil || p.ID == "" {
		return fmt.Errorf("purchase id required")
	}
	cp := *p
	m.purchases[p.ID] = &cp
	return nil
}

func (m *Memory) GetPurchase(userID, id string) (*Purchase, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.purchases[id]
	if !ok || p.UserID != userID {
		return nil, fmt.Errorf("purchase not found")
	}
	cp := *p
	return &cp, nil
}

func (m *Memory) ListPurchases(userID string, limit int) ([]*Purchase, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []*Purchase
	for _, p := range m.purchases {
		if p.UserID != userID {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) UpdatePurchase(p *Purchase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil || p.ID == "" {
		return fmt.Errorf("purchase id required")
	}
	if _, ok := m.purchases[p.ID]; !ok {
		return fmt.Errorf("purchase not found")
	}
	cp := *p
	m.purchases[p.ID] = &cp
	return nil
}

func (m *Memory) CreateConnector(c *ConnectorBinding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c == nil || c.ID == "" {
		return fmt.Errorf("connector id required")
	}
	cp := *c
	if c.ConfigJSON != nil {
		cp.ConfigJSON = append([]byte(nil), c.ConfigJSON...)
	}
	m.connectors[c.ID] = &cp
	return nil
}

func (m *Memory) GetConnector(userID, id string) (*ConnectorBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.connectors[id]
	if !ok || c.UserID != userID {
		return nil, fmt.Errorf("connector not found")
	}
	cp := *c
	if c.ConfigJSON != nil {
		cp.ConfigJSON = append([]byte(nil), c.ConfigJSON...)
	}
	return &cp, nil
}

func (m *Memory) ListConnectors(userID string) ([]*ConnectorBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*ConnectorBinding
	for _, c := range m.connectors {
		if c.UserID != userID {
			continue
		}
		cp := *c
		if c.ConfigJSON != nil {
			cp.ConfigJSON = append([]byte(nil), c.ConfigJSON...)
		}
		out = append(out, &cp)
	}
	return out, nil
}

func (m *Memory) DeleteConnector(userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.connectors[id]
	if !ok || c.UserID != userID {
		return fmt.Errorf("connector not found")
	}
	delete(m.connectors, id)
	return nil
}

func cloneWebhookOutbox(e *WebhookOutbox) *WebhookOutbox {
	if e == nil {
		return nil
	}
	cp := *e
	if e.PayloadJSON != nil {
		cp.PayloadJSON = append([]byte(nil), e.PayloadJSON...)
	}
	return &cp
}

func (m *Memory) EnqueueWebhookOutbox(e *WebhookOutbox) error {
	if e == nil || strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("webhook outbox id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.webhooks[e.ID]; ok {
		return fmt.Errorf("webhook outbox exists")
	}
	m.webhooks[e.ID] = cloneWebhookOutbox(e)
	return nil
}

func (m *Memory) ListDueWebhookOutbox(now time.Time, limit int) ([]*WebhookOutbox, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 32
	}
	if limit > 200 {
		limit = 200
	}
	var due []*WebhookOutbox
	for _, e := range m.webhooks {
		if e.Status != "pending" {
			continue
		}
		if e.NextAttemptAt.After(now) {
			continue
		}
		due = append(due, cloneWebhookOutbox(e))
	}
	// oldest next_attempt_at first
	for i := 0; i < len(due); i++ {
		for k := i + 1; k < len(due); k++ {
			if due[k].NextAttemptAt.Before(due[i].NextAttemptAt) {
				due[i], due[k] = due[k], due[i]
			}
		}
	}
	if len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

func (m *Memory) UpdateWebhookOutbox(e *WebhookOutbox) error {
	if e == nil || strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("webhook outbox id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.webhooks[e.ID]; !ok {
		return fmt.Errorf("webhook outbox not found")
	}
	m.webhooks[e.ID] = cloneWebhookOutbox(e)
	return nil
}

func (m *Memory) GetWebhookOutbox(id string) (*WebhookOutbox, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("webhook outbox not found")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.webhooks[id]
	if !ok {
		return nil, fmt.Errorf("webhook outbox not found")
	}
	return cloneWebhookOutbox(e), nil
}

func (m *Memory) ListWebhookOutbox(f WebhookOutboxFilter) ([]*WebhookOutbox, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	status := strings.TrimSpace(f.Status)
	var out []*WebhookOutbox
	for _, e := range m.webhooks {
		if status != "" && e.Status != status {
			continue
		}
		out = append(out, cloneWebhookOutbox(e))
	}
	// newest created_at first
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) PurgeWebhookOutbox(olderThan time.Time, limit int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	type cand struct {
		id  string
		at  time.Time
	}
	var list []cand
	for id, e := range m.webhooks {
		switch e.Status {
		case "delivered":
			at := e.DeliveredAt
			if at.IsZero() {
				at = e.UpdatedAt
			}
			if at.Before(olderThan) {
				list = append(list, cand{id: id, at: at})
			}
		case "dead":
			if e.UpdatedAt.Before(olderThan) {
				list = append(list, cand{id: id, at: e.UpdatedAt})
			}
		}
	}
	// oldest first
	for i := 0; i < len(list); i++ {
		for k := i + 1; k < len(list); k++ {
			if list[k].at.Before(list[i].at) {
				list[i], list[k] = list[k], list[i]
			}
		}
	}
	if len(list) > limit {
		list = list[:limit]
	}
	for _, c := range list {
		delete(m.webhooks, c.id)
	}
	return len(list), nil
}
