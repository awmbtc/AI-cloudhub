package main

import "testing"

func TestJoinJobNote(t *testing.T) {
	if got := joinJobNote("cloned to /w/repo", "agent: exit 1"); got != "cloned to /w/repo | agent: exit 1" {
		t.Fatalf("got %q", got)
	}
	if got := joinJobNote("clone failed: x", "clone failed: x"); got != "clone failed: x" {
		t.Fatalf("dedupe got %q", got)
	}
	if got := joinJobNote("", "  ", "only"); got != "only" {
		t.Fatalf("empty skip got %q", got)
	}
}

func TestCloneStrictEnv(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_CLONE_STRICT", "")
	if cloneStrict() {
		t.Fatal("default off")
	}
	t.Setenv("AI_CLOUDHUB_CLONE_STRICT", "1")
	if !cloneStrict() {
		t.Fatal("want true for 1")
	}
	t.Setenv("AI_CLOUDHUB_CLONE_STRICT", "yes")
	if !cloneStrict() {
		t.Fatal("want true for yes")
	}
	t.Setenv("AI_CLOUDHUB_CLONE_STRICT", "0")
	if cloneStrict() {
		t.Fatal("want false for 0")
	}
}
