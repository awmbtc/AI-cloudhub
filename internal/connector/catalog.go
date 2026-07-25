// Package connector registers Git/DB/SaaS connector *types* (Stage C).
// Full sync engines are out of scope; this is first-class catalog + binding registry.
package connector

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// TypeMeta describes a connector kind in the catalog.
type TypeMeta struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Category    string   `json:"category"` // git|db|saas
	Implemented bool     `json:"implemented"`
	Fields      []string `json:"fields"`
	Notes       string   `json:"notes"`
}

// Catalog returns first-class connector types (object storage remains primary for bytes).
func Catalog() []TypeMeta {
	return []TypeMeta{
		{Type: "git", Name: "Git repository", Category: "git", Implemented: true,
			Fields: []string{"remote_url", "branch?", "path_prefix?"},
			Notes:  "Metadata binding only; clone/sync runs on user runner (BYOC), not control plane"},
		{Type: "postgres", Name: "PostgreSQL", Category: "db", Implemented: true,
			Fields: []string{"host", "database", "schema?"},
			Notes:  "Schema/introspection hooks future; credentials never logged"},
		{Type: "mysql", Name: "MySQL", Category: "db", Implemented: true,
			Fields: []string{"host", "database"}, Notes: "Binding registry; query plane on user side"},
		{Type: "notion", Name: "Notion workspace", Category: "saas", Implemented: true,
			Fields: []string{"workspace_id"}, Notes: "OAuth/token held by user; control plane stores non-secret config only"},
		{Type: "slack", Name: "Slack workspace", Category: "saas", Implemented: true,
			Fields: []string{"team_id"}, Notes: "Bot token stays on user runner"},
		{Type: "github", Name: "GitHub", Category: "saas", Implemented: true,
			Fields: []string{"org_or_user", "repo?"}, Notes: "App install on user side"},
	}
}

// IsKnown reports a catalog type.
func IsKnown(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	for _, m := range Catalog() {
		if m.Type == t {
			return true
		}
	}
	return false
}

// Service manages connector bindings.
type Service struct{ st store.Store }

// New creates a connector service.
func New(st store.Store) *Service { return &Service{st: st} }

// CreateInput registers a connector.
type CreateInput struct {
	Type   string                 `json:"type"`
	Name   string                 `json:"name"`
	Config map[string]interface{} `json:"config"`
}

// Create registers a connector binding (config must not include secrets; use env on runner).
func (s *Service) Create(userID string, in CreateInput) (*store.ConnectorBinding, error) {
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("connectors not configured")
	}
	t := strings.ToLower(strings.TrimSpace(in.Type))
	if !IsKnown(t) {
		return nil, fmt.Errorf("unknown connector type %q", in.Type)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = t
	}
	cfg := in.Config
	if cfg == nil {
		cfg = map[string]interface{}{}
	}
	// Strip common secret keys if user mistakenly sent them
	for _, k := range []string{"password", "token", "secret", "api_key", "access_token"} {
		delete(cfg, k)
	}
	b, _ := json.Marshal(cfg)
	c := &store.ConnectorBinding{
		ID: uuid.NewString(), UserID: userID, Type: t, Name: name,
		ConfigJSON: b, Status: "registered", CreatedAt: time.Now().UTC(),
	}
	if err := s.st.CreateConnector(c); err != nil {
		return nil, err
	}
	return c, nil
}

// List returns user connectors.
func (s *Service) List(userID string) ([]*store.ConnectorBinding, error) {
	return s.st.ListConnectors(userID)
}

// Get one connector.
func (s *Service) Get(userID, id string) (*store.ConnectorBinding, error) {
	return s.st.GetConnector(userID, id)
}

// Delete removes a connector.
func (s *Service) Delete(userID, id string) error {
	return s.st.DeleteConnector(userID, id)
}
