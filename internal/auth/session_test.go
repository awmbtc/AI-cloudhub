package auth

import (
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestLogoutRevokesJTI(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret-long", st)
	if _, err := svc.Register("alice", "password1"); err != nil {
		t.Fatal(err)
	}
	tok, _, err := svc.Login("alice", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(tok); err != nil {
		t.Fatalf("parse before logout: %v", err)
	}
	if err := svc.Logout(tok); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(tok); err == nil {
		t.Fatal("expected revoked after logout")
	}
}

func TestRevokeAllSessionsBumpsVersion(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret-long", st)
	u, err := svc.Register("bob", "password1")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := svc.Login("bob", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevokeAllSessions(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(tok); err == nil {
		t.Fatal("expected revoked after version bump")
	}
	// new login works
	tok2, _, err := svc.Login("bob", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(tok2); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordRevokesSessions(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret-long", st)
	if _, err := svc.Register("carol", "password1"); err != nil {
		t.Fatal(err)
	}
	tok, u, err := svc.Login("carol", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(u.ID, "password1", "password2"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(tok); err == nil {
		t.Fatal("expected old token invalid after password change")
	}
	if _, _, err := svc.Login("carol", "password2"); err != nil {
		t.Fatal(err)
	}
}
