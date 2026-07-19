package store

import "testing"

func TestIsPostgresDSN(t *testing.T) {
	if !IsPostgresDSN("postgres://u:p@localhost:5432/db") {
		t.Fatal("expected true")
	}
	if !IsPostgresDSN("postgresql://localhost/db") {
		t.Fatal("expected true")
	}
	if IsPostgresDSN("./data/ai.db") || IsPostgresDSN("memory") {
		t.Fatal("expected false")
	}
}
