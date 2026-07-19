// Package agent manages Agent Identity principals (ROADMAP-2.0 stage A).
package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/auth"
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
	ID            string    `json:"id"`
	OwnerUserID   string    `json:"owner_user_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Status        string    `json:"status"`
	DefaultScopes []string  `json:"default_scopes"`
	CreatedAt     time.Time `json:"created_at"`
}

// CreateInput body for POST /v1/agents.
type CreateInput struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	DefaultScopes []string `json:"default_scopes"`
}

// Service backs agent CRUD.
type Service struct {
	store store.Store
}

// NewService creates an agent service.
func NewService(st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st}
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
	a := &store.Agent{
		ID:            uuid.NewString(),
		OwnerUserID:   ownerUserID,
		Name:          name,
		Description:   strings.TrimSpace(in.Description),
		Status:        StatusActive,
		DefaultScopes: scopes,
		CreatedAt:     time.Now().UTC(),
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

// Delete removes an agent.
func (s *Service) Delete(ownerUserID, id string) error {
	if err := s.store.DeleteAgent(ownerUserID, id); err != nil {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func fromStore(a *store.Agent) *Record {
	return &Record{
		ID:            a.ID,
		OwnerUserID:   a.OwnerUserID,
		Name:          a.Name,
		Description:   a.Description,
		Status:        a.Status,
		DefaultScopes: append([]string(nil), a.DefaultScopes...),
		CreatedAt:     a.CreatedAt,
	}
}
