package marketplace

import (
	"strings"
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestListSystemAndInstall(t *testing.T) {
	s := New(store.NewMemory())
	items, err := s.List("u1", false)
	if err != nil || len(items) < 2 {
		t.Fatalf("%v %d", err, len(items))
	}
	var created string
	res, err := s.InstallAgentTemplate("u1", "sys.agent.readonly", func(name, desc string, scopes []string) (string, error) {
		created = "agent-1"
		if name == "" || len(scopes) == 0 {
			t.Fatal(name, scopes)
		}
		return created, nil
	})
	if err != nil || res.AgentID != "agent-1" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestInstallPaidRequiresPurchase(t *testing.T) {
	st := store.NewMemory()
	s := New(st)
	it, err := s.Publish("seller", PublishInput{
		Name: "paid-agent", Kind: KindAgentTemplate, Public: true, PriceCents: 999, Currency: "usd",
		Payload: map[string]interface{}{"name": "paid", "default_scopes": []interface{}{"drive.read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InstallAgentTemplate("buyer", it.ID, func(name, desc string, scopes []string) (string, error) {
		return "x", nil
	})
	if err == nil || !strings.Contains(err.Error(), "purchase required") {
		t.Fatalf("want purchase required, got %v", err)
	}
	p, err := s.Checkout("buyer", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebhookPaid("buyer", p.ID, "test"); err != nil {
		t.Fatal(err)
	}
	res, err := s.InstallAgentTemplate("buyer", it.ID, func(name, desc string, scopes []string) (string, error) {
		return "agent-paid", nil
	})
	if err != nil || res.AgentID != "agent-paid" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestPublish(t *testing.T) {
	s := New(store.NewMemory())
	it, err := s.Publish("u1", PublishInput{
		Name: "my-tpl", Kind: KindAgentTemplate, Public: true,
		Payload: map[string]interface{}{"name": "x", "default_scopes": []interface{}{"drive.read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(it.ID)
	if err != nil || got.Name != "my-tpl" {
		t.Fatal(err, got)
	}
}

func TestInstallSkillFreeAndPaid(t *testing.T) {
	st := store.NewMemory()
	s := New(st)
	// system skill is free
	res, err := s.InstallSkill("u1", "sys.skill.qiniu_presign")
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentID != "" || res.Kind != KindSkill || res.ItemID != "sys.skill.qiniu_presign" {
		t.Fatalf("%+v", res)
	}
	if res.Extra["tool"] != "object_presign_get" {
		t.Fatalf("payload %+v", res.Extra)
	}
	// paid skill gate
	it, err := s.Publish("seller", PublishInput{
		Name: "paid-skill", Kind: KindSkill, Public: true, PriceCents: 100, Currency: "usd",
		Payload: map[string]interface{}{"tool": "x", "name": "paid-skill"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InstallSkill("buyer", it.ID)
	if err == nil || !strings.Contains(err.Error(), "purchase required") {
		t.Fatalf("want purchase required, got %v", err)
	}
	p, err := s.Checkout("buyer", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebhookPaid("buyer", p.ID, "test"); err != nil {
		t.Fatal(err)
	}
	res, err = s.InstallSkill("buyer", it.ID)
	if err != nil || res.AgentID != "" || res.Name != "paid-skill" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestInstallManifest(t *testing.T) {
	s := New(store.NewMemory())
	res, err := s.InstallManifest("u1", "sys.manifest.workspace_v2")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindManifest || res.AgentID != "" {
		t.Fatalf("%+v", res)
	}
	if res.Extra["version"] == nil {
		t.Fatalf("payload %+v", res.Extra)
	}
}

func TestInstallSkillWrongKind(t *testing.T) {
	s := New(store.NewMemory())
	_, err := s.InstallSkill("u1", "sys.agent.readonly")
	if err == nil || !strings.Contains(err.Error(), "need skill") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckoutDetailedMockURL(t *testing.T) {
	// No Stripe secret → mock checkout_url
	t.Setenv("AI_CLOUDHUB_STRIPE_SECRET_KEY", "")
	t.Setenv("STRIPE_SECRET_KEY", "")
	s := New(store.NewMemory())
	it, err := s.Publish("seller", PublishInput{
		Name: "paid", Kind: KindSkill, Public: true, PriceCents: 100, Currency: "usd",
		Payload: map[string]interface{}{"x": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.CheckoutDetailed("buyer", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "pending" {
		t.Fatalf("%+v", res)
	}
	if res.CheckoutURL == "" || !strings.Contains(res.CheckoutURL, "checkout.stripe.com") {
		t.Fatalf("checkout_url %q", res.CheckoutURL)
	}
	if res.SessionID == "" || !strings.HasPrefix(res.SessionID, "cs_test_mock_") {
		t.Fatalf("session %q", res.SessionID)
	}
	if res.StripeMetadata["purchase_id"] == "" {
		t.Fatal("metadata")
	}
}
