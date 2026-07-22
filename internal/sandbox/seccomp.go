package sandbox

import (
	"os"
	"strings"
)

// Enabled reports whether in-process seccomp should be applied.
// True when AI_CLOUDHUB_SECCOMP is 1, true, or yes (case-insensitive).
func Enabled() bool {
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_SECCOMP"))
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// Strict reports whether seccomp apply failure should abort the runner.
// True when AI_CLOUDHUB_SECCOMP_STRICT is 1, true, or yes (case-insensitive).
func Strict() bool {
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_SECCOMP_STRICT"))
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// ProfileName returns the allowlist profile: "default" or "strict".
// AI_CLOUDHUB_SECCOMP_PROFILE=strict|default (default when unset/unknown).
func ProfileName() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_SECCOMP_PROFILE")))
	switch v {
	case "strict":
		return "strict"
	default:
		return "default"
	}
}
