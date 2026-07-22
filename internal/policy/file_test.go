package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDocumentAndDenyPath(t *testing.T) {
	raw := []byte(`{
	  "version": 1,
	  "mode": "enforce",
	  "rules": [
	    {
	      "id": "no-ssh",
	      "effect": "deny",
	      "principals": ["agent"],
	      "actions": ["path.read", "drive.read"],
	      "path_deny_prefixes": [".ssh", ".env"],
	      "reason": "secrets blocked"
	    }
	  ]
	}`)
	doc, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.mu.Lock()
	e.doc = doc
	e.mu.Unlock()

	// human unaffected by agent-only rule
	d := e.Evaluate(Request{Action: ActionPathRead, Path: "/workspace/.ssh/id"})
	if !d.Allow {
		t.Fatalf("human: %s", d.Reason)
	}

	d = e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"drive.read"},
		Action:  ActionPathRead,
		Path:    "/workspace/.ssh/id_rsa",
	})
	if d.Allow || d.Reason != "secrets blocked" {
		t.Fatalf("want deny secrets, got %+v", d)
	}

	d = e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"drive.read"},
		Action:  ActionPathRead,
		Path:    "out/data.txt",
	})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
}

func TestObserveMode(t *testing.T) {
	raw := []byte(`{
	  "version": 1,
	  "mode": "observe",
	  "rules": [{"id":"x","effect":"deny","principals":["agent"],"actions":["job.run"],"reason":"no jobs"}]
	}`)
	doc, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.mu.Lock()
	e.doc = doc
	e.mu.Unlock()
	d := e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"job.run"},
		Action:  ActionJobRun,
	})
	if !d.Allow {
		t.Fatal("observe must allow")
	}
	if d.Reason != "observe:would-deny:no jobs" {
		t.Fatalf("reason %q", d.Reason)
	}
}

func TestLoadFileAndReload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"mode":"enforce","rules":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngineWithOptions(EngineOptions{FilePath: p})
	if err != nil {
		t.Fatal(err)
	}
	st := e.Status()
	if !st.Loaded || st.RuleCount != 0 || st.FilePath != p {
		t.Fatalf("%+v", st)
	}
	// add deny all jobs for agents
	body := `{"version":1,"mode":"enforce","rules":[{"id":"nj","effect":"deny","principals":["agent"],"actions":["job.run"],"reason":"jobs off"}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.LoadFile(p); err != nil {
		t.Fatal(err)
	}
	d := e.Evaluate(Request{AgentID: "a", Scopes: []string{"job.run"}, Action: ActionJobRun})
	if d.Allow {
		t.Fatal("expected deny after reload")
	}
}

func TestAllowRuleShortCircuit(t *testing.T) {
	raw := []byte(`{
	  "version": 1,
	  "rules": [
	    {"id":"allow-d1","effect":"allow","principals":["agent"],"drive_ids":["d1"],"actions":["drive.read"],"reason":"allow d1"},
	    {"id":"deny-all","effect":"deny","principals":["agent"],"actions":["drive.read"],"reason":"deny rest"}
	  ]
	}`)
	doc, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.mu.Lock()
	e.doc = doc
	e.mu.Unlock()
	d := e.Evaluate(Request{AgentID: "a", Scopes: []string{"drive.read"}, Action: ActionDriveRead, DriveID: "d1"})
	if !d.Allow || d.Reason != "allow d1" {
		t.Fatalf("%+v", d)
	}
	d = e.Evaluate(Request{AgentID: "a", Scopes: []string{"drive.read"}, Action: ActionDriveRead, DriveID: "d2"})
	if d.Allow || d.Reason != "deny rest" {
		t.Fatalf("%+v", d)
	}
}

func TestInvalidVersion(t *testing.T) {
	_, err := ParseDocument([]byte(`{"version":99}`))
	if err == nil {
		t.Fatal("want error")
	}
}
