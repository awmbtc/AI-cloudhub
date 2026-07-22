package s3store

import (
	"strings"
	"testing"
)

func TestRestoreHint(t *testing.T) {
	h := RestoreHint("s3.example:9000", "bkt", "p/k", "vid", true)
	if !strings.Contains(h, "--version-id vid") {
		t.Fatal(h)
	}
	h2 := RestoreHint("localhost:9000", "b", "k", "", false)
	if !strings.Contains(h2, "http://localhost:9000/b/k") {
		t.Fatal(h2)
	}
}

func TestCopyVersionToCurrentRequiresVersion(t *testing.T) {
	// No live client — zero-value Store only used to exercise guard.
	s := &Store{}
	err := s.CopyVersionToCurrent(nil, "b", "k", "")
	if err == nil || !strings.Contains(err.Error(), "version_id") {
		t.Fatalf("err = %v", err)
	}
}
