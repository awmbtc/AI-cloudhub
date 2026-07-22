package drive

import (
	"strings"
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func testDriveSvc(t *testing.T) (*Service, string, string) {
	t.Helper()
	st := store.NewMemory()
	ps := provider.NewService(st)
	rec, err := ps.Create("u1", provider.CreateInput{
		Name: "minio",
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
	ds := NewService(ps, st)
	m, err := ds.Create("u1", CreateInput{
		Name:       "ws",
		ProviderID: rec.ID,
		Bucket:     "b1",
		Prefix:     "proj/a",
		MountPoint: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ds, "u1", m.ID
}

func TestResolveObjectKeyPrefix(t *testing.T) {
	ds, uid, did := testDriveSvc(t)

	// Relative key under prefix
	_, resolved, k, err := ds.resolveObjectKey(uid, did, "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if k != "proj/a/file.txt" {
		t.Fatalf("key = %q", k)
	}
	if resolved == nil || resolved.AccessKey != "ak" {
		t.Fatalf("resolved = %+v", resolved)
	}

	// Already prefixed
	_, _, k2, err := ds.resolveObjectKey(uid, did, "proj/a/nested/x")
	if err != nil {
		t.Fatal(err)
	}
	if k2 != "proj/a/nested/x" {
		t.Fatalf("k2 = %q", k2)
	}

	// Exact prefix as key
	_, _, k3, err := ds.resolveObjectKey(uid, did, "proj/a")
	if err != nil {
		t.Fatal(err)
	}
	if k3 != "proj/a" {
		t.Fatalf("k3 = %q", k3)
	}
}

func TestResolveObjectKeyRejects(t *testing.T) {
	ds, uid, did := testDriveSvc(t)
	if _, _, _, err := ds.resolveObjectKey(uid, did, ""); err == nil {
		t.Fatal("empty key should fail")
	}
	if _, _, _, err := ds.resolveObjectKey(uid, did, "../etc/passwd"); err == nil {
		t.Fatal(".. should fail")
	}
	if _, _, _, err := ds.resolveObjectKey(uid, did, "ok/../bad"); err == nil {
		t.Fatal("embedded .. should fail")
	}
}

func TestObjectVersionHintNoS3Call(t *testing.T) {
	ds, uid, did := testDriveSvc(t)
	out, err := ds.ObjectVersionHint(uid, did, "data.bin", "ver-1")
	if err != nil {
		t.Fatal(err)
	}
	if out["key"] != "proj/a/data.bin" {
		t.Fatalf("%v", out["key"])
	}
	hint, _ := out["hint"].(string)
	if !strings.Contains(hint, "ver-1") || !strings.Contains(hint, "get-object") {
		t.Fatalf("hint = %q", hint)
	}
}

func TestObjectRestorePlanStructure(t *testing.T) {
	ds, uid, did := testDriveSvc(t)
	// Presign will fail (no real MinIO) but plan should still return CLI path.
	out, err := ds.ObjectRestorePlan(uid, did, "x", "v9", 10)
	if err != nil {
		t.Fatal(err)
	}
	if out["api_restore"] == nil || out["cli_hint"] == nil {
		t.Fatalf("%v", out)
	}
	// either presign or presign_error present
	if out["presign"] == nil && out["presign_error"] == nil {
		t.Fatalf("expected presign or error: %v", out)
	}
}
