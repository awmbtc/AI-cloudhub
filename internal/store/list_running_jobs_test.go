package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestListRunningJobsMemory(t *testing.T) {
	m := NewMemory()
	now := time.Now().UTC()
	jobs := []*Job{
		{ID: "p1", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "pending", CreatedAt: now, UpdatedAt: now},
		{ID: "r1", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "running", CreatedAt: now.Add(time.Second), UpdatedAt: now},
		{ID: "s1", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "succeeded", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now},
		{ID: "r0", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "running", CreatedAt: now.Add(-time.Second), UpdatedAt: now},
		{ID: "r2", UserID: "u2", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "running", CreatedAt: now, UpdatedAt: now},
	}
	for _, j := range jobs {
		if err := m.CreateJob(j); err != nil {
			t.Fatal(err)
		}
	}
	list, err := m.ListRunningJobs("u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 running for u1, got %d", len(list))
	}
	// created_at ASC: r0 then r1
	if list[0].ID != "r0" || list[1].ID != "r1" {
		t.Fatalf("order: got %s, %s want r0, r1", list[0].ID, list[1].ID)
	}
	all, err := m.ListJobs("u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("ListJobs should still return all statuses: %d", len(all))
	}
}

func TestListRunningJobsSQLite(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	jobs := []*Job{
		{ID: "p1", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "pending", CreatedAt: now, UpdatedAt: now},
		{ID: "r1", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "running", CreatedAt: now.Add(time.Second), UpdatedAt: now},
		{ID: "f1", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "failed", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now},
		{ID: "r0", UserID: "u1", DriveID: "d", Mode: "m", CommandJSON: []byte(`["x"]`), Status: "running", CreatedAt: now.Add(-time.Second), UpdatedAt: now},
	}
	for _, j := range jobs {
		if err := st.CreateJob(j); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.ListRunningJobs("u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 running, got %d", len(list))
	}
	if list[0].ID != "r0" || list[1].ID != "r1" {
		t.Fatalf("order: got %s, %s want r0, r1", list[0].ID, list[1].ID)
	}
}
