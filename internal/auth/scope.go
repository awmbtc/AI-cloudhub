package auth

import "strings"

// Well-known scopes for Agent capability tokens (ROADMAP-2.0).
const (
	ScopeDriveRead     = "drive.read"
	ScopeDriveWrite    = "drive.write"
	ScopeJobRun        = "job.run"
	ScopeProviderRead  = "provider.read"
	ScopeProviderWrite = "provider.write"
)

// KnownScopes is the v0 vocabulary.
var KnownScopes = []string{
	ScopeDriveRead,
	ScopeDriveWrite,
	ScopeJobRun,
	ScopeProviderRead,
	ScopeProviderWrite,
}

// IsKnownScope reports whether s is in the v0 vocabulary.
func IsKnownScope(s string) bool {
	s = strings.TrimSpace(s)
	for _, k := range KnownScopes {
		if k == s {
			return true
		}
	}
	return false
}

// NormalizeScopes trims, dedupes, drops empties.
func NormalizeScopes(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// HasScope returns true if the principal is a human (no agent) or scopes contain need.
// Human tokens (AgentID empty) are unrestricted for API scope checks.
func HasScope(agentID string, scopes []string, need string) bool {
	if agentID == "" {
		return true // human session
	}
	need = strings.TrimSpace(need)
	for _, s := range scopes {
		if s == need {
			return true
		}
	}
	return false
}

// HasAnyScope returns true if human or any of needs is present.
func HasAnyScope(agentID string, scopes []string, needs ...string) bool {
	if agentID == "" {
		return true
	}
	for _, n := range needs {
		if HasScope(agentID, scopes, n) {
			return true
		}
	}
	return false
}
