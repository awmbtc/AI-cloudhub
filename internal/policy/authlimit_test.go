package policy

import (
	"testing"
	"time"
)

func TestAuthLimiterBurst(t *testing.T) {
	a := NewAuthLimiter(60, 3) // 1/s, burst 3
	if !a.Allow("ip1") || !a.Allow("ip1") || !a.Allow("ip1") {
		t.Fatal("expected first 3 allowed")
	}
	if a.Allow("ip1") {
		t.Fatal("expected 4th denied")
	}
	// different key ok
	if !a.Allow("ip2") {
		t.Fatal("other key should allow")
	}
}

func TestFailureTracker(t *testing.T) {
	ft := NewFailureTracker(3, time.Minute)
	if ft.Locked("u") {
		t.Fatal("not locked yet")
	}
	if ft.Fail("u") || ft.Fail("u") {
		t.Fatal("should not lock before max")
	}
	if !ft.Fail("u") {
		t.Fatal("third fail should lock")
	}
	if !ft.Locked("u") {
		t.Fatal("should be locked")
	}
	ft.Clear("u")
	if ft.Locked("u") {
		t.Fatal("cleared")
	}
}
