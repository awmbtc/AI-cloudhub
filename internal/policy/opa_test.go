package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOPADenyProviderWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "authz.rego")
	module := `
package aicloudhub.authz
import rego.v1
default allow := true
allow := false if {
  input.principal == "agent"
  input.action == "provider.write"
}
`
	if err := os.WriteFile(p, []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngineWithOptions(EngineOptions{OPAPath: p})
	if err != nil {
		t.Fatal(err)
	}
	// agent provider.write denied
	d := e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"provider.write"},
		Action:  ActionProviderWrite,
	})
	if d.Allow {
		t.Fatalf("want deny, got %+v", d)
	}
	// agent provider.read allowed
	d = e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"provider.read"},
		Action:  ActionProviderRead,
	})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
	// human always ok at OPA if default allow (no agent rules)
	d = e.Evaluate(Request{Action: ActionProviderWrite})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
}

func TestOPAObserve(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "authz.rego")
	module := `
package aicloudhub.authz
import rego.v1
default allow := true
allow := false if { input.agent_id != "" }
`
	if err := os.WriteFile(p, []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_CLOUDHUB_OPA_OBSERVE", "1")
	// re-load after env
	e, err := NewEngineWithOptions(EngineOptions{OPAPath: p})
	if err != nil {
		t.Fatal(err)
	}
	// LoadOPAFile reads env at load time
	e.opa, err = LoadOPAFile(p)
	if err != nil {
		t.Fatal(err)
	}
	d := e.Evaluate(Request{AgentID: "a", Scopes: []string{"drive.read"}, Action: ActionDriveRead})
	if !d.Allow {
		t.Fatalf("observe should allow: %+v", d)
	}
	if d.Reason != "opa:observe:would-deny" && d.Reason != "opa:allow" {
		// reason may be combined
		t.Logf("reason=%s", d.Reason)
	}
}
