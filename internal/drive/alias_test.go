package drive

import (
	"strings"
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestNormalizeAlias(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"  a  ", "A", true},
		{"work", "WORK", true},
		{"Work_1", "WORK_1", true},
		{"B-2", "B-2", true},
		{"1bad", "", false},
		{"-no", "", false},
		{strings.Repeat("a", 17), "", false},
		{"has space", "", false},
	}
	for _, tc := range cases {
		got, err := NormalizeAlias(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Fatalf("NormalizeAlias(%q)=%q,%v want %q,nil", tc.in, got, err, tc.want)
			}
		} else if err == nil {
			t.Fatalf("NormalizeAlias(%q) expected error, got %q", tc.in, got)
		}
	}
}

func TestCreateWithAliasAndDuplicate(t *testing.T) {
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
		Name:       "R2 workspace",
		Alias:      "a",
		ProviderID: rec.ID,
		Bucket:     "b1",
		MountPoint: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Alias != "A" {
		t.Fatalf("alias = %q, want A", m.Alias)
	}

	_, err = ds.Create("u1", CreateInput{
		Name:       "other",
		Alias:      "A",
		ProviderID: rec.ID,
		Bucket:     "b2",
		MountPoint: "/workspace2",
	})
	if err == nil || !strings.Contains(err.Error(), "alias already in use") {
		t.Fatalf("expected duplicate alias error, got %v", err)
	}

	// different user may reuse alias
	if err := st.CreateUser(&store.User{ID: "u2", Username: "u2", Password: "x"}); err != nil {
		// memory CreateUser may not need user row — ignore if unused
		_ = err
	}
	rec2, err := ps.Create("u2", provider.CreateInput{
		Name:  "minio2",
		Type:  provider.TypeMinIO,
		Creds: provider.Credentials{AccessKey: "ak", SecretKey: "sk", Endpoint: "127.0.0.1:9000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := ds.Create("u2", CreateInput{
		Name: "ws", Alias: "a", ProviderID: rec2.ID, Bucket: "b3", MountPoint: "/workspace",
	})
	if err != nil {
		t.Fatalf("other user should reuse alias: %v", err)
	}
	if m2.Alias != "A" {
		t.Fatalf("u2 alias = %q", m2.Alias)
	}
}

func TestResolveByAlias(t *testing.T) {
	ds, uid, driveID := testDriveSvc(t)
	// set alias via Update
	m, err := ds.Update(uid, driveID, UpdateInput{Alias: "work", SetAlias: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Alias != "WORK" {
		t.Fatalf("alias = %q", m.Alias)
	}
	got, err := ds.Resolve(uid, "work")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != driveID || got.Alias != "WORK" {
		t.Fatalf("resolve: %+v", got)
	}
	got2, err := ds.GetByAlias(uid, "WORK")
	if err != nil || got2.ID != driveID {
		t.Fatalf("GetByAlias: %+v %v", got2, err)
	}
	// name fallback
	got3, err := ds.Resolve(uid, "ws")
	if err != nil || got3.ID != driveID {
		t.Fatalf("resolve by name: %+v %v", got3, err)
	}
}

func TestUpdateAliasConflict(t *testing.T) {
	st := store.NewMemory()
	ps := provider.NewService(st)
	rec, err := ps.Create("u1", provider.CreateInput{
		Name: "minio", Type: provider.TypeMinIO,
		Creds: provider.Credentials{AccessKey: "ak", SecretKey: "sk", Endpoint: "127.0.0.1:9000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ds := NewService(ps, st)
	a, err := ds.Create("u1", CreateInput{Name: "one", Alias: "A", ProviderID: rec.ID, Bucket: "b1", MountPoint: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ds.Create("u1", CreateInput{Name: "two", ProviderID: rec.ID, Bucket: "b2", MountPoint: "/b"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ds.Update("u1", b.ID, UpdateInput{Alias: "a", SetAlias: true})
	if err == nil || !strings.Contains(err.Error(), "alias already in use") {
		t.Fatalf("expected conflict, got %v (drive a=%s)", err, a.ID)
	}
}
