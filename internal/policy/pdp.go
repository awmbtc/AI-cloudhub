package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// RemotePDP is an optional HTTP Policy Decision Point (Stage C).
//
// Protocol (minimal OpenAPI-ish):
//
//	POST {PDPURL}
//	Content-Type: application/json
//	Body: {
//	  "input": { agent_id, action, drive_id, path, scopes, … same as OPA input },
//	  "principal": "agent"|"human"
//	}
//	Response 200: { "allow": true|false, "reason": "optional" }
//
// Fail-open by default (like OPA). Set AI_CLOUDHUB_PDP_STRICT=1 to deny on errors.
// Set AI_CLOUDHUB_PDP_OBSERVE=1 to log would-deny as allow.
type RemotePDP struct {
	URL     string
	Timeout time.Duration
	strict  bool
	observe bool
	client  *http.Client
}

// LoadRemotePDPFromEnv builds a client when AI_CLOUDHUB_PDP_URL is set.
// Empty URL → nil (disabled).
func LoadRemotePDPFromEnv() *RemotePDP {
	url := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_PDP_URL"))
	if url == "" {
		return nil
	}
	ms := 500
	if v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_PDP_TIMEOUT_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ms = n
		}
	}
	if ms > 10000 {
		ms = 10000
	}
	return &RemotePDP{
		URL:     url,
		Timeout: time.Duration(ms) * time.Millisecond,
		strict:  envTruthyPolicy("AI_CLOUDHUB_PDP_STRICT"),
		observe: envTruthyPolicy("AI_CLOUDHUB_PDP_OBSERVE"),
		client:  &http.Client{Timeout: time.Duration(ms) * time.Millisecond},
	}
}

// NewRemotePDP constructs a PDP for tests.
func NewRemotePDP(url string, timeout time.Duration, strict, observe bool) *RemotePDP {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	return &RemotePDP{
		URL:     strings.TrimSpace(url),
		Timeout: timeout,
		strict:  strict,
		observe: observe,
		client:  &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a remote PDP is configured.
func (p *RemotePDP) Enabled() bool {
	return p != nil && strings.TrimSpace(p.URL) != ""
}

// Evaluate calls the remote PDP. Nil PDP → allow.
func (p *RemotePDP) Evaluate(req Request) (allow bool, reason string, err error) {
	if p == nil || !p.Enabled() {
		return true, "", nil
	}
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
	body, _ := json.Marshal(map[string]interface{}{
		"input":     input,
		"principal": input["principal"],
	})
	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return p.fail(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_PDP_TOKEN")); tok != "" {
		httpReq.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := p.client.Do(httpReq)
	if err != nil {
		return p.fail(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return p.fail(fmt.Errorf("pdp HTTP %d: %s", res.StatusCode, truncatePDP(string(raw), 160)))
	}
	var out struct {
		Allow  *bool  `json:"allow"`
		Reason string `json:"reason"`
		// Also accept top-level decision from some PDPs
		Result *bool `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return p.fail(fmt.Errorf("pdp json: %w", err))
	}
	allowed := true
	if out.Allow != nil {
		allowed = *out.Allow
	} else if out.Result != nil {
		allowed = *out.Result
	} else {
		// No boolean — treat as allow with note
		return true, "pdp:no-allow-field", nil
	}
	reason = strings.TrimSpace(out.Reason)
	if allowed {
		if reason == "" {
			reason = "pdp:allow"
		}
		return true, reason, nil
	}
	if p.observe {
		if reason == "" {
			reason = "pdp:observe:would-deny"
		} else {
			reason = "pdp:observe:" + reason
		}
		return true, reason, nil
	}
	if reason == "" {
		reason = "pdp:deny"
	} else if !strings.HasPrefix(reason, "pdp:") {
		reason = "pdp:deny:" + reason
	}
	return false, reason, nil
}

func (p *RemotePDP) fail(err error) (bool, string, error) {
	if p.strict {
		return false, "pdp error: " + err.Error(), err
	}
	return true, "pdp error (fail-open): " + err.Error(), err
}

func truncatePDP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
