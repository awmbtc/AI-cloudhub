package provider

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/crypto/secretbox"
	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestCreateSealsSecretWhenBoxSet(t *testing.T) {
	key := hex.EncodeToString([]byte(strings.Repeat("a", 32)))
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithBox(store.NewMemory(), box)
	rec, err := svc.Create("user-1", CreateInput{
		Name: "minio-local",
		Type: TypeMinIO,
		Creds: Credentials{
			AccessKey: "ak",
			SecretKey: "super-secret-sk",
			Endpoint:  "127.0.0.1:9000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Creds.SecretKey != "" {
		t.Fatalf("expected SecretKey cleared at rest, got %q", rec.Creds.SecretKey)
	}
	if len(rec.SecretEnc) == 0 {
		t.Fatal("expected SecretEnc ciphertext")
	}

	// Reload from store — must still resolve
	got, err := svc.Get("user-1", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Creds.SecretKey != "" {
		t.Fatalf("persisted SecretKey should be empty, got %q", got.Creds.SecretKey)
	}
	if len(got.SecretEnc) == 0 {
		t.Fatal("persisted SecretEnc missing")
	}

	resolved, _, err := svc.ResolveRecord("user-1", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SecretKey != "super-secret-sk" {
		t.Fatalf("ResolveRecord secret = %q", resolved.SecretKey)
	}
}

func TestCreatePlaintextWhenNoBox(t *testing.T) {
	svc := NewService(store.NewMemory())
	if svc.EncryptionEnabled() {
		t.Fatal("expected encryption off")
	}
	rec, err := svc.Create("user-1", CreateInput{
		Name: "s3",
		Type: TypeS3,
		Creds: Credentials{
			AccessKey: "ak",
			SecretKey: "sk-plain",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Creds.SecretKey != "sk-plain" {
		t.Fatalf("dev mode should keep plaintext, got %q", rec.Creds.SecretKey)
	}
	if len(rec.SecretEnc) != 0 {
		t.Fatal("dev mode should not set SecretEnc")
	}
	resolved, _, err := svc.ResolveRecord("user-1", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SecretKey != "sk-plain" {
		t.Fatal(resolved.SecretKey)
	}
}
