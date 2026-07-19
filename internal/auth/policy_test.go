package auth

import (
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestValidateUsername(t *testing.T) {
	if err := ValidateUsername("ab"); err == nil {
		t.Fatal("too short")
	}
	if err := ValidateUsername("alice"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUsername("a.b_c-1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUsername("bad name"); err == nil {
		t.Fatal("space")
	}
	if err := ValidateUsername(".starts-dot"); err == nil {
		t.Fatal("must start alnum")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("too short")
	}
	if err := ValidatePassword("long-enough"); err != nil {
		t.Fatal(err)
	}
}

func TestLastAdminProtected(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret-long", st)
	admin, err := svc.Register("admin1", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != RoleAdmin {
		t.Fatalf("first user admin: %s", admin.Role)
	}
	if err := svc.SetRole(admin.ID, RoleUser); err == nil {
		t.Fatal("expected cannot demote last admin")
	}
	// second admin then demote first ok
	u2, err := svc.Register("user2", "password2")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRole(u2.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRole(admin.ID, RoleUser); err != nil {
		t.Fatalf("demote with second admin: %v", err)
	}
}
