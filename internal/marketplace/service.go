// Package marketplace is Stage C Agent Marketplace v0 — catalog of templates
// agents/skills that install into the control plane (not a payment marketplace).
package marketplace

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Kind values.
const (
	KindAgentTemplate = "agent_template"
	KindSkill         = "skill"
	KindManifest      = "manifest"
)

// Service is the marketplace API (module boundary for future split).
type Service struct {
	st store.Store
}

// New returns a marketplace service.
func New(st store.Store) *Service { return &Service{st: st} }

// systemCatalog is built-in (not in DB).
func systemCatalog() []*store.MarketplaceItem {
	now := time.Unix(0, 0).UTC()
	mk := func(id, name, desc, kind string, payload map[string]interface{}) *store.MarketplaceItem {
		b, _ := json.Marshal(payload)
		return &store.MarketplaceItem{
			ID: id, Name: name, Description: desc, Kind: kind, Version: "1.0.0",
			PayloadJSON: b, Public: true, CreatedAt: now,
		}
	}
	return []*store.MarketplaceItem{
		mk("sys.agent.readonly", "Read-only Drive Agent", "Agent template with drive.read only", KindAgentTemplate, map[string]interface{}{
			"name": "readonly-bot", "default_scopes": []string{"drive.read"}, "description": "list and read drives",
		}),
		mk("sys.agent.jobs", "BYOC Job Worker Agent", "Agent with job.run + drive.read for claim loops", KindAgentTemplate, map[string]interface{}{
			"name": "job-worker", "default_scopes": []string{"drive.read", "job.run"}, "description": "claim and complete BYOC jobs",
		}),
		mk("sys.skill.qiniu_presign", "Qiniu private GET skill", "Hints for object_presign_get on type=qiniu", KindSkill, map[string]interface{}{
			"tool": "object_presign_get", "notes": "method=qiniu_download without live S3",
		}),
		mk("sys.manifest.workspace_v2", "Workspace Manifest v2 skeleton", "permissions.read/write prefixes", KindManifest, map[string]interface{}{
			"version": 2, "permissions": map[string]interface{}{"read": []string{"*"}, "write": []string{"out/"}},
		}),
	}
}

// List returns system catalog + optional DB items.
func (s *Service) List(userID string, mineOnly bool) ([]*store.MarketplaceItem, error) {
	var out []*store.MarketplaceItem
	out = append(out, systemCatalog()...)
	if s == nil || s.st == nil {
		return out, nil
	}
	pub, err := s.st.ListMarketplaceItems(true, "")
	if err != nil {
		return nil, err
	}
	out = append(out, pub...)
	if mineOnly && userID != "" {
		mine, err := s.st.ListMarketplaceItems(false, userID)
		if err != nil {
			return nil, err
		}
		// already may include public mine; append private mine
		seen := map[string]bool{}
		for _, it := range out {
			seen[it.ID] = true
		}
		for _, it := range mine {
			if !seen[it.ID] {
				out = append(out, it)
			}
		}
	}
	return out, nil
}

// Get resolves system or DB item.
func (s *Service) Get(id string) (*store.MarketplaceItem, error) {
	for _, it := range systemCatalog() {
		if it.ID == id {
			return it, nil
		}
	}
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("marketplace item not found")
	}
	return s.st.GetMarketplaceItem(id)
}

// PublishInput is a user-published listing.
type PublishInput struct {
	Name        string
	Description string
	Kind        string
	Version     string
	Payload     map[string]interface{}
	Public      bool
}

// Publish creates a user listing.
func (s *Service) Publish(userID string, in PublishInput) (*store.MarketplaceItem, error) {
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("marketplace not configured")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = KindAgentTemplate
	}
	switch kind {
	case KindAgentTemplate, KindSkill, KindManifest:
	default:
		return nil, fmt.Errorf("kind must be agent_template|skill|manifest")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("name required")
	}
	ver := strings.TrimSpace(in.Version)
	if ver == "" {
		ver = "0.1.0"
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]interface{}{}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	it := &store.MarketplaceItem{
		ID:              uuid.NewString(),
		PublisherUserID: userID,
		Name:            name,
		Description:     strings.TrimSpace(in.Description),
		Kind:            kind,
		Version:         ver,
		PayloadJSON:     b,
		Public:          in.Public,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.st.CreateMarketplaceItem(it); err != nil {
		return nil, err
	}
	return it, nil
}

// Delete removes a user-published item.
func (s *Service) Delete(userID, id string) error {
	if strings.HasPrefix(id, "sys.") {
		return fmt.Errorf("cannot delete system catalog item")
	}
	return s.st.DeleteMarketplaceItem(userID, id)
}

// AgentInstallResult is the outcome of installing an agent_template.
type AgentInstallResult struct {
	AgentID string                 `json:"agent_id"`
	Name    string                 `json:"name"`
	Scopes  []string               `json:"scopes,omitempty"`
	ItemID  string                 `json:"item_id"`
	Extra   map[string]interface{} `json:"payload,omitempty"`
}

// InstallAgentTemplate materializes an agent from a marketplace agent_template.
func (s *Service) InstallAgentTemplate(userID, itemID string, createAgent func(name, desc string, scopes []string) (agentID string, err error)) (*AgentInstallResult, error) {
	it, err := s.Get(itemID)
	if err != nil {
		return nil, err
	}
	if it.Kind != KindAgentTemplate {
		return nil, fmt.Errorf("item kind is %s, need agent_template", it.Kind)
	}
	var payload map[string]interface{}
	_ = json.Unmarshal(it.PayloadJSON, &payload)
	name, _ := payload["name"].(string)
	if name == "" {
		name = it.Name
	}
	desc, _ := payload["description"].(string)
	if desc == "" {
		desc = it.Description
	}
	var scopes []string
	if arr, ok := payload["default_scopes"].([]interface{}); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				scopes = append(scopes, s)
			}
		}
	}
	if len(scopes) == 0 {
		scopes = []string{"drive.read"}
	}
	aid, err := createAgent(name, desc, scopes)
	if err != nil {
		return nil, err
	}
	return &AgentInstallResult{AgentID: aid, Name: name, Scopes: scopes, ItemID: itemID, Extra: payload}, nil
}
