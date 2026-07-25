package runtimeenv

import (
	"runtime"
	"strings"
	"testing"
)

func TestWindowsInstallHint(t *testing.T) {
	h := windowsInstallHint()
	if !strings.Contains(h, "install-deps.ps1") {
		t.Fatalf("%q", h)
	}
	if !strings.Contains(h, "CheckOnly") {
		t.Fatalf("want CheckOnly in hint: %q", h)
	}
}

func TestIsWindowsDriveLetter(t *testing.T) {
	cases := map[string]bool{
		"G:":     true,
		"G:\\":   true,
		"g:/":    true,
		"C:":     true,
		"/workspace": false,
		`C:\Users\x`: false,
		"":       false,
		"GG:":    false,
	}
	for in, want := range cases {
		if got := IsWindowsDriveLetter(in); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestWindowsRcloneCandidatesNonEmptyOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only path list semantics")
	}
	if len(windowsRcloneCandidates()) == 0 {
		t.Fatal("expected candidates")
	}
}

func TestCheckDoesNotPanic(t *testing.T) {
	r := Check()
	if r.OS == "" || r.Arch == "" {
		t.Fatalf("%+v", r)
	}
}
