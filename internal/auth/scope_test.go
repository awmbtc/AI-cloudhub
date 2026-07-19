package auth

import "testing"

func TestHasScopeHumanUnrestricted(t *testing.T) {
	if !HasScope("", nil, ScopeDriveWrite) {
		t.Fatal("human should pass")
	}
}

func TestHasScopeAgent(t *testing.T) {
	scopes := []string{ScopeDriveRead}
	if !HasScope("a1", scopes, ScopeDriveRead) {
		t.Fatal("should have read")
	}
	if HasScope("a1", scopes, ScopeDriveWrite) {
		t.Fatal("should not have write")
	}
}

func TestNormalizeScopes(t *testing.T) {
	got := NormalizeScopes([]string{" drive.read ", "drive.read", "", "job.run"})
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
}
