// Package marketplace is Stage C Agent Marketplace v0 — catalog of templates
// agents/skills that install into the control plane (not a payment marketplace).
package marketplace

import (
	"encoding/json"
	"fmt"
	"os"
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
	PriceCents  int64
	Currency    string
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
	cur := strings.TrimSpace(in.Currency)
	if in.PriceCents > 0 && cur == "" {
		cur = "usd"
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
		PriceCents:      in.PriceCents,
		Currency:        cur,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.st.CreateMarketplaceItem(it); err != nil {
		return nil, err
	}
	return it, nil
}

// Checkout creates a pending purchase (payment-grade skeleton).
// Free items (price 0) are marked paid immediately.
// Paid items return status=pending with provider=stripe_stub — complete via WebhookPaid.
func (s *Service) Checkout(userID, itemID string) (*store.Purchase, error) {
	it, err := s.Get(itemID)
	if err != nil {
		return nil, err
	}
	p := &store.Purchase{
		ID: uuid.NewString(), UserID: userID, ItemID: itemID,
		AmountCents: it.PriceCents, Currency: it.Currency,
		Provider: "stripe_stub", CreatedAt: time.Now().UTC(),
	}
	if it.PriceCents <= 0 {
		p.Status = "paid"
		p.Provider = "free"
		p.ProviderRef = "free"
	} else {
		p.Status = "pending"
		p.Provider = "stripe"
		p.ProviderRef = "pending_" + p.ID
		if p.Currency == "" {
			p.Currency = "usd"
		}
	}
	if err := s.st.CreatePurchase(p); err != nil {
		return nil, err
	}
	return p, nil
}

// CheckoutResult extends purchase with client_hint for Stripe Checkout metadata.
type CheckoutResult struct {
	*store.Purchase
	// StripeMetadata should be attached to Checkout Session metadata on the client.
	StripeMetadata map[string]string `json:"stripe_metadata,omitempty"`
	// Note explains next steps when using real Stripe.
	Note string `json:"note,omitempty"`
}

// CheckoutDetailed returns purchase plus Stripe metadata hints.
func (s *Service) CheckoutDetailed(userID, itemID string) (*CheckoutResult, error) {
	p, err := s.Checkout(userID, itemID)
	if err != nil {
		return nil, err
	}
	out := &CheckoutResult{Purchase: p}
	if p.Status == "pending" {
		out.StripeMetadata = map[string]string{
			"purchase_id": p.ID,
			"user_id":     userID,
			"item_id":     itemID,
		}
		out.Note = "Create Stripe Checkout Session with these metadata fields; webhook POST /v1/webhooks/stripe with AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET"
	}
	return out, nil
}

// HandleStripeWebhook verifies optional Stripe signature and marks purchase paid.
func (s *Service) HandleStripeWebhook(payload []byte, sigHeader string) (*store.Purchase, error) {
	secret := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET"))
	insecure := envTruthy("AI_CLOUDHUB_STRIPE_ALLOW_INSECURE")
	if secret != "" {
		if err := VerifyStripeSignature(payload, sigHeader, secret, time.Now()); err != nil {
			return nil, fmt.Errorf("stripe signature: %w", err)
		}
	} else if !insecure {
		return nil, fmt.Errorf("set AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET (or AI_CLOUDHUB_STRIPE_ALLOW_INSECURE=1 for local dev)")
	}
	pid, uid, sid, err := ParseStripeCheckoutCompleted(payload)
	if err != nil {
		return nil, err
	}
	return s.WebhookPaid(uid, pid, sid)
}

func envTruthy(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	return v == "1" || v == "true" || v == "yes"
}

// WebhookPaid marks a purchase paid (called by payment provider webhook stub).
func (s *Service) WebhookPaid(userID, purchaseID, providerRef string) (*store.Purchase, error) {
	p, err := s.st.GetPurchase(userID, purchaseID)
	if err != nil {
		return nil, err
	}
	p.Status = "paid"
	if providerRef != "" {
		p.ProviderRef = providerRef
	}
	if err := s.st.UpdatePurchase(p); err != nil {
		return nil, err
	}
	return p, nil
}

// ListPurchases returns user purchases.
func (s *Service) ListPurchases(userID string, limit int) ([]*store.Purchase, error) {
	return s.st.ListPurchases(userID, limit)
}

// Delete removes a user-published item.
func (s *Service) Delete(userID, id string) error {
	if strings.HasPrefix(id, "sys.") {
		return fmt.Errorf("cannot delete system catalog item")
	}
	return s.st.DeleteMarketplaceItem(userID, id)
}

// AgentInstallResult is the outcome of installing a marketplace item.
// For agent_template, AgentID is set. For skill/manifest, AgentID is empty (payload-only install).
type AgentInstallResult struct {
	Kind    string                 `json:"kind,omitempty"`
	AgentID string                 `json:"agent_id,omitempty"`
	Name    string                 `json:"name"`
	Scopes  []string               `json:"scopes,omitempty"`
	ItemID  string                 `json:"item_id"`
	Extra   map[string]interface{} `json:"payload,omitempty"`
}

// HasPaidAccess reports whether user may use a paid item (free items always ok).
func (s *Service) HasPaidAccess(userID, itemID string) (bool, error) {
	it, err := s.Get(itemID)
	if err != nil {
		return false, err
	}
	if it.PriceCents <= 0 {
		return true, nil
	}
	purchases, err := s.ListPurchases(userID, 200)
	if err != nil {
		return false, err
	}
	for _, p := range purchases {
		if p.ItemID == itemID && p.Status == "paid" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) requirePaid(userID, itemID string) error {
	ok, err := s.HasPaidAccess(userID, itemID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("purchase required: POST /v1/marketplace/%s/checkout then complete payment (status=paid)", itemID)
	}
	return nil
}

// InstallAgentTemplate materializes an agent from a marketplace agent_template.
// Paid listings require a purchase with status=paid for this user.
func (s *Service) InstallAgentTemplate(userID, itemID string, createAgent func(name, desc string, scopes []string) (agentID string, err error)) (*AgentInstallResult, error) {
	it, err := s.Get(itemID)
	if err != nil {
		return nil, err
	}
	if it.Kind != KindAgentTemplate {
		return nil, fmt.Errorf("item kind is %s, need agent_template", it.Kind)
	}
	if err := s.requirePaid(userID, itemID); err != nil {
		return nil, err
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
	return &AgentInstallResult{Kind: KindAgentTemplate, AgentID: aid, Name: name, Scopes: scopes, ItemID: itemID, Extra: payload}, nil
}

// InstallSkill grants access to a skill catalog item (metadata/docs/hints).
// Does not create an agent — payload is returned for client/memory materialization.
// Paid listings require purchase status=paid (same gate as agent_template).
func (s *Service) InstallSkill(userID, itemID string) (*AgentInstallResult, error) {
	return s.installPayloadItem(userID, itemID, KindSkill)
}

// InstallManifest grants access to a manifest catalog item (workspace skeleton payload).
// Same paid gate and no agent create as InstallSkill.
func (s *Service) InstallManifest(userID, itemID string) (*AgentInstallResult, error) {
	return s.installPayloadItem(userID, itemID, KindManifest)
}

func (s *Service) installPayloadItem(userID, itemID, wantKind string) (*AgentInstallResult, error) {
	it, err := s.Get(itemID)
	if err != nil {
		return nil, err
	}
	if it.Kind != wantKind {
		return nil, fmt.Errorf("item kind is %s, need %s", it.Kind, wantKind)
	}
	if err := s.requirePaid(userID, itemID); err != nil {
		return nil, err
	}
	var payload map[string]interface{}
	_ = json.Unmarshal(it.PayloadJSON, &payload)
	if payload == nil {
		payload = map[string]interface{}{}
	}
	name, _ := payload["name"].(string)
	if name == "" {
		name = it.Name
	}
	return &AgentInstallResult{
		Kind:   wantKind,
		Name:   name,
		ItemID: itemID,
		Extra:  payload,
	}, nil
}
