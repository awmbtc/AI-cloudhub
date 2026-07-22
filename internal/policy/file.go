package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Document is an external policy file (AI_CLOUDHUB_POLICY_FILE).
// Version 1: ordered rules after built-in scope/drive/path checks.
//
// Schema (JSON):
//
//	{
//	  "version": 1,
//	  "mode": "enforce",          // enforce | observe (observe never denies)
//	  "rules": [
//	    {
//	      "id": "block-ssh",
//	      "effect": "deny",
//	      "principals": ["agent"],
//	      "actions": ["path.read", "path.write", "drive.read", "drive.write"],
//	      "path_deny_prefixes": [".ssh", ".env"],
//	      "reason": "secret paths blocked for agents"
//	    }
//	  ]
//	}
type Document struct {
	Version int    `json:"version"`
	Mode    string `json:"mode"` // enforce (default) | observe
	Rules   []Rule `json:"rules"`
}

// Rule is one ordered policy rule. Empty match fields mean "any".
// A rule matches only when ALL specified constraints match the request.
type Rule struct {
	ID     string `json:"id"`
	Effect string `json:"effect"` // deny | allow (default deny if unknown)
	// Principals: agent | human | any (empty = any)
	Principals []string `json:"principals"`
	// Actions: e.g. drive.read, drive.write, drive.session, job.run, path.read, path.write
	Actions []string `json:"actions"`
	// AgentIDs: exact agent id match; empty = any agent (when principal is agent)
	AgentIDs []string `json:"agent_ids"`
	// DriveIDs: exact drive id; empty = any
	DriveIDs []string `json:"drive_ids"`
	// PathDenyPrefixes: if set, rule matches when req.Path is under any of these prefixes
	// (relative or basename-style, same semantics as agent path prefixes).
	PathDenyPrefixes []string `json:"path_deny_prefixes"`
	// PathAllowPrefixes: if set, rule matches when path is under one of these (for allow rules).
	PathAllowPrefixes []string `json:"path_allow_prefixes"`
	// RequireScopes: when rule matches, all listed scopes must be present (else deny).
	// Useful for effect=allow gates or effect=deny with empty path (scope gate).
	RequireScopes []string `json:"require_scopes"`
	// Reason shown on deny / observe note.
	Reason string `json:"reason"`
}

// ParseDocument unmarshals and validates a policy document.
func ParseDocument(raw []byte) (*Document, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &Document{Version: 1, Mode: "enforce"}, nil
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("policy json: %w", err)
	}
	if doc.Version == 0 {
		doc.Version = 1
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("unsupported policy version %d (want 1)", doc.Version)
	}
	mode := strings.ToLower(strings.TrimSpace(doc.Mode))
	if mode == "" {
		mode = "enforce"
	}
	if mode != "enforce" && mode != "observe" {
		return nil, fmt.Errorf("policy mode %q (want enforce|observe)", doc.Mode)
	}
	doc.Mode = mode
	for i := range doc.Rules {
		r := &doc.Rules[i]
		eff := strings.ToLower(strings.TrimSpace(r.Effect))
		if eff == "" {
			eff = "deny"
		}
		if eff != "deny" && eff != "allow" {
			return nil, fmt.Errorf("rule %q: effect %q (want deny|allow)", r.ID, r.Effect)
		}
		r.Effect = eff
		if strings.TrimSpace(r.ID) == "" {
			r.ID = fmt.Sprintf("rule-%d", i)
		}
	}
	return &doc, nil
}

// LoadDocumentFile reads a policy JSON file from disk.
func LoadDocumentFile(path string) (*Document, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &Document{Version: 1, Mode: "enforce"}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file: %w", err)
	}
	return ParseDocument(b)
}

// fileMeta is used for reload detection.
type fileMeta struct {
	path    string
	modTime time.Time
	size    int64
}

func statFile(path string) (fileMeta, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileMeta{path: path}, err
	}
	return fileMeta{path: path, modTime: fi.ModTime(), size: fi.Size()}, nil
}

