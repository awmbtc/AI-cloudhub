// runner — AI-cloudhub cloud runtime (BYOC).
//
// Modes:
//  1) One-shot: set AI_CLOUDHUB_DRIVE_ID (or BINDING_ID) and optional command after --
//  2) Worker:   AI_CLOUDHUB_WORKER=1  → poll claim next job, run, complete (still user compute)
//  3) check:    host preflight (rclone/FUSE); no token — symmetric to hubd check
//  4) dry-run:  list pending jobs + optional session conf write; no claim/run
//
// Never run as a platform multi-tenant mega-pool (docs/DECISIONS.md D-001).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/runtimeenv"
	"github.com/awmbtc/AI-cloudhub/internal/sandbox"
)

const (
	runnerModeRun     = "run"
	runnerModeCheck   = "check"
	runnerModeDryRun  = "dry-run"
	runnerModeHelp    = "help"
	runnerModeWorker  = "worker"
	runnerModeMaterialize = "materialize"
)

func main() {
	mode, restArgs := parseRunnerMode(os.Args[1:])
	if mode == runnerModeHelp {
		printRunnerHelp()
		return
	}
	if mode == runnerModeCheck {
		os.Exit(runRunnerCheck())
	}

	api := env("AI_CLOUDHUB_API", "http://127.0.0.1:8080")
	token := os.Getenv("AI_CLOUDHUB_TOKEN")
	if token == "" {
		log.Fatal("AI_CLOUDHUB_TOKEN required (except: runner check)")
	}
	mountPoint := env("AI_CLOUDHUB_MOUNT", "/workspace")

	if mode == runnerModeDryRun {
		os.Exit(runRunnerDryRun(api, token, mountPoint))
	}

	// Materialize-only: connector fetch + git clone / DB env inject (no rclone/session).
	// Used for BYOC connector 联调 and smoke-byoc-connectors (D-001: still user machine).
	if mode == runnerModeMaterialize || envTruthy(os.Getenv("AI_CLOUDHUB_MATERIALIZE_ONLY")) {
		if err := runMaterializeOnly(api, token, mountPoint); err != nil {
			log.Fatal(err)
		}
		return
	}

	if mode == runnerModeWorker || env("AI_CLOUDHUB_WORKER", "") == "1" || env("AI_CLOUDHUB_WORKER", "") == "true" {
		runWorker(api, token, mountPoint)
		return
	}

	driveID := os.Getenv("AI_CLOUDHUB_DRIVE_ID")
	bindingID := os.Getenv("AI_CLOUDHUB_BINDING_ID")
	if driveID == "" && bindingID == "" {
		log.Fatal("set AI_CLOUDHUB_DRIVE_ID / BINDING_ID, AI_CLOUDHUB_WORKER=1, AI_CLOUDHUB_MATERIALIZE_ONLY=1, or: runner check|dry-run")
	}
	args := restArgs
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if _, err := runOnce(api, token, mountPoint, driveID, bindingID, "", args, 0); err != nil {
		log.Fatal(err)
	}
}

func parseRunnerMode(args []string) (mode string, rest []string) {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_RUNNER_MODE"))); v != "" {
		switch v {
		case "check", "dry-run", "dryrun", "help", "worker", "materialize", "run", "daemon":
			if v == "dryrun" {
				return runnerModeDryRun, args
			}
			if v == "daemon" {
				return runnerModeRun, args
			}
			return v, args
		}
	}
	if len(args) == 0 {
		return runnerModeRun, nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "check", "--check", "-check":
		return runnerModeCheck, args[1:]
	case "dry-run", "--dry-run", "-dry-run", "dryrun", "--dryrun":
		return runnerModeDryRun, args[1:]
	case "help", "--help", "-h":
		return runnerModeHelp, args[1:]
	case "worker", "--worker":
		return runnerModeWorker, args[1:]
	case "materialize", "--materialize":
		return runnerModeMaterialize, args[1:]
	case "run":
		return runnerModeRun, args[1:]
	default:
		// legacy: command args after -- or bare command
		return runnerModeRun, args
	}
}

