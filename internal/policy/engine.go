// Package policy — Rate limits, quotas, and Policy Engine v0 (ROADMAP stage B).
package policy

import (
	"fmt"
	"strings"
)

// Actions for Policy Engine v0.
const (
	ActionDriveRead    = "drive.read"
	ActionDriveWrite   = "drive.write"
	ActionDriveSession = "drive.session"
	ActionJobRun       = "job.run"
	ActionProviderRead = "provider.read"
	ActionPathRead     = "path.read"
	ActionPathWrite    = "path.write"
)

// Request is an authorization request.
type Request struct {
	// AgentID empty = human principal (allow all resource checks at this layer).
	AgentID string
	// AllowedDriveIDs from agent record; empty = all drives of owner.
	AllowedDriveIDs []string
	// ReadPrefixes / WritePrefixes for path checks.
	ReadPrefixes  []string
	WritePrefixes []string
	// Scopes from token.
	Scopes []string
	// Action e.g. drive.session
	Action string
	// DriveID optional resource
	DriveID string
	// Path optional relative/abs path under workspace
	Path string
}

// Decision is allow/deny.
type Decision struct {
	Allow  bool
	Reason string
}

// Engine is Policy v0 (JSON-friendly rules later; now structural checks).
type Engine struct{}

// NewEngine returns Policy Engine v0.
func NewEngine() *Engine { return &Engine{} }

// Evaluate applies capability rules for agent principals.
// Humans (AgentID empty) always allow.
func (e *Engine) Evaluate(req Request) Decision {
	if req.AgentID == "" {
		return Decision{Allow: true, Reason: "human"}
	}
	// Scope gate (map action → scope)
	needScope := scopeForAction(req.Action)
	if needScope != "" && !hasScope(req.Scopes, needScope) {
		return Decision{Allow: false, Reason: "missing scope: " + needScope}
	}
	// Drive allowlist
	if req.DriveID != "" && len(req.AllowedDriveIDs) > 0 {
		if !contains(req.AllowedDriveIDs, req.DriveID) {
			return Decision{Allow: false, Reason: "drive not allowed for agent"}
		}
	}
	// Path prefixes
	if req.Path != "" {
		switch req.Action {
		case ActionPathWrite, ActionDriveWrite:
			if len(req.WritePrefixes) > 0 && !prefixOK(req.Path, req.WritePrefixes) {
				return Decision{Allow: false, Reason: "path not in write_prefixes"}
			}
		case ActionPathRead, ActionDriveRead, ActionDriveSession:
			if len(req.ReadPrefixes) > 0 && !prefixOK(req.Path, req.ReadPrefixes) {
				return Decision{Allow: false, Reason: "path not in read_prefixes"}
			}
		}
	}
	return Decision{Allow: true, Reason: "ok"}
}

// CanAccessDrive is a convenience for B1.
func CanAccessDrive(agentID string, allowed []string, driveID string) error {
	d := NewEngine().Evaluate(Request{
		AgentID:         agentID,
		AllowedDriveIDs: allowed,
		DriveID:         driveID,
		Action:          ActionDriveRead,
		Scopes:          []string{"drive.read", "drive.write"}, // scope checked elsewhere
	})
	// Only check drive list when agent set
	if agentID == "" {
		return nil
	}
	if driveID == "" {
		return nil
	}
	if len(allowed) == 0 {
		return nil // all drives
	}
	if !contains(allowed, driveID) {
		return fmt.Errorf("drive not allowed for agent")
	}
	_ = d
	return nil
}

func scopeForAction(action string) string {
	switch action {
	case ActionDriveRead, ActionDriveSession, ActionPathRead:
		return "drive.read"
	case ActionDriveWrite, ActionPathWrite:
		return "drive.write"
	case ActionJobRun:
		return "job.run"
	case ActionProviderRead:
		return "provider.read"
	default:
		return ""
	}
}

func hasScope(scopes []string, need string) bool {
	for _, s := range scopes {
		if s == need {
			return true
		}
	}
	// drive.write implies drive.read for session?
	if need == "drive.read" {
		for _, s := range scopes {
			if s == "drive.write" {
				return true
			}
		}
	}
	return false
}

func contains(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}

func prefixOK(path string, prefixes []string) bool {
	path = strings.TrimSpace(path)
	// normalize leading slash
	p := strings.TrimPrefix(path, "/")
	for _, pref := range prefixes {
		pref = strings.TrimSpace(pref)
		pref = strings.TrimPrefix(pref, "/")
		if pref == "" || pref == "*" {
			return true
		}
		if p == pref || strings.HasPrefix(p, pref+"/") || strings.HasPrefix(path, pref) {
			return true
		}
		// also allow if path is under /workspace/pref
		if strings.Contains(path, "/"+pref) || strings.HasSuffix(path, pref) {
			return true
		}
	}
	return false
}
