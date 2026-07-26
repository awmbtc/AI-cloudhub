package main

import (
	"os"
	"testing"
)

func TestParseRunnerMode(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_RUNNER_MODE", "")
	mode, rest := parseRunnerMode(nil)
	if mode != runnerModeRun || len(rest) != 0 {
		t.Fatalf("default: %q %v", mode, rest)
	}
	mode, rest = parseRunnerMode([]string{"check"})
	if mode != runnerModeCheck {
		t.Fatalf("check: %q", mode)
	}
	mode, rest = parseRunnerMode([]string{"dry-run", "x"})
	if mode != runnerModeDryRun || len(rest) != 1 {
		t.Fatalf("dry-run: %q %v", mode, rest)
	}
	mode, _ = parseRunnerMode([]string{"worker"})
	if mode != runnerModeWorker {
		t.Fatalf("worker: %q", mode)
	}
	mode, rest = parseRunnerMode([]string{"--", "echo", "hi"})
	if mode != runnerModeRun || len(rest) != 3 {
		t.Fatalf("legacy args: %q %v", mode, rest)
	}
	t.Setenv("AI_CLOUDHUB_RUNNER_MODE", "check")
	mode, _ = parseRunnerMode([]string{"worker"})
	if mode != runnerModeCheck {
		t.Fatalf("env override: %q", mode)
	}
	_ = os.Unsetenv("AI_CLOUDHUB_RUNNER_MODE")
}

func TestTruncate(t *testing.T) {
	if truncate("ab", 5) != "ab" {
		t.Fatal("short")
	}
	if truncate("abcdef", 3) != "abc…" {
		t.Fatalf("%q", truncate("abcdef", 3))
	}
}
