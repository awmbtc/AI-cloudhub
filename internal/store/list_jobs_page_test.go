package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestListJobsPageLabelsSQLite(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	j := &Job{
		ID: "j1", UserID: "u1", DriveID: "d", Mode: "mount",
		CommandJSON: []byte(`["true"]`), Status: "pending",
		LabelsJSON: []byte(`{"env":"prod","team":"ml"}`),
		CreatedAt:  now, UpdatedAt: now,
	}
	if err := st.CreateJob(j); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListJobsPage(JobListFilter{
		UserID: "u1",
		Labels: map[string]string{"env": "prod"},
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d err labels: %#v", len(list), list)
	}
	list2, err := st.ListJobsPage(JobListFilter{
		UserID: "u1",
		Labels: map[string]string{"env": "prod", "team": "ml"},
		Limit:  10,
	})
	if err != nil || len(list2) != 1 {
		t.Fatalf("and match: %v n=%d", err, len(list2))
	}
	list3, err := st.ListJobsPage(JobListFilter{
		UserID: "u1",
		Labels: map[string]string{"env": "dev"},
		Limit:  10,
	})
	if err != nil || len(list3) != 0 {
		t.Fatalf("no match: %v n=%d", err, len(list3))
	}
	// sibling job with empty labels must not break the query
	j2 := &Job{
		ID: "j2", UserID: "u1", DriveID: "d", Mode: "mount",
		CommandJSON: []byte(`["true"]`), Status: "pending",
		LabelsJSON: nil, CreatedAt: now.Add(time.Second), UpdatedAt: now,
	}
	if err := st.CreateJob(j2); err != nil {
		t.Fatal(err)
	}
	list4, err := st.ListJobsPage(JobListFilter{
		UserID: "u1",
		Labels: map[string]string{"env": "prod"},
		Limit:  10,
	})
	if err != nil || len(list4) != 1 {
		t.Fatalf("with empty-label sibling: %v n=%d", err, len(list4))
	}
}
