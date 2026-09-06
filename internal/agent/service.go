// Package agent manages Agent Identity principals (ROADMAP-2.0 stage A/B).
package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/auth"
	"github.com/awmbtc/AI-cloudhub/internal/policy"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Status values.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Record is the public agent view.
type Record struct {
	ID              string    `json:"id"`
	OwnerUserID     string    `json:"owner_user_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	Status          string    `json:"status"`
	DefaultScopes   []string  `json:"default_scopes"`
	AllowedDriveIDs []string  `json:"allowed_drive_ids"`
	ReadPrefixes    []string  `json:"read_prefixes,omitempty"`
	WritePrefixes   []string  `json:"write_prefixes,omitempty"`
	TokenVersion    int       `json:"token_version"`
	CreatedAt       time.Time `json:"created_at"`
}

// CreateInput body for POST /v1/agents.
type CreateInput struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	DefaultScopes   []string `json:"default_scopes"`
	AllowedDriveIDs []string `json:"allowed_drive_ids"`
	ReadPrefixes    []string `json:"read_prefixes"`
	WritePrefixes   []string `json:"write_prefixes"`
}

// UpdateInput for PATCH-like POST /v1/agents/{id}.
type UpdateInput struct {
	Name            *string  `json:"name"`
	Description     *string  `json:"description"`
	Status          *string  `json:"status"`
	DefaultScopes   []string `json:"default_scopes"`
	AllowedDriveIDs []string `json:"allowed_drive_ids"`
	ReadPrefixes    []string `json:"read_prefixes"`
	WritePrefixes   []string `json:"write_prefixes"`
	// SetDrives when true replaces AllowedDriveIDs even if empty (clear allowlist).
	SetDrives bool `json:"set_drives"`
}

// Service backs agent CRUD.
type Service struct {
	store  store.Store
	engine *policy.Engine
}

// NewService creates an agent service with built-in policy only.
func NewService(st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st, engine: policy.NewEngine()}
}

// NewServiceWithEngine creates an agent service with a shared policy engine
// (optional external JSON file).
func NewServiceWithEngine(st store.Store, eng *policy.Engine) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	if eng == nil {
		eng = policy.NewEngine()
	}
	return &Service{store: st, engine: eng}
}

// Engine returns the policy engine (for HTTP admin / drive checks).
func (s *Service) Engine() *policy.Engine {
	if s == nil {
		return nil
	}
	return s.engine
}

// Create registers an agent for the owner.
func (s *Service) Create(ownerUserID string, in CreateInput) (*Record, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("name required")
	}
	if len(name) > 64 {
		return nil, fmt.Errorf("name too long")
	}
	scopes := auth.NormalizeScopes(in.DefaultScopes)
	if len(scopes) == 0 {
		scopes = []string{auth.ScopeDriveRead, auth.ScopeDriveWrite}
	}
	for _, sc := range scopes {
		if !auth.IsKnownScope(sc) {
			return nil, fmt.Errorf("unknown scope %q", sc)
		}
	}
	// Validate drives belong to owner when specified.
	drives := normalizeIDs(in.AllowedDriveIDs)
	if err := s.validateDrives(ownerUserID, drives); err != nil {
		return nil, err
	}
	a := &store.Agent{
		ID:              uuid.NewString(),
		OwnerUserID:     ownerUserID,
		Name:            name,
		Description:     strings.TrimSpace(in.Description),
		Status:          StatusActive,
		DefaultScopes:   scopes,
		AllowedDriveIDs: drives,
		ReadPrefixes:    normalizePrefixes(in.ReadPrefixes),
		WritePrefixes:   normalizePrefixes(in.WritePrefixes),
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.store.CreateAgent(a); err != nil {
		return nil, err
	}
	return fromStore(a), nil
}

// Get returns one agent owned by user.
func (s *Service) Get(ownerUserID, id string) (*Record, error) {
	a, err := s.store.GetAgent(ownerUserID, id)
	if err != nil {
		return nil, fmt.Errorf("agent not found")
	}
	return fromStore(a), nil
}

// List returns agents for owner.
func (s *Service) List(ownerUserID string) []*Record {
	list, err := s.store.ListAgents(ownerUserID)
	if err != nil {
		return nil
	}
	out := make([]*Record, 0, len(list))
	for _, a := range list {
		out = append(out, fromStore(a))
	}
	return out
}

// Update patches agent fields (human only).
// Disabling or tightening DefaultScopes / AllowedDriveIDs / path prefixes bumps
// TokenVersion so existing agent JWTs fail auth immediately.
func (s *Service) Update(ownerUserID, id string, in UpdateInput) (*Record, error) {
	a, err := s.store.GetAgent(ownerUserID, id)
	if err != nil {
		return nil, fmt.Errorf("agent not found")
	}
	prevStatus := a.Status
	prevScopes := append([]string(nil), a.DefaultScopes...)
	prevDrives := append([]string(nil), a.AllowedDriveIDs...)
	prevRead := append([]string(nil), a.ReadPrefixes...)
	prevWrite := append([]string(nil), a.WritePrefixes...)
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return nil, fmt.Errorf("name required")
		}
		a.Name = n
	}
	if in.Description != nil {
		a.Description = strings.TrimSpace(*in.Description)
	}
	if in.Status != nil {
		st := strings.TrimSpace(*in.Status)
		if st != StatusActive && st != StatusDisabled {
			return nil, fmt.Errorf("status must be active|disabled")
		}
		a.Status = st
	}
	if in.DefaultScopes != nil {
		scopes := auth.NormalizeScopes(in.DefaultScopes)
		for _, sc := range scopes {
			if !auth.IsKnownScope(sc) {
				return nil, fmt.Errorf("unknown scope %q", sc)
			}
		}
		a.DefaultScopes = scopes
	}
	if in.SetDrives || in.AllowedDriveIDs != nil {
		drives := normalizeIDs(in.AllowedDriveIDs)
		if err := s.validateDrives(ownerUserID, drives); err != nil {
			return nil, err
		}
		a.AllowedDriveIDs = drives
	}
	if in.ReadPrefixes != nil {
		a.ReadPrefixes = normalizePrefixes(in.ReadPrefixes)
	}
	if in.WritePrefixes != nil {
		a.WritePrefixes = normalizePrefixes(in.WritePrefixes)
	}
	revoke := false
	if a.Status == StatusDisabled && prevStatus != StatusDisabled {
		revoke = true
	}
	if scopesTightened(prevScopes, a.DefaultScopes) {
		revoke = true
	}
	if drivesTightened(prevDrives, a.AllowedDriveIDs) {
		revoke = true
	}
	if prefixesTightened(prevRead, a.ReadPrefixes) || prefixesTightened(prevWrite, a.WritePrefixes) {
		revoke = true
	}
	if revoke {
		a.TokenVersion++
	}
	if err := s.store.UpdateAgent(a); err != nil {
		return nil, err
	}
	return fromStore(a), nil
}

// Delete removes an agent.
func (s *Service) Delete(ownerUserID, id string) error {
	if err := s.store.DeleteAgent(ownerUserID, id); err != nil {
		return fmt.Errorf("agent not found")
	}
	return nil
}

// CheckAccess evaluates full policy (built-in + file) for an agent request.
func (s *Service) CheckAccess(req policy.Request) error {
	if s == nil || s.engine == nil {
		return nil
	}
	// Fill agent path/drive prefixes from record when agent known.
	if req.AgentID != "" {
		a, err := s.store.GetAgentByID(req.AgentID)
		if err != nil || a == nil {
			return fmt.Errorf("agent not found")
		}
		if a.Status == StatusDisabled {
			return fmt.Errorf("agent disabled")
		}
		if len(req.AllowedDriveIDs) == 0 {
			req.AllowedDriveIDs = a.AllowedDriveIDs
		}
		if len(req.ReadPrefixes) == 0 {
			req.ReadPrefixes = a.ReadPrefixes
		}
		if len(req.WritePrefixes) == 0 {
			req.WritePrefixes = a.WritePrefixes
		}
	}
	d := s.engine.Evaluate(req)
	if !d.Allow {
		return fmt.Errorf("%s", d.Reason)
	}
	return nil
}

// CheckDriveAccess enforces B1 allowlist for an agent token.
func (s *Service) CheckDriveAccess(agentID, driveID string) error {
	if agentID == "" || driveID == "" {
		return nil
	}
	a, err := s.store.GetAgentByID(agentID)
	if err != nil {
		return fmt.Errorf("agent not found")
	}
	if a.Status == StatusDisabled {
		return fmt.Errorf("agent disabled")
	}
	return policy.CanAccessDrive(agentID, a.AllowedDriveIDs, driveID)
}

// GetByID for policy/manifest enrichment.
func (s *Service) GetByID(id string) (*Record, error) {
	a, err := s.store.GetAgentByID(id)
	if err != nil {
		return nil, err
	}
	return fromStore(a), nil
}

func (s *Service) validateDrives(ownerUserID string, driveIDs []string) error {
	for _, id := range driveIDs {
		if _, err := s.store.GetDrive(ownerUserID, id); err != nil {
			return fmt.Errorf("drive %s not found or not owned", id)
		}
	}
	return nil
}

func normalizeIDs(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func normalizePrefixes(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "/")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func fromStore(a *store.Agent) *Record {
	return &Record{
		ID:              a.ID,
		OwnerUserID:     a.OwnerUserID,
		Name:            a.Name,
		Description:     a.Description,
		Status:          a.Status,
		DefaultScopes:   append([]string(nil), a.DefaultScopes...),
		AllowedDriveIDs: append([]string(nil), a.AllowedDriveIDs...),
		ReadPrefixes:    append([]string(nil), a.ReadPrefixes...),
		WritePrefixes:   append([]string(nil), a.WritePrefixes...),
		TokenVersion:    a.TokenVersion,
		CreatedAt:       a.CreatedAt,
	}
}

// scopesTightened is true when new scopes omit at least one previously granted scope
// (or become empty while old was non-empty). Expanding scopes does not revoke.
func scopesTightened(oldS, newS []string) bool {
	if oldS == nil && newS == nil {
		return false
	}
	if stringSlicesEqual(oldS, newS) {
		return false
	}
	oldSet := map[string]bool{}
	for _, s := range oldS {
		oldSet[s] = true
	}
	newSet := map[string]bool{}
	for _, s := range newS {
		newSet[s] = true
	}
	for s := range oldSet {
		if !newSet[s] {
			return true
		}
	}
	return false
}

// drivesTightened: empty allowlist means all drives; non-empty is a restriction.
// Tightening = empty→non-empty, or removing an id from a non-empty list.
func drivesTightened(oldD, newD []string) bool {
	if stringSlicesEqual(oldD, newD) {
		return false
	}
	if len(oldD) == 0 && len(newD) > 0 {
		return true // newly restricted
	}
	if len(oldD) > 0 && len(newD) == 0 {
		return false // expanded to all
	}
	oldSet := map[string]bool{}
	for _, id := range oldD {
		oldSet[id] = true
	}
	newSet := map[string]bool{}
	for _, id := range newD {
		newSet[id] = true
	}
	for id := range oldSet {
		if !newSet[id] {
			return true
		}
	}
	return false
}

// prefixesTightened: empty = full workspace; adding/removing that shrinks access revokes.
func prefixesTightened(oldP, newP []string) bool {
	if stringSlicesEqual(oldP, newP) {
		return false
	}
	if len(oldP) == 0 && len(newP) > 0 {
		return true
	}
	if len(oldP) > 0 && len(newP) == 0 {
		return false
	}
	oldSet := map[string]bool{}
	for _, p := range oldP {
		oldSet[p] = true
	}
	newSet := map[string]bool{}
	for _, p := range newP {
		newSet[p] = true
	}
	for p := range oldSet {
		if !newSet[p] {
			return true
		}
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