func printRunnerHelp() {
	fmt.Fprintf(os.Stdout, `runner — AI-cloudhub BYOC runtime (user machine only; D-001)

Usage:
  runner check              # host preflight (rclone/FUSE); no token
  runner dry-run            # list pending jobs + optional session conf; no claim/run
  runner worker             # same as AI_CLOUDHUB_WORKER=1
  runner materialize        # same as AI_CLOUDHUB_MATERIALIZE_ONLY=1
  runner                    # one-shot: need DRIVE_ID/BINDING_ID [ -- cmd… ]

Env:
  AI_CLOUDHUB_API / TOKEN / MOUNT / DRIVE_ID / BINDING_ID / WORKER / MATERIALIZE_ONLY
  AI_CLOUDHUB_RUNNER_MODE   check|dry-run|worker|materialize|run
  AI_CLOUDHUB_RUNNER_ID / REGION  claim attribution
  AI_CLOUDHUB_STATE         dry-run conf dir (default temp)

Docs: docs/RUNNER.md · docs/HUBD.md · docs/DECISIONS.md D-001
`)
}

// runRunnerCheck prints runtimeenv.Check as JSON (+ jail defaults). Exit 0 if rclone OK.
func runRunnerCheck() int {
	rep := runtimeenv.Check()
	out := map[string]interface{}{
		"component":    "runner",
		"os":           rep.OS,
		"arch":         rep.Arch,
		"rclone_ok":    rep.RcloneOK,
		"rclone_path":  rep.RclonePath,
		"rclone_version": rep.RcloneVer,
		"fuse_hint":    rep.FuseHint,
		"winfsp_ok":    rep.WinFspOK,
		"winfsp_hint":  rep.WinFspHint,
		"install_hint": rep.InstallHint,
		"ok":           rep.OK,
		"warnings":     rep.Warnings,
		"errors":       rep.Errors,
		"goos":         runtime.GOOS,
		"jail_default": env("AI_CLOUDHUB_JAIL", "1") != "0",
		"byoc_note":    "jobs run only on this host; no platform pool (D-001)",
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if !rep.RcloneOK {
		return 1
	}
	return 0
}

// runRunnerDryRun lists pending jobs (no claim) and optionally writes session conf for DRIVE_ID.
func runRunnerDryRun(api, token, mountPoint string) int {
	out := map[string]interface{}{
		"mode":        "dry-run",
		"component":   "runner",
		"api":         api,
		"mount":       mountPoint,
		"runner_id":   runnerIdentity(),
		"region":      strings.TrimSpace(os.Getenv("AI_CLOUDHUB_REGION")),
		"byoc_note":   "dry-run does not claim or execute jobs",
	}

	// Pending jobs preview (claimable set; no cursor).
	jobs, err := listPendingJobs(api, token)
	if err != nil {
		out["jobs_error"] = err.Error()
	} else {
		summaries := make([]map[string]interface{}, 0, len(jobs))
		for _, j := range jobs {
			summaries = append(summaries, map[string]interface{}{
				"id":           j.ID,
				"drive_id":     j.DriveID,
				"status":       j.Status,
				"command":      j.Command,
				"mode":         j.Mode,
				"connector_id": j.ConnectorID,
				"agent_id":     j.AgentID,
			})
		}
		out["pending_jobs"] = summaries
		out["pending_count"] = len(summaries)
	}

	// Optional: session conf for drive/binding without running command.
	driveID := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_DRIVE_ID"))
	bindingID := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_BINDING_ID"))
	if driveID != "" || bindingID != "" {
		stateDir := env("AI_CLOUDHUB_STATE", filepath.Join(os.TempDir(), "ai-cloudhub-runner"))
		sessInfo, confPath, serr := dryRunSessionConf(api, token, driveID, bindingID, stateDir)
		if serr != nil {
			out["session_error"] = serr.Error()
		} else {
			out["session"] = sessInfo
			out["conf_path"] = confPath
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if err != nil {
		return 1
	}
	if _, ok := out["session_error"]; ok {
		return 1
	}
	return 0
}

func listPendingJobs(api, token string) ([]jobDTO, error) {
	req, err := http.NewRequest(http.MethodGet, api+"/v1/jobs?status=pending&limit=50", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list jobs HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var wrap struct {
		Items []jobDTO `json:"items"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	return wrap.Items, nil
}

func dryRunSessionConf(api, token, driveID, bindingID, stateDir string) (map[string]interface{}, string, error) {
	// Prefer drive session endpoint when driveID set; binding session when only binding.
	var url string
	if bindingID != "" {
		url = api + "/v1/bindings/" + bindingID + "/session"
	} else {
		url = api + "/v1/drives/" + driveID + "/session"
	}
	payload := map[string]string{
		"device_id": env("AI_CLOUDHUB_DEVICE_ID", "cloud-runner"),
		"mode":      env("AI_CLOUDHUB_MODE", "sync_workspace"),
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("session HTTP %d: %s", resp.StatusCode, truncate(string(body), 240))
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", err
	}
	sess, _ := raw["session"].(map[string]interface{})
	if sess == nil {
		sess = raw
	}
	spec, _ := raw["spec"].(map[string]interface{})
	if spec == nil {
		if s2, ok := sess["spec"].(map[string]interface{}); ok {
			spec = s2
		}
	}
	conf := ""
	if spec != nil {
		if c, ok := spec["rclone_conf"].(string); ok {
			conf = c
		}
	}
	_ = os.MkdirAll(filepath.Join(stateDir, "dry-run"), 0o700)
	confPath := filepath.Join(stateDir, "dry-run", "rclone.conf")
	if conf == "" {
		return map[string]interface{}{"source": sess["source"]}, "", fmt.Errorf("empty rclone_conf in session")
	}
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		return nil, "", err
	}
	info := map[string]interface{}{
		"source":     sess["source"],
		"expires_at": sess["expires_at"],
		"id":         sess["id"],
	}
	if man, ok := raw["manifest"].(map[string]interface{}); ok {
		if envM, ok := man["env"].(map[string]interface{}); ok {
			info["workspace"] = envM["AI_CLOUDHUB_WORKSPACE"]
		}
	} else if man, ok := sess["manifest"].(map[string]interface{}); ok {
		if envM, ok := man["env"].(map[string]interface{}); ok {
			info["workspace"] = envM["AI_CLOUDHUB_WORKSPACE"]
		}
	}
	return info, confPath, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// runOnceResult is workspace prep + agent command outcome for worker complete.
type runOnceResult struct {
	CloneNote       string
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
}

func envTruthy(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "yes"
}

// runMaterializeOnly applies the connector for AI_CLOUDHUB_CONNECTOR_ID without drive mount.
// Writes report JSON to stdout (and optional AI_CLOUDHUB_MATERIALIZE_REPORT path).
func runMaterializeOnly(api, token, mountPoint string) error {
	if strings.TrimSpace(os.Getenv("AI_CLOUDHUB_CONNECTOR_ID")) == "" {
		return fmt.Errorf("AI_CLOUDHUB_CONNECTOR_ID required for MATERIALIZE_ONLY")
	}
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return err
	}
	mat := materializeConnector(api, token, mountPoint)
	report := map[string]interface{}{
		"note":        mat.Note,
		"clone_path":  mat.ClonePath,
		"extra_env":   mat.ExtraEnv,
		"pass_libpq":  mat.PassLibpq,
		"pass_mysql":  mat.PassMysql,
		"strict_hint": mat.StrictHint,
		"ok":          mat.Err == nil,
		"mount":       mountPoint,
	}
	if mat.Err != nil {
		report["error"] = mat.Err.Error()
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	if path := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_MATERIALIZE_REPORT")); path != "" {
		_ = os.WriteFile(path, b, 0o600)
	}
	fmt.Println(string(b))
	if mat.Err != nil {
		// Soft-style: still exit 1 so smoke can assert; CLONE/PG/MYSQL strict not applied here.
		return mat.Err
	}
	return nil
}

// cloneStrict fails the job when git connector materialization fails.
// AI_CLOUDHUB_CLONE_STRICT=1|true|yes (default soft: continue agent, note still recorded).
func cloneStrict() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_CLONE_STRICT")))
	return v == "1" || v == "true" || v == "yes"
}

// joinJobNote joins non-empty segments with " | ", skipping exact duplicates.
func joinJobNote(parts ...string) string {
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, " | ")
}

func runWorker(api, token, mountPoint string) {
	interval := 10 * time.Second
	if v := os.Getenv("AI_CLOUDHUB_POLL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}
	log.Printf("AI-cloudhub runner WORKER mode (BYOC) api=%s poll=%s", api, interval)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			log.Printf("worker stopping")
			return
		case <-ticker.C:
			j, err := claimNext(api, token)
			if err != nil {
				// no jobs is normal
				continue
			}
			log.Printf("claimed job %s drive=%s created_by=%s claimer=%s connector=%s cmd=%v",
				j.ID, j.DriveID, j.AgentID, j.ClaimedByAgentID, j.ConnectorID, j.Command)
			mode := j.Mode
			if mode != "" {
				_ = os.Setenv("AI_CLOUDHUB_MODE", mode)
			}
			// Job-level connector overrides env (still BYOC clone on this host).
			if j.ConnectorID != "" {
				_ = os.Setenv("AI_CLOUDHUB_CONNECTOR_ID", j.ConnectorID)
			}
			stopHB := startJobHeartbeat(api, token, j.ID)
			start := time.Now()
			res, err := runOnce(api, token, mountPoint, j.DriveID, j.BindingID, j.ID, j.Command, j.TimeoutSec)
			durMs := time.Since(start).Milliseconds()
			stopHB()
			ok := err == nil
			note := res.CloneNote
			exitCode := 0
			if err != nil {
				note = joinJobNote(res.CloneNote, err.Error())
				exitCode = exitCodeFromErr(err)
				if errors.Is(err, errJobTimeout) {
					exitCode = 124
				}
				if errors.Is(err, errJobCancelled) {
					exitCode = 130
					note = joinJobNote(res.CloneNote, "cancelled")
				}
				log.Printf("job %s failed: %v", j.ID, err)
			}
			// If remote cancel already set status, complete is no-op; still try to record note/exit.
			_ = completeJob(api, token, j.ID, ok, note, &exitCode, durMs, res.Stdout, res.Stderr, res.StdoutTruncated, res.StderrTruncated)
		}
	}
}

var errJobTimeout = errors.New("job timeout")

// exitCodeFromErr unwraps exec.ExitError; otherwise 1 for non-exit failures.
func exitCodeFromErr(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

type jobDTO struct {
	ID               string   `json:"id"`
	DriveID          string   `json:"drive_id"`
	BindingID        string   `json:"binding_id"`
	Mode             string   `json:"mode"`
	Command          []string `json:"command"`
	Status           string   `json:"status"`
	ConnectorID      string   `json:"connector_id"`
	AgentID          string   `json:"agent_id"`
	ClaimedByAgentID string   `json:"claimed_by_agent_id"`
	TimeoutSec       int      `json:"timeout_sec"`
	Priority         int      `json:"priority"`
}

// runnerIdentity is AI_CLOUDHUB_RUNNER_ID or hostname (for claim attribution).
func runnerIdentity() string {
	if v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_RUNNER_ID")); v != "" {
		if len(v) > 128 {
			return v[:128]
		}
		return v
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	h = strings.TrimSpace(h)
	if len(h) > 128 {
		return h[:128]
	}
	return h
}

func claimNext(api, token string) (*jobDTO, error) {
	req, _ := http.NewRequest(http.MethodPost, api+"/v1/jobs/next/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if rid := runnerIdentity(); rid != "" {
		req.Header.Set("X-AI-Cloudhub-Runner-Id", rid)
	}
	if region := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_REGION")); region != "" {
		req.Header.Set("X-AI-Cloudhub-Region", region)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, body)
	}
	var j jobDTO
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func completeJob(api, token, id string, ok bool, note string, exitCode *int, durationMs int64, stdout, stderr string, stdoutTrunc, stderrTrunc bool) error {
	payload := map[string]interface{}{"ok": ok, "note": note}
	if exitCode != nil {
		payload["exit_code"] = *exitCode
	}
	if durationMs > 0 {
		payload["duration_ms"] = durationMs
	}
	if stdout != "" {
		payload["stdout"] = stdout
	}
	if stderr != "" {
		payload["stderr"] = stderr
	}
	if stdoutTrunc {
		payload["stdout_truncated"] = true
	}
	if stderrTrunc {
		payload["stderr_truncated"] = true
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, api+"/v1/jobs/"+id+"/complete", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return nil
}

// startJobHeartbeat posts POST /v1/jobs/{id}/heartbeat until stop is called.
// Interval from AI_CLOUDHUB_HEARTBEAT (duration, default 30s). No-op if id empty.
func startJobHeartbeat(api, token, id string) (stop func()) {
	if strings.TrimSpace(id) == "" {
		return func() {}
	}
	interval := 30 * time.Second
	if v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_HEARTBEAT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		// Immediate refresh so lease clock is current after claim latency.
		_ = heartbeatJob(api, token, id)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := heartbeatJob(api, token, id); err != nil {
					log.Printf("job %s heartbeat: %v", id, err)
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

func heartbeatJob(api, token, id string) error {
	req, err := http.NewRequest(http.MethodPost, api+"/v1/jobs/"+id+"/heartbeat", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, b)
	}
	return nil
}

// runOnce prepares workspace and runs the agent command.
// CloneNote is "cloned to <path>" or "clone failed: …" when a connector was attempted; empty otherwise.
// With AI_CLOUDHUB_CLONE_STRICT, clone failures return as err (job fails); default soft-continues.
// Stdout/Stderr are capped agent process output (also mirrored to the runner process streams).
func runOnce(api, token, mountPoint, driveID, bindingID, jobID string, args []string, timeoutSec int) (runOnceResult, error) {
	log.Printf("AI-cloudhub runner (BYOC) api=%s mount=%s job=%s", api, mountPoint, jobID)
	var out runOnceResult
	var clonePath string

	var sessURL string
	var body []byte
	var err error
	if bindingID != "" {
		sessURL = api + "/v1/bindings/" + bindingID + "/session"
		body, err = postJSON(sessURL, token, nil)
	} else {
		sessURL = api + "/v1/drives/" + driveID + "/session"
		body, err = postJSON(sessURL, token, map[string]string{
			"mount_point": mountPoint,
			"device_id":   env("AI_CLOUDHUB_DEVICE_ID", "cloud-runner"),
			"mode":        env("AI_CLOUDHUB_MODE", "mount"),
		})
	}
	if err != nil {
		return out, fmt.Errorf("session: %w", err)
	}

	var bundle struct {
		Spec struct {
			RemotePath string `json:"remote_path"`
			MountPoint string `json:"mount_point"`
			RcloneConf string `json:"rclone_conf"`
		} `json:"spec"`
		Session struct {
			Spec struct {
				RemotePath string `json:"remote_path"`
				MountPoint string `json:"mount_point"`
				RcloneConf string `json:"rclone_conf"`
			} `json:"spec"`
			Manifest struct {
				Env map[string]string `json:"env"`
			} `json:"manifest"`
		} `json:"session"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.Unmarshal(body, &bundle); err != nil {
		return out, err
	}
	spec := bundle.Spec
	if spec.RcloneConf == "" {
		spec = bundle.Session.Spec
	}
	if spec.MountPoint != "" {
		mountPoint = spec.MountPoint
	}

	if _, err := exec.LookPath("rclone"); err != nil {
		return out, fmt.Errorf("rclone not found in PATH")
	}

	state := filepath.Join(os.TempDir(), "ai-cloudhub-runner")
	_ = os.MkdirAll(state, 0o700)
	confPath := filepath.Join(state, "rclone.conf")
	if err := os.WriteFile(confPath, []byte(spec.RcloneConf), 0o600); err != nil {
		return out, err
	}
	_ = os.MkdirAll(filepath.Join(mountPoint, ".ai-cloudhub"), 0o755)
	_ = os.MkdirAll(mountPoint, 0o755)
	if len(bundle.Manifest) > 0 {
		_ = os.WriteFile(filepath.Join(mountPoint, ".ai-cloudhub", "manifest.json"), bundle.Manifest, 0o600)
	}

	mode := env("AI_CLOUDHUB_MODE", "")
	if mode == "" && bundle.Session.Manifest.Env != nil {
		mode = bundle.Session.Manifest.Env["AI_CLOUDHUB_MODE"]
	}
	if mode == "" {
		mode = "mount"
	}

	var mountCmd *exec.Cmd
	cleanup := func() {
		if mode == "sync_workspace" {
			log.Printf("sync_workspace push %s -> %s", mountPoint, spec.RemotePath)
			c := exec.Command("rclone", "sync", mountPoint, spec.RemotePath, "--config", confPath, "--create-empty-src-dirs")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			_ = c.Run()
			return
		}
		if mountCmd != nil && mountCmd.Process != nil {
			_ = mountCmd.Process.Signal(syscall.SIGTERM)
			_, _ = mountCmd.Process.Wait()
		}
		_ = exec.Command("fusermount", "-u", mountPoint).Run()
		_ = exec.Command("umount", mountPoint).Run()
	}
	defer cleanup()

	if mode == "sync_workspace" {
		log.Printf("sync_workspace pull %s -> %s", spec.RemotePath, mountPoint)
		pull := exec.Command("rclone", "sync", spec.RemotePath, mountPoint, "--config", confPath, "--create-empty-src-dirs")
		pull.Stdout = os.Stdout
		pull.Stderr = os.Stderr
		if err := pull.Run(); err != nil {
			return out, fmt.Errorf("sync pull: %w", err)
		}
	} else {
		mountCmd = exec.Command("rclone", "mount", spec.RemotePath, mountPoint,
			"--config", confPath,
			"--vfs-cache-mode", "full",
			"--dir-cache-time", "10s",
		)
		mountCmd.Stdout = os.Stdout
		mountCmd.Stderr = os.Stderr
		if err := mountCmd.Start(); err != nil {
			return out, fmt.Errorf("mount: %w", err)
		}
		for i := 0; i < 40; i++ {
			if st, err := os.Stat(mountPoint); err == nil && st.IsDir() {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	// BYOC connector materialization: git / postgres / mysql env inject.
	// Control plane stores non-secret config only; host holds secrets (GIT_ASKPASS / PGPASSWORD / MYSQL_PWD).
	// Soft default: continue agent on fail but surface note; *_STRICT fail job.
	mat := materializeConnector(api, token, mountPoint)
	if mat.Note != "" {
		out.CloneNote = mat.Note
	}
	if mat.Err != nil {
		strict := false
		switch mat.StrictHint {
		case "clone":
			strict = cloneStrict()
		case "pg":
			strict = pgStrict()
		case "mysql":
			strict = mysqlStrict()
		default:
			strict = cloneStrict() || pgStrict() || mysqlStrict()
		}
		log.Printf("connector: %v (strict=%v)", mat.Err, strict)
		if strict {
			return out, fmt.Errorf("%s", out.CloneNote)
		}
	} else if mat.ClonePath != "" {
		clonePath = mat.ClonePath
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup()
		os.Exit(1)
	}()

	// Sandbox v1: filter parent env; inject only AI_CLOUDHUB_* + safe keys.
	// Opt-out: AI_CLOUDHUB_JAIL=0. Pass parent API token only if AI_CLOUDHUB_PASS_TOKEN=1.
	// PassLibpq / PassMysql when DB connectors materialize (host PGPASSWORD / MYSQL_PWD).
	extra := map[string]string{}
	for k, v := range bundle.Session.Manifest.Env {
		extra[k] = v
	}
	extra["AI_CLOUDHUB_WORKSPACE"] = mountPoint
	extra["AI_CLOUDHUB_MODE"] = mode
	if jobID != "" {
		extra["AI_CLOUDHUB_JOB_ID"] = jobID
	}
	if clonePath != "" {
		extra["AI_CLOUDHUB_CLONE_PATH"] = clonePath
	}
	for k, v := range mat.ExtraEnv {
		extra[k] = v
	}
	passLibpq := mat.PassLibpq
	if env("AI_CLOUDHUB_PASS_PG", "") == "0" || strings.EqualFold(env("AI_CLOUDHUB_PASS_PG", ""), "false") {
		passLibpq = false
	}
	passMysql := mat.PassMysql
	if env("AI_CLOUDHUB_PASS_MYSQL", "") == "0" || strings.EqualFold(env("AI_CLOUDHUB_PASS_MYSQL", ""), "false") {
		passMysql = false
	}
	jailOn := env("AI_CLOUDHUB_JAIL", "1") != "0" && env("AI_CLOUDHUB_JAIL", "1") != "false"
	var childEnv []string
	if jailOn {
		passTok := env("AI_CLOUDHUB_PASS_TOKEN", "") == "1" || env("AI_CLOUDHUB_PASS_TOKEN", "") == "true"
		// Soft network policy: AI_CLOUDHUB_NETWORK=deny strips proxy env (not a kernel netns).
		netDeny := strings.EqualFold(env("AI_CLOUDHUB_NETWORK", ""), "deny") ||
			env("AI_CLOUDHUB_NETWORK", "") == "0" || env("AI_CLOUDHUB_NETWORK", "") == "off"
		childEnv = sandbox.FilterOSEnviron(extra, sandbox.EnvFilter{
			PassToken: passTok, PassLibpq: passLibpq, PassMysql: passMysql, DenyNetwork: netDeny,
		})
		log.Printf("sandbox v1 env filter on (keys=%d pass_token=%v pass_libpq=%v pass_mysql=%v network_deny=%v)",
			len(childEnv), passTok, passLibpq, passMysql, netDeny)
	} else {
		childEnv = os.Environ()
		for k, v := range extra {
			childEnv = append(childEnv, k+"="+v)
		}
	}

	if len(args) == 0 {
		log.Printf("ready mode=%s path=%s clone_note=%s (no command); waiting signal", mode, mountPoint, out.CloneNote)
		<-sig
		return out, nil
	}

	// Path jail: reject command args that resolve outside workspace.
	if jailOn {
		jail := sandbox.NewPathJail(mountPoint)
		if err := jail.Allow(mountPoint); err != nil {
			return out, fmt.Errorf("jail mount: %w", err)
		}
		for _, a := range args[1:] {
			// only check path-looking args (absolute or containing / or ..)
			if a == "" || (!strings.Contains(a, "/") && !strings.Contains(a, `\`) && !strings.Contains(a, "..")) {
				continue
			}
			if err := jail.Allow(a); err != nil {
				return out, fmt.Errorf("path jail: arg %q: %w", a, err)
			}
		}
	}

	// Optional in-process seccomp (Linux pure-Go BPF; no-op elsewhere).
	// Apply after env/path jail and mount setup, immediately before agent.
	// AI_CLOUDHUB_SECCOMP=1|true|yes; on error continue unless AI_CLOUDHUB_SECCOMP_STRICT=1.
	if sandbox.Enabled() {
		if err := sandbox.ApplyRunnerDefault(); err != nil {
			if sandbox.Strict() {
				return out, fmt.Errorf("seccomp: %w", err)
			}
			log.Printf("seccomp: apply failed (continuing): %v", err)
		} else {
			log.Printf("seccomp: filter applied profile=%s (no_new_privs+tsync)", sandbox.EffectiveProfile())
		}
	}

	// Hard timeout: job.TimeoutSec, else AI_CLOUDHUB_JOB_TIMEOUT_SEC (0 = none).
	// Cancel poll: when jobID set, poll GET job; if cancelled, cancel context and kill agent.
	to := timeoutSec
	if to <= 0 {
		if v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_TIMEOUT_SEC")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				to = n
			}
		}
	}
	var (
		agent  *exec.Cmd
		ctx    context.Context
		cancel context.CancelFunc
	)
	base := context.Background()
	if to > 0 {
		ctx, cancel = context.WithTimeout(base, time.Duration(to)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(base)
	}
	defer cancel()
	if jobID != "" {
		go watchJobCancel(ctx, cancel, api, token, jobID)
	}
	agent = exec.CommandContext(ctx, args[0], args[1:]...)
	agent.Dir = mountPoint
	agent.Env = childEnv
	var stdoutBuf, stderrBuf bytes.Buffer
	capN := runnerOutputCap()
	outLim := &limitedBuffer{buf: &stdoutBuf, max: capN}
	errLim := &limitedBuffer{buf: &stderrBuf, max: capN}
	agent.Stdout = io.MultiWriter(os.Stdout, outLim)
	agent.Stderr = io.MultiWriter(os.Stderr, errLim)
	agent.Stdin = os.Stdin
	runErr := agent.Run()
	out.Stdout = stdoutBuf.String()
	out.Stderr = stderrBuf.String()
	out.StdoutTruncated = outLim.truncated
	out.StderrTruncated = errLim.truncated
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("%w after %ds: %v", errJobTimeout, to, runErr)
		}
		if ctx.Err() == context.Canceled {
			return out, fmt.Errorf("%w: %v", errJobCancelled, runErr)
		}
		return out, fmt.Errorf("agent: %w", runErr)
	}
	return out, nil
}

var errJobCancelled = errors.New("job cancelled")

// watchJobCancel polls GET /v1/jobs/{id}; if status=cancelled, cancels agent context.
func watchJobCancel(ctx context.Context, cancel context.CancelFunc, api, token, jobID string) {
	interval := 5 * time.Second
	if v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_CANCEL_POLL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			st, err := getJobStatus(api, token, jobID)
			if err != nil {
				continue
			}
			if st == "cancelled" {
				log.Printf("job %s cancelled remotely; stopping agent", jobID)
				cancel()
				return
			}
		}
	}
}

func getJobStatus(api, token, id string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, api+"/v1/jobs/"+id, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var j struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return "", err
	}
	return j.Status, nil
}

// runnerOutputCap is how many bytes of agent stdout/stderr to keep for complete.
// AI_CLOUDHUB_JOB_OUTPUT_MAX (default 8192); keeps the tail.
func runnerOutputCap() int {
	const def = 8192
	v := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_JOB_OUTPUT_MAX"))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if n > 256*1024 {
		return 256 * 1024
	}
	return n
}

// limitedBuffer keeps at most max bytes (tail) of written data.
type limitedBuffer struct {
	buf       *bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l == nil || l.buf == nil {
		return len(p), nil
	}
	if l.max <= 0 {
		return l.buf.Write(p)
	}
	// Always accept full write; trim front if over max.
	n, err := l.buf.Write(p)
	if l.buf.Len() > l.max {
		l.truncated = true
		b := l.buf.Bytes()
		trim := len(b) - l.max
		l.buf.Reset()
		_, _ = l.buf.Write(b[trim:])
	}
	return n, err
}

func postJSON(url, token string, payload interface{}) ([]byte, error) {
	var rdr io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPost, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, body)
	}
	return body, nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
