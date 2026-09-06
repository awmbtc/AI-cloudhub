package httpserver

import (
	"net/http"
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/drive"
	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

func TestGetDriveByAliasHTTP(t *testing.T) {
	e := newJobSecurityEnv(t)
	u, tok := e.register(t, "alias-user")

	ps := provider.NewService(e.st)
	rec, err := ps.Create(u.ID, provider.CreateInput{
		Name: "p-alias",
		Type: provider.TypeMinIO,
		Creds: provider.Credentials{
			AccessKey: "ak",
			SecretKey: "sk",
			Endpoint:  "127.0.0.1:9000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.drives.Create(u.ID, drive.CreateInput{
		Name:       "R2 A",
		Alias:      "a",
		ProviderID: rec.ID,
		Bucket:     "b1",
		MountPoint: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.drives.Create(u.ID, drive.CreateInput{
		Name:       "other",
		Alias:      "B",
		ProviderID: rec.ID,
		Bucket:     "b2",
		MountPoint: "/other",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Human: lowercase path segment normalizes to A
	code, body, raw := e.do(t, http.MethodGet, "/v1/drives/by-alias/a", tok, nil)
	if code != http.StatusOK {
		t.Fatalf("by-alias human %d %s", code, raw)
	}
	if body["id"] != m.ID || body["alias"] != "A" {
		t.Fatalf("unexpected body: %+v", body)
	}

	// Missing alias → 404
	code, _, raw = e.do(t, http.MethodGet, "/v1/drives/by-alias/NOPE", tok, nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing want 404 got %d %s", code, raw)
	}

	// Invalid alias → 400
	code, _, raw = e.do(t, http.MethodGet, "/v1/drives/by-alias/1bad", tok, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("invalid want 400 got %d %s", code, raw)
	}

	// Agent allowlisted for m only
	_, agentTok := e.agentToken(t, u, "alias-agent", []string{"drive.read"}, []string{m.ID})
	code, body, raw = e.do(t, http.MethodGet, "/v1/drives/by-alias/A", agentTok, nil)
	if code != http.StatusOK || body["id"] != m.ID {
		t.Fatalf("agent allowed %d %s", code, raw)
	}
	code, _, raw = e.do(t, http.MethodGet, "/v1/drives/by-alias/B", agentTok, nil)
	if code != http.StatusForbidden {
		t.Fatalf("agent denied other drive want 403 got %d %s (other=%s)", code, raw, other.ID)
	}

	// Existing {id} route still works; by-alias does not steal UUID paths
	code, body, raw = e.do(t, http.MethodGet, "/v1/drives/"+m.ID, tok, nil)
	if code != http.StatusOK || body["id"] != m.ID {
		t.Fatalf("get by id %d %s", code, raw)
	}

	// Method not allowed
	code, _, raw = e.do(t, http.MethodPost, "/v1/drives/by-alias/A", tok, map[string]interface{}{})
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("POST want 405 got %d %s", code, raw)
	}
}
