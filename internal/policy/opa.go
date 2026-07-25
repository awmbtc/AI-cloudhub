package policy

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/open-policy-agent/opa/rego"
)

// OPAEvaluator evaluates Rego against a policy.Request.
// Package expectation (see protocols/aicloudhub.rego.example):
//
//	package aicloudhub.authz
//	default allow := true
//	# set allow := false to deny
//
// Query: data.aicloudhub.authz.allow
type OPAEvaluator struct {
	mu       sync.RWMutex
	query    rego.PreparedEvalQuery
	path     string
	modTime  time.Time
	size     int64
	mod      string // raw module for reload compare
	strict   bool   // if true, OPA eval error → deny
	observe  bool   // if true, OPA deny becomes allow with would-deny reason
}

// LoadOPAFile compiles a .rego module from path.
// Empty path returns nil evaluator (disabled).
func LoadOPAFile(path string) (*OPAEvaluator, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("opa policy: %w", err)
	}
	ev, err := compileOPA(path, string(b))
	if err != nil {
		return nil, err
	}
	meta, err := os.Stat(path)
	if err == nil {
		ev.modTime = meta.ModTime()
		ev.size = meta.Size()
	}
	ev.strict = envTruthyPolicy("AI_CLOUDHUB_OPA_STRICT")
	ev.observe = envTruthyPolicy("AI_CLOUDHUB_OPA_OBSERVE")
	return ev, nil
}

func compileOPA(path, module string) (*OPAEvaluator, error) {
	ctx := context.Background()
	r := rego.New(
		rego.Query("data.aicloudhub.authz.allow"),
		rego.Module(path, module),
	)
	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("opa compile: %w", err)
	}
	return &OPAEvaluator{query: pq, path: path, mod: module}, nil
}

// Evaluate runs OPA with request as input. Returns (allow, reason, error).
// When OPA is disabled (nil), returns allow=true, reason="".
func (o *OPAEvaluator) Evaluate(req Request) (allow bool, reason string, err error) {
	if o == nil {
		return true, "", nil
	}
	o.maybeReload()
	o.mu.RLock()
	pq := o.query
	strict := o.strict
	observe := o.observe
	o.mu.RUnlock()

	input := map[string]interface{}{
		"agent_id":          req.AgentID,
		"action":            req.Action,
		"drive_id":          req.DriveID,
		"path":              req.Path,
		"scopes":            req.Scopes,
		"allowed_drive_ids": req.AllowedDriveIDs,
		"read_prefixes":     req.ReadPrefixes,
		"write_prefixes":    req.WritePrefixes,
		"principal":         map[bool]string{true: "agent", false: "human"}[req.AgentID != ""],
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rs, err := pq.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		if strict {
			return false, "opa error: " + err.Error(), err
		}
		// Soft: fail open with note
		return true, "opa error (fail-open): " + err.Error(), err
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		// No result — treat as allow (default)
		return true, "opa:no-result", nil
	}
	v := rs[0].Expressions[0].Value
	allowed, ok := v.(bool)
	if !ok {
		if strict {
			return false, "opa: non-boolean allow result", fmt.Errorf("opa result type %T", v)
		}
		return true, "opa: non-boolean (fail-open)", nil
	}
	if allowed {
		return true, "opa:allow", nil
	}
	if observe {
		return true, "opa:observe:would-deny", nil
	}
	return false, "opa:deny", nil
}

func (o *OPAEvaluator) maybeReload() {
	if o == nil || o.path == "" {
		return
	}
	meta, err := os.Stat(o.path)
	if err != nil {
		return
	}
	o.mu.RLock()
	same := meta.ModTime().Equal(o.modTime) && meta.Size() == o.size
	o.mu.RUnlock()
	if same {
		return
	}
	b, err := os.ReadFile(o.path)
	if err != nil {
		return
	}
	ev, err := compileOPA(o.path, string(b))
	if err != nil {
		return
	}
	o.mu.Lock()
	o.query = ev.query
	o.mod = ev.mod
	o.modTime = meta.ModTime()
	o.size = meta.Size()
	o.mu.Unlock()
}

// Status fields for admin diagnostics.
func (o *OPAEvaluator) Enabled() bool { return o != nil }
func (o *OPAEvaluator) Path() string {
	if o == nil {
		return ""
	}
	return o.path
}

func envTruthyPolicy(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes"
}
