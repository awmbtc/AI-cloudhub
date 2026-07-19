package store

import (
	"testing"
	"time"
)

func TestMemoryListAuditFilter(t *testing.T) {
	m := NewMemory()
	_ = m.AppendAudit(&AuditEvent{ID: "1", UserID: "a", Action: "x", CreatedAt: time.Now()})
	_ = m.AppendAudit(&AuditEvent{ID: "2", UserID: "b", Action: "y", CreatedAt: time.Now()})
	_ = m.AppendAudit(&AuditEvent{ID: "3", UserID: "a", Action: "z", CreatedAt: time.Now()})

	all, err := m.ListAudit(AuditFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all: %d", len(all))
	}
	// newest first
	if all[0].ID != "3" {
		t.Fatalf("newest first want 3 got %s", all[0].ID)
	}

	aOnly, err := m.ListAudit(AuditFilter{Limit: 100, UserID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(aOnly) != 2 {
		t.Fatalf("user a: %d", len(aOnly))
	}
	for _, e := range aOnly {
		if e.UserID != "a" {
			t.Fatalf("leak: %+v", e)
		}
	}

	lim, err := m.ListAudit(AuditFilter{Limit: 1, UserID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lim) != 1 || lim[0].ID != "3" {
		t.Fatalf("limit+filter: %+v", lim)
	}

	byAct, err := m.ListAudit(AuditFilter{Limit: 10, Action: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAct) != 1 || byAct[0].ID != "2" {
		t.Fatalf("action filter: %+v", byAct)
	}
}
