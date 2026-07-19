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

// NewService creates an agent service.
func NewService(st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st, engine: policy.NewEngine()}
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
func (s *Service) Update(ownerUserID, id string, in UpdateInput) (*Record, error) {
	a, err := s.store.GetAgent(ownerUserID, id)
	if err != nil {
		return nil, fmt.Errorf("agent not found")
	}
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

// CheckDriveAccess enforces B1 allowlist for an agent token.
func (s *Service) CheckDriveAccess(agentID, driveID string) error {
	if agentID == "" || driveID == "" {
		return nil
	}
	a, err := s.store.GetAgentByID(agentID)
	if err != nil {
		return fmt.Errorf("agent not found")
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
		CreatedAt:       a.CreatedAt,
	}
}
