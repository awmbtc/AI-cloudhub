package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLiteCRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	if err := st.CreateUser(&User{ID: "u1", Username: "alice", Password: "secret"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	u, err := st.GetUserByUsername("alice")
	if err != nil || u.Password != "secret" {
		t.Fatalf("get user: %+v %v", u, err)
	}
	if err := st.UpdateUserPassword("u1", "$2a$10$dummyhashforupgradetestxx"); err != nil {
		t.Fatalf("update password: %v", err)
	}
	u, err = st.GetUserByUsername("alice")
	if err != nil || u.Password != "$2a$10$dummyhashforupgradetestxx" {
		t.Fatalf("get user after password update: %+v %v", u, err)
	}
	if err := st.UpdateUserPassword("missing", "x"); err == nil {
		t.Fatal("expected update missing user to fail")
	}

	creds := []byte(`{"access_key":"ak","secret_key":"sk"}`)
	if err := st.CreateProvider(&Provider{
		ID: "p1", UserID: "u1", Name: "minio", Type: "minio",
		CredsJSON: creds, EndpointPublic: "127.0.0.1:9000", Region: "us-east-1",
	}); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	p, err := st.GetProvider("u1", "p1")
	if err != nil || string(p.CredsJSON) != string(creds) {
		t.Fatalf("get provider: %+v %v", p, err)
	}
	list, err := st.ListProviders("u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list providers: %v %v", list, err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.CreateDrive(&Drive{
		ID: "d1", UserID: "u1", Name: "ws", ProviderID: "p1",
		Bucket: "b", Prefix: "pre", MountPoint: "/workspace",
		Region: "us-west-2", CreatedAt: now,
	}); err != nil {
		t.Fatalf("create drive: %v", err)
	}
	d, err := st.GetDrive("u1", "d1")
	if err != nil || d.Bucket != "b" || d.Region != "us-west-2" {
		t.Fatalf("get drive: %+v %v", d, err)
	}

	if err := st.CreateBinding(&Binding{
		ID: "b1", UserID: "u1", DriveID: "d1", DeviceID: "laptop",
		MountPoint: "/workspace", Mode: "mount", Desired: "mounted", Actual: "unknown",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	b, err := st.GetBinding("u1", "b1")
	if err != nil || b.Desired != "mounted" {
		t.Fatalf("get binding: %+v %v", b, err)
	}
	b.Actual = "mounted"
	b.UpdatedAt = time.Now().UTC()
	if err := st.UpdateBinding(b); err != nil {
		t.Fatalf("update binding: %v", err)
	}
	b2, err := st.GetBinding("u1", "b1")
	if err != nil || b2.Actual != "mounted" {
		t.Fatalf("after update: %+v %v", b2, err)
	}

	// reopen — durable
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	if _, err := st2.GetUserByUsername("alice"); err != nil {
		t.Fatalf("persist user: %v", err)
	}
	if _, err := st2.GetProvider("u1", "p1"); err != nil {
		t.Fatalf("persist provider: %v", err)
	}
	if _, err := st2.GetDrive("u1", "d1"); err != nil {
		t.Fatalf("persist drive: %v", err)
	}
	if _, err := st2.GetBinding("u1", "b1"); err != nil {
		t.Fatalf("persist binding: %v", err)
	}

	if err := st2.UpsertDevice(&Device{
		ID: "dev1", UserID: "u1", Name: "laptop", LastSeen: now,
	}); err != nil {
		t.Fatalf("upsert device: %v", err)
	}
	dev, err := st2.GetDevice("u1", "dev1")
	if err != nil || dev.Name != "laptop" {
		t.Fatalf("get device: %+v %v", dev, err)
	}
	devs, err := st2.ListDevices("u1")
	if err != nil || len(devs) != 1 {
		t.Fatalf("list devices: %v %v", devs, err)
	}
	// re-register updates last_seen / name
	later := now.Add(time.Minute)
	if err := st2.UpsertDevice(&Device{
		ID: "dev1", UserID: "u1", Name: "laptop-2", LastSeen: later,
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	dev2, err := st2.GetDevice("u1", "dev1")
	if err != nil || dev2.Name != "laptop-2" {
		t.Fatalf("after re-upsert: %+v %v", dev2, err)
	}

	if err := st2.DeleteDrive("u1", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := st2.DeleteProvider("u1", "p1"); err != nil {
		t.Fatal(err)
	}
}

func TestDriveRegionMigrateExistingDB(t *testing.T) {
	// Simulate a pre-region schema DB and ensure Open/Migrate adds the column.
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Create legacy schema without region via raw modernc driver.
	db, err := openRaw(path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	_, err = db.Exec(`
CREATE TABLE drives (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  bucket TEXT NOT NULL,
  prefix TEXT,
  mount_point TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO drives (id, user_id, name, provider_id, bucket, prefix, mount_point, created_at)
VALUES ('d-legacy', 'u1', 'old', 'p1', 'b', '', '/workspace', '2020-01-01T00:00:00Z');
`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed legacy: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open migrate: %v", err)
	}
	defer st.Close()

	// Existing row should load with empty region.
	d, err := st.GetDrive("u1", "d-legacy")
	if err != nil {
		t.Fatalf("get legacy drive: %v", err)
	}
	if d.Region != "" {
		t.Fatalf("expected empty region, got %q", d.Region)
	}

	// New writes with region must work after migration.
	now := time.Now().UTC()
	if err := st.CreateDrive(&Drive{
		ID: "d-new", UserID: "u1", Name: "n", ProviderID: "p1",
		Bucket: "b2", MountPoint: "/workspace", Region: "ap-southeast-1", CreatedAt: now,
	}); err != nil {
		t.Fatalf("create with region: %v", err)
	}
	d2, err := st.GetDrive("u1", "d-new")
	if err != nil || d2.Region != "ap-southeast-1" {
		t.Fatalf("get new drive: %+v %v", d2, err)
	}

	// Second Open (idempotent migrate) must not fail.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after migrate: %v", err)
	}
	defer st2.Close()
	if _, err := st2.GetDrive("u1", "d-new"); err != nil {
		t.Fatalf("persist after remigrate: %v", err)
	}
}

func openRaw(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
}

func TestMemoryCRUD(t *testing.T) {
	st := NewMemory()
	if err := st.CreateUser(&User{ID: "u1", Username: "bob", Password: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetUserByUsername("bob"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(&User{ID: "u2", Username: "bob", Password: "y"}); err == nil {
		t.Fatal("expected duplicate username error")
	}
	now := time.Now().UTC()
	if err := st.UpsertDevice(&Device{ID: "d1", UserID: "u1", Name: "n", LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDevice("u1", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDevice(&Device{ID: "d1", UserID: "other", Name: "x", LastSeen: now}); err == nil {
		t.Fatal("expected device id conflict")
	}
	if err := st.CreateDrive(&Drive{
		ID: "drv1", UserID: "u1", Name: "ws", ProviderID: "p1",
		Bucket: "b", MountPoint: "/workspace", Region: "eu-central-1", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	drv, err := st.GetDrive("u1", "drv1")
	if err != nil || drv.Region != "eu-central-1" {
		t.Fatalf("memory drive region: %+v %v", drv, err)
	}
}
