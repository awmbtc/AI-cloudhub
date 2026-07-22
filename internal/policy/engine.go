// Package policy — Rate limits, quotas, and Policy Engine v0/v1 (ROADMAP stage B).
package policy

import (
	"fmt"
	"strings"
	"sync"
	"time"
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

// Engine is Policy v0 built-ins + optional external JSON file (v1).
type Engine struct {
	mu         sync.RWMutex
	doc        *Document
	filePath   string
	fileMeta   fileMeta
	reloadEvery time.Duration
	lastCheck  time.Time
}

// EngineOptions configures optional file-backed rules.
type EngineOptions struct {
	// FilePath is AI_CLOUDHUB_POLICY_FILE (empty = built-ins only).
	FilePath string
	// ReloadEvery re-stats the file on Evaluate when elapsed (0 = load once).
	ReloadEvery time.Duration
}

// NewEngine returns Policy Engine with built-ins only.
func NewEngine() *Engine { return &Engine{doc: &Document{Version: 1, Mode: "enforce"}} }

// NewEngineWithOptions loads optional external policy file.
func NewEngineWithOptions(opts EngineOptions) (*Engine, error) {
	e := NewEngine()
	e.filePath = strings.TrimSpace(opts.FilePath)
	e.reloadEvery = opts.ReloadEvery
	if e.filePath == "" {
		return e, nil
	}
	if err := e.loadFile(); err != nil {
		return nil, err
	}
	return e, nil
}

// LoadFile replaces the document from path (also used by Reload).
func (e *Engine) LoadFile(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.filePath = strings.TrimSpace(path)
	if e.filePath == "" {
		e.doc = &Document{Version: 1, Mode: "enforce"}
		e.fileMeta = fileMeta{}
		return nil
	}
	return e.loadFileLocked()
}

func (e *Engine) loadFile() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadFileLocked()
}

func (e *Engine) loadFileLocked() error {
	doc, err := LoadDocumentFile(e.filePath)
	if err != nil {
		return err
	}
	meta, err := statFile(e.filePath)
	if err != nil {
		return err
	}
	e.doc = doc
	e.fileMeta = meta
	e.lastCheck = time.Now()
	return nil
}

// maybeReload reloads when reloadEvery > 0 and mtime/size changed.
func (e *Engine) maybeReload() {
	e.mu.RLock()
	path := e.filePath
	every := e.reloadEvery
	last := e.lastCheck
	prev := e.fileMeta
	e.mu.RUnlock()
	if path == "" || every <= 0 {
		return
	}
	if time.Since(last) < every {
		return
	}
	meta, err := statFile(path)
	e.mu.Lock()
	e.lastCheck = time.Now()
	e.mu.Unlock()
	if err != nil {
		return
	}
	if meta.modTime.Equal(prev.modTime) && meta.size == prev.size {
		return
	}
	_ = e.loadFile()
}

// Status is a snapshot for admin / diagnostics.
type Status struct {
	FilePath    string `json:"file_path,omitempty"`
	Loaded      bool   `json:"loaded"`
	Version     int    `json:"version"`
	Mode        string `json:"mode"`
	RuleCount   int    `json:"rule_count"`
	ReloadEvery string `json:"reload_every,omitempty"`
	ModTime     string `json:"mod_time,omitempty"`
}

// Status returns current file policy diagnostics.
func (e *Engine) Status() Status {
	if e == nil {
		return Status{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := Status{
		FilePath: e.filePath,
		Loaded:   e.doc != nil,
	}
	if e.reloadEvery > 0 {
		st.ReloadEvery = e.reloadEvery.String()
	}
	if e.doc != nil {
		st.Version = e.doc.Version
		st.Mode = e.doc.Mode
		st.RuleCount = len(e.doc.Rules)
	}
	if !e.fileMeta.modTime.IsZero() {
		st.ModTime = e.fileMeta.modTime.UTC().Format(time.RFC3339)
	}
	return st
}

// Document returns a copy of the loaded document (rules only; for tests).
func (e *Engine) Document() *Document {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.doc == nil {
		return nil
	}
	cp := *e.doc
	cp.Rules = append([]Rule(nil), e.doc.Rules...)
	return &cp
}

// Evaluate applies capability rules for agent principals, then external JSON rules.
// Humans (AgentID empty) skip built-in agent checks but still match file rules with principal=human|any.
func (e *Engine) Evaluate(req Request) Decision {
	if e != nil {
		e.maybeReload()
	}
	// Built-in agent capability checks
	if req.AgentID != "" {
		needScope := scopeForAction(req.Action)
		if needScope != "" && !hasScope(req.Scopes, needScope) {
			return Decision{Allow: false, Reason: "missing scope: " + needScope}
		}
		if req.DriveID != "" && len(req.AllowedDriveIDs) > 0 {
			if !contains(req.AllowedDriveIDs, req.DriveID) {
				return Decision{Allow: false, Reason: "drive not allowed for agent"}
			}
		}
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
	}

	var doc *Document
	if e != nil {
		e.mu.RLock()
		doc = e.doc
		e.mu.RUnlock()
	}
	d := applyFileRules(doc, req)
	if d.Reason == "ok" && req.AgentID == "" {
		return Decision{Allow: true, Reason: "human"}
	}
	if d.Reason == "ok" {
		return Decision{Allow: true, Reason: "ok"}
	}
	return d
}

// CanAccessDrive is a convenience for B1.
func CanAccessDrive(agentID string, allowed []string, driveID string) error {
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
	return nil
}

// CanAccessDriveWithEngine uses full Evaluate (built-in + file).
func CanAccessDriveWithEngine(e *Engine, req Request) error {
	if e == nil {
		e = NewEngine()
	}
	if req.Action == "" {
		req.Action = ActionDriveRead
	}
	d := e.Evaluate(req)
	if !d.Allow {
		return fmt.Errorf("%s", d.Reason)
	}
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
	// drive.write implies drive.read for session
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
