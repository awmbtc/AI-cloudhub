package auth

import (
	"strings"
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestRegisterStoresBcryptHash(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret", st)

	u, err := svc.Register("alice", "s3cret-pass")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.ID == "" || u.Username != "alice" {
		t.Fatalf("unexpected user: %+v", u)
	}

	rec, err := st.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if rec.Password == "s3cret-pass" {
		t.Fatal("password stored in plaintext")
	}
	if !isBcryptHash(rec.Password) {
		t.Fatalf("expected bcrypt hash, got %q", rec.Password)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(rec.Password), []byte("s3cret-pass")); err != nil {
		t.Fatalf("stored hash does not match password: %v", err)
	}
}

func TestLoginWithBcrypt(t *testing.T) {
	st := store.NewMemory()
	svc := New("test-secret", st)

	if _, err := svc.Register("bob", "hunter2xx"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	pair, err := svc.Login("bob", "hunter2xx")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if pair.AccessToken == "" || pair.User == nil || pair.User.Username != "bob" {
		t.Fatalf("unexpected login result: %+v", pair)
	}
	if pair.RefreshToken == "" {
		t.Fatal("expected refresh token")
	}

	// Wrong password
	if _, err := svc.Login("bob", "wrong"); err == nil {
		t.Fatal("expected invalid credentials")
	}
}

func TestLoginUpgradesLegacyPlaintext(t *testing.T) {
	st := store.NewMemory()
	// Seed a pre-v0.1.1 plaintext password row.
	if err := st.CreateUser(&store.User{
		ID:       "legacy-1",
		Username: "legacy",
		Password: "old-plain",
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	svc := New("test-secret", st)
	pair, err := svc.Login("legacy", "old-plain")
	if err != nil {
		t.Fatalf("Login legacy: %v", err)
	}
	if pair.AccessToken == "" || pair.User == nil || pair.User.ID != "legacy-1" {
		t.Fatalf("unexpected login: %+v", pair)
	}

	rec, err := st.GetUserByUsername("legacy")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if rec.Password == "old-plain" {
		t.Fatal("legacy password was not upgraded to bcrypt")
	}
	if !isBcryptHash(rec.Password) {
		t.Fatalf("expected bcrypt after upgrade, got %q", rec.Password)
	}

	// Subsequent login must work with the new hash.
	pair2, err := svc.Login("legacy", "old-plain")
	if err != nil {
		t.Fatalf("Login after upgrade: %v", err)
	}
	if pair2.AccessToken == "" {
		t.Fatal("empty token after upgrade login")
	}

	// Wrong password still fails after upgrade.
	if _, err := svc.Login("legacy", "nope"); err == nil {
		t.Fatal("expected invalid credentials after upgrade")
	}
}

func TestHashPasswordIsBcrypt(t *testing.T) {
	h, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$2") {
		t.Fatalf("not bcrypt: %q", h)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	svc := New("s", store.NewMemory())
	if _, err := svc.Register("dup", "password1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Register("dup", "password2"); err == nil {
		t.Fatal("expected user exists")
	}
}

func TestRegisterRequiresFields(t *testing.T) {
	svc := New("s", store.NewMemory())
	if _, err := svc.Register("", "password1"); err == nil {
		t.Fatal("expected error for empty username")
	}
	if _, err := svc.Register("usr", ""); err == nil {
		t.Fatal("expected error for empty password")
	}
	if _, err := svc.Register("ab", "password1"); err == nil {
		t.Fatal("expected username too short")
	}
	if _, err := svc.Register("validuser", "short"); err == nil {
		t.Fatal("expected password too short")
	}
}
