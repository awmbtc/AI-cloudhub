package store

import (
	"testing"
	"time"
)

func TestCountWebhookOutboxMemory(t *testing.T) {
	mem := NewMemory()
	now := time.Now().UTC()
	rows := []*WebhookOutbox{
		{ID: "e1", JobID: "j1", UserID: "u1", Event: "job.succeeded", Status: "pending", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now},
		{ID: "e2", JobID: "j2", UserID: "u1", Event: "job.failed", Status: "pending", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now},
		{ID: "e3", JobID: "j3", UserID: "u1", Event: "job.succeeded", Status: "delivered", CreatedAt: now, UpdatedAt: now, DeliveredAt: now},
		{ID: "e4", JobID: "j4", UserID: "u2", Event: "job.cancelled", Status: "dead", CreatedAt: now, UpdatedAt: now},
		{ID: "e5", JobID: "j5", UserID: "u2", Event: "job.failed", Status: "dead", CreatedAt: now, UpdatedAt: now},
	}
	for _, r := range rows {
		if err := mem.EnqueueWebhookOutbox(r); err != nil {
			t.Fatalf("enqueue %s: %v", r.ID, err)
		}
	}

	c, err := mem.CountWebhookOutbox()
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("nil counts")
	}
	if c.Pending != 2 {
		t.Fatalf("pending: got %d want 2", c.Pending)
	}
	if c.Delivered != 1 {
		t.Fatalf("delivered: got %d want 1", c.Delivered)
	}
	if c.Dead != 2 {
		t.Fatalf("dead: got %d want 2", c.Dead)
	}
	if c.Total != 5 {
		t.Fatalf("total: got %d want 5", c.Total)
	}

	// Empty store
	empty := NewMemory()
	c0, err := empty.CountWebhookOutbox()
	if err != nil {
		t.Fatal(err)
	}
	if c0.Pending != 0 || c0.Delivered != 0 || c0.Dead != 0 || c0.Total != 0 {
		t.Fatalf("empty want zeros got %+v", c0)
	}
}
