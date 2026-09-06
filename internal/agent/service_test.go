package agent

import (
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/auth"
	"github.com/awmbtc/AI-cloudhub/internal/policy"
	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestCheckAccessRejectsDisabled(t *testing.T) {
	st := store.NewMemory()
	_ = st.CreateUser(&store.User{ID: "u1", Username: "a", Password: "x", Role: "user"})
	svc := NewService(st)
	rec, err := svc.Create("u1", CreateInput{Name: "bot", DefaultScopes: []string{auth.ScopeDriveRead}})
	if err != nil {
		t.Fatal(err)
	}
	dis := StatusDisabled
	rec, err = svc.Update("u1", rec.ID, UpdateInput{Status: &dis})
	if err != nil {
		t.Fatal(err)
	}
	if rec.TokenVersion < 1 {
		t.Fatalf("expected token_version bump on disable, got %d", rec.TokenVersion)
	}
	err = svc.CheckAccess(policy.Request{AgentID: rec.ID, Action: policy.ActionDriveRead})
	if err == nil || err.Error() != "agent disabled" {
		t.Fatalf("want agent disabled, got %v", err)
	}
	if err := svc.CheckDriveAccess(rec.ID, "d1"); err == nil {
		t.Fatal("expected drive access denied for disabled")
	}
}

func TestScopeShrinkBumpsTokenVersion(t *testing.T) {
	st := store.NewMemory()
	_ = st.CreateUser(&store.User{ID: "u1", Username: "a", Password: "x", Role: "user"})
	svc := NewService(st)
	rec, err := svc.Create("u1", CreateInput{
		Name:          "bot",
		DefaultScopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
	})
	if err != nil {
		t.Fatal(err)
	}
	ver0 := rec.TokenVersion
	rec, err = svc.Update("u1", rec.ID, UpdateInput{
		DefaultScopes: []string{auth.ScopeDriveRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.TokenVersion != ver0+1 {
		t.Fatalf("want bump %d -> %d, got %d", ver0, ver0+1, rec.TokenVersion)
	}
	// Expanding should not bump
	rec2, err := svc.Update("u1", rec.ID, UpdateInput{
		DefaultScopes: []string{auth.ScopeDriveRead, auth.ScopeDriveWrite},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec2.TokenVersion != rec.TokenVersion {
		t.Fatalf("expand should not bump: %d -> %d", rec.TokenVersion, rec2.TokenVersion)
	}
}

func TestDrivesTightened(t *testing.T) {
	if !drivesTightened(nil, []string{"d1"}) {
		t.Fatal("empty->list is tighten")
	}
	if drivesTightened([]string{"d1"}, nil) {
		t.Fatal("list->empty is expand")
	}
	if !drivesTightened([]string{"d1", "d2"}, []string{"d1"}) {
		t.Fatal("remove id is tighten")
	}
	if drivesTightened([]string{"d1"}, []string{"d1", "d2"}) {
		t.Fatal("add id is not tighten")
	}
}