// ruleMatches reports whether r applies to req.
func ruleMatches(r Rule, req Request) bool {
	// Principal
	isAgent := req.AgentID != ""
	if len(r.Principals) > 0 {
		ok := false
		for _, p := range r.Principals {
			p = strings.ToLower(strings.TrimSpace(p))
			switch p {
			case "any", "*":
				ok = true
			case "agent":
				if isAgent {
					ok = true
				}
			case "human":
				if !isAgent {
					ok = true
				}
			}
		}
		if !ok {
			return false
		}
	}
	// Action
	if len(r.Actions) > 0 && req.Action != "" {
		if !containsFold(r.Actions, req.Action) {
			return false
		}
	}
	// Agent IDs
	if len(r.AgentIDs) > 0 {
		if !isAgent || !contains(r.AgentIDs, req.AgentID) {
			return false
		}
	}
	// Drive IDs
	if len(r.DriveIDs) > 0 {
		if req.DriveID == "" || !contains(r.DriveIDs, req.DriveID) {
			return false
		}
	}
	// Path deny prefixes: match if path hits any
	if len(r.PathDenyPrefixes) > 0 {
		if req.Path == "" || !pathHitsAny(req.Path, r.PathDenyPrefixes) {
			return false
		}
	}
	// Path allow prefixes: match if path under allow list
	if len(r.PathAllowPrefixes) > 0 {
		if req.Path == "" || !prefixOK(req.Path, r.PathAllowPrefixes) {
			return false
		}
	}
	return true
}

func containsFold(ss []string, x string) bool {
	x = strings.ToLower(strings.TrimSpace(x))
	for _, s := range ss {
		if strings.ToLower(strings.TrimSpace(s)) == x {
			return true
		}
	}
	return false
}

// pathHitsAny is true if path is under or equals any deny prefix
// (also matches ".ssh" inside "/workspace/.ssh/id_rsa").
func pathHitsAny(path string, prefixes []string) bool {
	if prefixOK(path, prefixes) {
		return true
	}
	// Extra: segment match for basenames like .ssh, .env
	p := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(p, "/")
	for _, pref := range prefixes {
		pref = strings.Trim(strings.TrimSpace(pref), "/")
		if pref == "" {
			continue
		}
		for _, seg := range parts {
			if seg == pref {
				return true
			}
		}
		if strings.Contains(p, "/"+pref+"/") || strings.HasSuffix(p, "/"+pref) {
			return true
		}
	}
	return false
}

// applyFileRules runs ordered file rules after built-ins.
// First matching deny → deny (enforce). First matching allow → allow short-circuit.
// Observe mode never returns Allow=false; Reason may note would-deny.
func applyFileRules(doc *Document, req Request) Decision {
	if doc == nil || len(doc.Rules) == 0 {
		return Decision{Allow: true, Reason: "ok"}
	}
	observe := doc.Mode == "observe"
	for _, r := range doc.Rules {
		if !ruleMatches(r, req) {
			continue
		}
		// Scope requirements when rule matches
		if len(r.RequireScopes) > 0 {
			for _, need := range r.RequireScopes {
				if !hasScope(req.Scopes, need) {
					reason := r.Reason
					if reason == "" {
						reason = "policy " + r.ID + ": missing scope " + need
					}
					if observe {
						return Decision{Allow: true, Reason: "observe:would-deny:" + reason}
					}
					return Decision{Allow: false, Reason: reason}
				}
			}
		}
		switch r.Effect {
		case "deny":
			reason := r.Reason
			if reason == "" {
				reason = "policy deny: " + r.ID
			}
			if observe {
				return Decision{Allow: true, Reason: "observe:would-deny:" + reason}
			}
			return Decision{Allow: false, Reason: reason}
		case "allow":
			reason := r.Reason
			if reason == "" {
				reason = "policy allow: " + r.ID
			}
			return Decision{Allow: true, Reason: reason}
		}
	}
	return Decision{Allow: true, Reason: "ok"}
}
