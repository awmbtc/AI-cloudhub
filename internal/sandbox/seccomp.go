package sandbox

import (
	"os"
	"strings"
)

// Enabled reports whether in-process seccomp should be applied.
// True when AI_CLOUDHUB_SECCOMP is 1, true, or yes (case-insensitive).
func Enabled() bool {
	return envTruthy("AI_CLOUDHUB_SECCOMP")
}

// Strict reports whether seccomp apply failure should abort the runner.
// True when AI_CLOUDHUB_SECCOMP_STRICT is 1, true, or yes (case-insensitive).
func Strict() bool {
	return envTruthy("AI_CLOUDHUB_SECCOMP_STRICT")
}

// NetDeny reports whether socket() is restricted to AF_UNIX only
// (blocks AF_INET / AF_INET6 at seccomp arg filter level).
//
// Enabled when:
//   - AI_CLOUDHUB_SECCOMP_PROFILE=netdeny, or
//   - AI_CLOUDHUB_SECCOMP_NET=deny|1|true|yes
func NetDeny() bool {
	if ProfileName() == "netdeny" {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_SECCOMP_NET")))
	switch v {
	case "1", "true", "yes", "deny", "off", "0":
		// "deny"/"1"/"true"/"yes" → net deny
		// "off"/"0"/"false" → explicit allow (handled below)
	}
	switch v {
	case "1", "true", "yes", "deny":
		return true
	default:
		return false
	}
}

// ProfileName returns the allowlist profile: "default", "strict", or "netdeny".
// AI_CLOUDHUB_SECCOMP_PROFILE=strict|netdeny|default (default when unset/unknown).
//
// netdeny implies the strict syscall set plus AF_UNIX-only sockets.
func ProfileName() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_SECCOMP_PROFILE")))
	switch v {
	case "strict":
		return "strict"
	case "netdeny", "net-deny", "network_deny":
		return "netdeny"
	default:
		return "default"
	}
}

// EffectiveProfile returns the profile label used for logging (includes net overlay).
// e.g. "strict+netdeny" when PROFILE=strict and SECCOMP_NET=deny.
func EffectiveProfile() string {
	p := ProfileName()
	if p == "netdeny" {
		return "netdeny"
	}
	if NetDeny() {
		return p + "+netdeny"
	}
	return p
}

func envTruthy(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
