package policy

import "testing"

func TestLimiterBurst(t *testing.T) {
	l := NewLimiter(1, 3)
	if !l.Allow("u1") || !l.Allow("u1") || !l.Allow("u1") {
		t.Fatal("burst should allow 3")
	}
	if l.Allow("u1") {
		t.Fatal("4th should deny")
	}
	if !l.Allow("u2") {
		t.Fatal("other key independent")
	}
}
