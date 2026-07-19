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
	pair, err := svc.Login("alice", "password1")
	if err != nil {
		t.Fatal(err)
	}
	tok := pair.AccessToken
	if _, _, _, err := svc.Parse(tok); err != nil {
		t.Fatalf("parse before logout: %v", err)
	}
	if err := svc.Logout(tok, pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(tok); err == nil {
		t.Fatal("expected revoked after logout")
	}
	if _, err := svc.Refresh(pair.RefreshToken); err == nil {
		t.Fatal("expected refresh revoked after logout")
	}
}

func TestRevokeAllSessionsBumpsVersion(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret-long", st)
	u, err := svc.Register("bob", "password1")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := svc.Login("bob", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevokeAllSessions(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(pair.AccessToken); err == nil {
		t.Fatal("expected revoked after version bump")
	}
	if _, err := svc.Refresh(pair.RefreshToken); err == nil {
		t.Fatal("expected refresh revoked")
	}
	// new login works
	pair2, err := svc.Login("bob", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(pair2.AccessToken); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordRevokesSessions(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret-long", st)
	if _, err := svc.Register("carol", "password1"); err != nil {
		t.Fatal(err)
	}
	pair, err := svc.Login("carol", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(pair.User.ID, "password1", "password2"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Parse(pair.AccessToken); err == nil {
		t.Fatal("expected old token invalid after password change")
	}
	if _, err := svc.Login("carol", "password2"); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshRotates(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret-long", st)
	if _, err := svc.Register("dave", "password1"); err != nil {
		t.Fatal(err)
	}
	pair, err := svc.Login("dave", "password1")
	if err != nil {
		t.Fatal(err)
	}
	next, err := svc.Refresh(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if next.AccessToken == "" || next.RefreshToken == "" {
		t.Fatal("empty pair")
	}
	if next.RefreshToken == pair.RefreshToken {
		t.Fatal("expected rotated refresh")
	}
	// old refresh dead
	if _, err := svc.Refresh(pair.RefreshToken); err == nil {
		t.Fatal("old refresh should fail")
	}
	if _, _, _, err := svc.Parse(next.AccessToken); err != nil {
		t.Fatal(err)
	}
}
