package policy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRemotePDPDeny(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		var in map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&in)
		input, _ := in["input"].(map[string]interface{})
		if input != nil && input["action"] == ActionProviderWrite {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"allow": false, "reason": "no-provider-write"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"allow": true})
	}))
	defer srv.Close()

	e, err := NewEngineWithOptions(EngineOptions{PDPURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	// force client (Load from URL field set in options)
	e.pdp = NewRemotePDP(srv.URL, time.Second, false, false)

	d := e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"provider.write"},
		Action:  ActionProviderWrite,
	})
	if d.Allow {
		t.Fatalf("want deny: %+v", d)
	}
	if d.Reason == "" {
		t.Fatal("expected reason")
	}

	d = e.Evaluate(Request{
		AgentID: "a1",
		Scopes:  []string{"provider.read"},
		Action:  ActionProviderRead,
	})
	if !d.Allow {
		t.Fatal(d.Reason)
	}
}

func TestRemotePDPObserve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"allow": false, "reason": "blocked"})
	}))
	defer srv.Close()
	p := NewRemotePDP(srv.URL, time.Second, false, true)
	allow, reason, err := p.Evaluate(Request{AgentID: "a", Action: ActionDriveRead, Scopes: []string{"drive.read"}})
	if err != nil || !allow {
		t.Fatalf("observe should allow: %v %v %v", allow, reason, err)
	}
	if reason == "" || reason == "pdp:allow" {
		t.Fatalf("reason=%s", reason)
	}
}

func TestRemotePDPFailOpen(t *testing.T) {
	p := NewRemotePDP("http://127.0.0.1:1", 50*time.Millisecond, false, false)
	allow, reason, _ := p.Evaluate(Request{Action: ActionDriveRead})
	if !allow {
		t.Fatal("fail-open expected")
	}
	if reason == "" {
		t.Fatal("expected fail-open reason")
	}
}
