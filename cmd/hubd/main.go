// hubd — AI-cloudhub desktop runtime (P0).
// Polls control plane for bindings with desired=mounted, issues STS sessions,
// mounts via rclone, writes Workspace Manifest, reports actual state.
//
// Usage:
//
//	export AI_CLOUDHUB_API=http://127.0.0.1:8080
//	export AI_CLOUDHUB_TOKEN=<bearer>
//	export AI_CLOUDHUB_DEVICE_ID=laptop-1
//	hubd
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/runtimeenv"
)

func main() {
	mode := parseHubdMode(os.Args[1:])
	if mode == hubdModeHelp {
		printHubdHelp()
		return
	}
	if mode == hubdModeCheck {
		os.Exit(runHubdCheck())
	}

	api := env("AI_CLOUDHUB_API", "http://127.0.0.1:8080")
	token := os.Getenv("AI_CLOUDHUB_TOKEN")
	if token == "" {
		log.Fatal("AI_CLOUDHUB_TOKEN required (except: hubd check)")
	}
	device := env("AI_CLOUDHUB_DEVICE_ID", "default")
	interval := 15 * time.Second
	if v := os.Getenv("AI_CLOUDHUB_POLL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}

	stateDir := env("AI_CLOUDHUB_STATE", filepath.Join(os.TempDir(), "ai-cloudhub-hubd"))
	_ = os.MkdirAll(stateDir, 0o700)

	rep := runtimeenv.Check()
	log.Printf("runtimeenv ok=%v rclone=%v os=%s", rep.OK, rep.RcloneOK, rep.OS)
	for _, w := range rep.Warnings {
		log.Printf("warning: %s", w)
	}
	if rep.InstallHint != "" {
		log.Printf("install: %s", rep.InstallHint)
	}

	// dry-run only needs API sessions + conf write — rclone optional.
	if mode == hubdModeDryRun {
		if !rep.RcloneOK {
			log.Printf("warning: rclone missing — dry-run still issues session conf (mount would fail)")
		}
		os.Exit(runHubdDryRun(api, token, device, stateDir))
	}

	// Hard fail only when rclone is missing (daemon / once actually mount).
	// On Windows, missing WinFsp is a warning: mount mode may fail, but mode=sync_workspace can still work.
	if !rep.RcloneOK {
		for _, e := range rep.Errors {
			log.Printf("error: %s", e)
		}
		if rep.InstallHint != "" {
			log.Fatalf("runtimeenv: rclone required — %s", rep.InstallHint)
		}
		log.Fatalf("runtimeenv: %v", rep.Errors)
	}
	if !rep.OK {
		// Other hard errors (if any beyond rclone) still fail.
		log.Fatalf("runtimeenv: %v", rep.Errors)
	}
	if runtime.GOOS == "windows" && !rep.WinFspOK {
		log.Printf("warning: WinFsp not detected — mount mode will be refused until WinFsp is installed")
		log.Printf("install WinFsp+rclone: powershell -ExecutionPolicy Bypass -File scripts\\windows\\install-deps.ps1")
		log.Printf("docs: docs/WINDOWS.md — or use mode=sync_workspace without WinFsp")
	}
	rcloneBin := "rclone"
	if rep.RclonePath != "" {
		rcloneBin = rep.RclonePath
	}
	winFspOK := rep.WinFspOK

	log.Printf("AI-cloudhub hubd starting api=%s device=%s rclone=%s mode=%s", api, device, rcloneBin, mode)

	// active: bindingID -> mount process + session expiry
	active := map[string]*mountProc{}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	remount := func(id string, b bindingDTO) {
		if old, ok := active[id]; ok {
			old.stop()
			delete(active, id)
		}
		log.Printf("mount binding %s -> %s mode=%s", id, b.MountPoint, strings.TrimSpace(b.Mode))
		sess, err := issueSession(api, token, id)
		if err != nil {
			log.Printf("session %s: %v", id, err)
			_ = reportActual(api, token, id, "error", err.Error())
			return
		}
		mode := resolveMode(b.Mode, sess)
		mp, err := startMount(stateDir, id, sess, mode, rcloneBin, winFspOK)
		if err != nil {
			log.Printf("mount %s: %v", id, err)
			_ = reportActual(api, token, id, "error", err.Error())
			return
		}
		mp.expiresAt = sess.Session.ExpiresAt
		mp.sessionToken = sess.Session.Token
		mp.driveID = b.DriveID
		mp.bindingMode = strings.TrimSpace(b.Mode)
		active[id] = mp
		_ = reportActual(api, token, id, "mounted", "")
		log.Printf("mounted %s drive=%s mode=%s workspace=%s expires=%s",
			id, b.DriveID, mp.mode, sess.workspace(), sess.Session.ExpiresAt.Format(time.RFC3339))
	}

	reconcile := func() {
		bindings, err := listBindings(api, token, device)
		if err != nil {
			log.Printf("list bindings: %v", err)
			return
		}
		want := map[string]bindingDTO{}
		for _, b := range bindings {
			if b.Desired == "mounted" {
				want[b.ID] = b
			}
		}
		// unmount removed/unmounted
		for id, mp := range active {
			if _, ok := want[id]; !ok {
				log.Printf("unmount binding %s", id)
				mp.stop()
				delete(active, id)
				_ = postBarrier(api, token, mp.driveID, device)
				_ = reportActual(api, token, id, "unmounted", "")
			}
		}
		// mount new, detect dead rclone, mode change, or soft-refresh expiring sessions
		for id, b := range want {
			mp, ok := active[id]
			if !ok {
				remount(id, b)
				continue
			}
			// binding.mode changed while active → full remount
			if strings.TrimSpace(b.Mode) != "" && strings.TrimSpace(b.Mode) != mp.bindingMode &&
				strings.TrimSpace(b.Mode) != mp.mode {
				log.Printf("mode change binding %s %s -> %s; remounting", id, mp.mode, b.Mode)
				remount(id, b)
				continue
			}
			// rclone mount process died after we reported mounted
			if mountDead(mp) {
				log.Printf("mount process dead for binding %s; reporting error and remounting", id)
				_ = reportActual(api, token, id, "error", "rclone mount process exited")
				mp.stop()
				delete(active, id)
				remount(id, b)
				continue
			}
			// Process alive but FUSE path unreadable (Windows WinFsp hang / Linux dead mount).
			if mp.mode == "mount" && mountPointUnreachable(mp.mountPoint, 3*time.Second) {
				log.Printf("mount path unreachable for binding %s (%s); reporting error and remounting", id, mp.mountPoint)
				_ = reportActual(api, token, id, "error", "mount path unreachable")
				mp.stop()
				delete(active, id)
				remount(id, b)
				continue
			}
			if !mp.expiresAt.IsZero() && time.Until(mp.expiresAt) < 5*time.Minute {
				log.Printf("refresh session for binding %s (expires soon)", id)
				// AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH=1 skips soft conf rewrite (honest for open FUSE handles).
				if forceRemountOnRefresh() {
					log.Printf("force remount on refresh for binding %s", id)
					remount(id, b)
					continue
				}
				if mp.sessionToken != "" && mp.driveID != "" {
					if nb, err := refreshSession(api, token, mp.sessionToken, mp.driveID); err == nil {
						spec := nb.mountSpec()
						if mp.confPath != "" && spec.RcloneConf != "" {
							if err := os.WriteFile(mp.confPath, []byte(spec.RcloneConf), 0o600); err == nil {
								mp.expiresAt = nb.Session.ExpiresAt
								mp.sessionToken = nb.Session.Token
								// soft-refresh may leave open FUSE handles on old creds (documented)
								if mountDead(mp) {
									log.Printf("mount dead after soft-refresh %s; remounting", id)
									remount(id, b)
									continue
								}
								if mountPointUnreachable(mp.mountPoint, 3*time.Second) {
									log.Printf("mount unreachable after soft-refresh %s; remounting", id)
									remount(id, b)
									continue
								}
								log.Printf("soft-refreshed binding %s expires=%s", id, mp.expiresAt.Format(time.RFC3339))
								continue
							}
						}
					} else {
						log.Printf("soft refresh failed: %v; remounting", err)
					}
				}
				remount(id, b)
			}
		}
	}

	reconcile()
	if mode == hubdModeOnce {
		log.Printf("once: single reconcile done; exiting (no long-run mounts kept by once mode)")
		// once still leaves mounts up if remount started them — stop cleanly so CI doesn't leak.
		for id, mp := range active {
			mp.stop()
			_ = postBarrier(api, token, mp.driveID, device)
			_ = reportActual(api, token, id, "unmounted", "hubd once exit")
		}
		return
	}
	for {
		select {
		case <-stop:
			log.Printf("shutting down")
			for id, mp := range active {
				mp.stop()
				_ = postBarrier(api, token, mp.driveID, device)
				_ = reportActual(api, token, id, "unmounted", "hubd shutdown")
			}
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

// hubd subcommands: check | dry-run | once | (default daemon)
const (
	hubdModeDaemon = "daemon"
	hubdModeCheck  = "check"
	hubdModeDryRun = "dry-run"
	hubdModeOnce   = "once"
	hubdModeHelp   = "help"
)

func parseHubdMode(args []string) string {
	// Env override (CI-friendly)
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_HUBD_MODE"))); v != "" {
		switch v {
		case "check", "dry-run", "dryrun", "once", "daemon", "help":
			if v == "dryrun" {
				return hubdModeDryRun
			}
			return v
		}
	}
	for _, a := range args {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "check", "--check", "-check":
			return hubdModeCheck
		case "dry-run", "--dry-run", "-dry-run", "dryrun", "--dryrun":
			return hubdModeDryRun
		case "once", "--once", "-once":
			return hubdModeOnce
		case "help", "--help", "-h":
			return hubdModeHelp
		case "daemon", "run":
			return hubdModeDaemon
		}
	}
	return hubdModeDaemon
}

func printHubdHelp() {
	fmt.Fprintf(os.Stdout, `hubd — AI-cloudhub local runtime (BYOC / user machine only)

Usage:
  hubd                 # daemon: poll bindings, mount via rclone
  hubd check           # host preflight only (rclone/FUSE); no token
  hubd dry-run         # list desired bindings, issue session, write conf; NO mount
  hubd once            # one reconcile cycle (may mount), then clean exit

Env:
  AI_CLOUDHUB_API          control plane URL (default http://127.0.0.1:8080)
  AI_CLOUDHUB_TOKEN        bearer (required except check)
  AI_CLOUDHUB_DEVICE_ID    device id (default default)
  AI_CLOUDHUB_STATE        state dir for rclone.conf / manifest
  AI_CLOUDHUB_POLL         poll interval (default 15s)
  AI_CLOUDHUB_HUBD_MODE    check|dry-run|once|daemon (override argv)
  AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH=1  full remount instead of soft conf rewrite

Docs: docs/HUBD.md · docs/GOLDEN-PATH.md · docs/WINDOWS.md
`)
}

// runHubdCheck prints runtimeenv.Check as JSON. Exit 0 if rclone OK.
func runHubdCheck() int {
	rep := runtimeenv.Check()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
	if !rep.RcloneOK {
		return 1
	}
	return 0
}

// runHubdDryRun issues sessions for desired=mounted bindings and writes conf/manifest
// under stateDir without starting rclone mount/sync. Does not report actual=mounted.
func runHubdDryRun(api, token, device, stateDir string) int {
	bindings, err := listBindings(api, token, device)
	if err != nil {
		log.Printf("list bindings: %v", err)
		return 1
	}
	type item struct {
		BindingID  string `json:"binding_id"`
		DriveID    string `json:"drive_id"`
		MountPoint string `json:"mount_point"`
		Mode       string `json:"mode"`
		Workspace  string `json:"workspace,omitempty"`
		Source     string `json:"session_source,omitempty"`
		ConfPath   string `json:"conf_path,omitempty"`
		ExpiresAt  string `json:"expires_at,omitempty"`
		Error      string `json:"error,omitempty"`
	}
	var items []item
	nOK := 0
	for _, b := range bindings {
		if b.Desired != "mounted" {
			continue
		}
		it := item{
			BindingID:  b.ID,
			DriveID:    b.DriveID,
			MountPoint: b.MountPoint,
			Mode:       strings.TrimSpace(b.Mode),
		}
		sess, err := issueSession(api, token, b.ID)
		if err != nil {
			it.Error = err.Error()
			items = append(items, it)
			continue
		}
		it.Mode = resolveMode(b.Mode, sess)
		it.Workspace = sess.workspace()
		it.Source = strings.TrimSpace(sess.Session.Source)
		spec := sess.mountSpec()
		dir := filepath.Join(stateDir, "dry-run", b.ID)
		_ = os.MkdirAll(dir, 0o700)
		confPath := filepath.Join(dir, "rclone.conf")
		if spec.RcloneConf == "" {
			it.Error = "empty rclone_conf"
			items = append(items, it)
			continue
		}
		if err := os.WriteFile(confPath, []byte(spec.RcloneConf), 0o600); err != nil {
			it.Error = err.Error()
			items = append(items, it)
			continue
		}
		if len(sess.Manifest) > 0 {
			_ = os.WriteFile(filepath.Join(dir, "manifest.json"), sess.Manifest, 0o600)
		}
		it.ConfPath = confPath
		if !sess.Session.ExpiresAt.IsZero() {
			it.ExpiresAt = sess.Session.ExpiresAt.Format(time.RFC3339)
		}
		items = append(items, it)
		nOK++
		log.Printf("dry-run ok binding=%s drive=%s mode=%s source=%s conf=%s", b.ID, b.DriveID, it.Mode, it.Source, confPath)
	}
	out := map[string]interface{}{
		"mode":     "dry-run",
		"device":   device,
		"api":      api,
		"bindings": items,
		"ok_count": nOK,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if nOK == 0 && len(items) == 0 {
		log.Printf("dry-run: no desired=mounted bindings for device=%s", device)
		return 0 // empty is success (nothing to do)
	}
	for _, it := range items {
		if it.Error != "" {
			return 1
		}
	}
	return 0
}

type bindingDTO struct {
	ID         string `json:"id"`
	DriveID    string `json:"drive_id"`
	DeviceID   string `json:"device_id"`
	MountPoint string `json:"mount_point"`
	Desired    string `json:"desired"`
	Actual     string `json:"actual"`
	Mode       string `json:"mode"`
}

type sessionBundle struct {
	Session struct {
		ID        string    `json:"id"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
		Mode      string    `json:"mode"`
		Source    string    `json:"source"`
		Note      string    `json:"note"`
		Spec      mountSpec `json:"spec"`
		Manifest  struct {
			Env map[string]string `json:"env"`
		} `json:"manifest"`
	} `json:"session"`
	Manifest json.RawMessage `json:"manifest"`
	Spec     mountSpec       `json:"spec"`
	Note     string          `json:"note"`
}

// resolveMode picks mount vs sync_workspace.
// Precedence: binding.mode → session.mode → session.manifest.env AI_CLOUDHUB_MODE → "mount".
func resolveMode(bindingMode string, sess *sessionBundle) string {
	if m := strings.TrimSpace(bindingMode); m != "" {
		return m
	}
	if sess != nil {
		if m := strings.TrimSpace(sess.Session.Mode); m != "" {
			return m
		}
		if sess.Session.Manifest.Env != nil {
			if m := strings.TrimSpace(sess.Session.Manifest.Env["AI_CLOUDHUB_MODE"]); m != "" {
				return m
			}
		}
	}
	return "mount"
}

// forceRemountOnRefresh is true when soft STS conf rewrite is disabled in favor of full remount.
func forceRemountOnRefresh() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH")))
	return v == "1" || v == "true" || v == "yes"
}

// mountPointUnreachable returns true if path cannot be listed within timeout
// (stuck FUSE / WinFsp, missing mount). Empty path is treated as unreachable.
// sync_workspace modes should not call this for process health.
func mountPointUnreachable(path string, timeout time.Duration) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ch := make(chan error, 1)
	go func() {
		_, err := os.ReadDir(path)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err != nil
	case <-time.After(timeout):
		return true
	}
}

// mountDead reports whether a mount-mode rclone process has exited.
func mountDead(mp *mountProc) bool {
	if mp == nil || mp.mode == "sync_workspace" {
		return false
	}
	if mp.cmd == nil || mp.cmd.Process == nil {
		return false
	}
	if mp.cmd.ProcessState != nil {
		return true
	}
	// Non-blocking peek via waitCh (filled by Wait goroutine after Start).
	if mp.waitCh != nil {
		select {
		case err, ok := <-mp.waitCh:
			if ok {
				// drain stored; process is dead
				_ = err
				return true
			}
		default:
		}
	}
	return false
}

type mountSpec struct {
	RemotePath string `json:"remote_path"`
	MountPoint string `json:"mount_point"`
	RcloneConf string `json:"rclone_conf"`
}

func (s *sessionBundle) mountSpec() mountSpec {
	if s.Spec.RcloneConf != "" {
		return s.Spec
	}
	return s.Session.Spec
}

func (s *sessionBundle) workspace() string {
	if s.Session.Manifest.Env != nil {
		if v := s.Session.Manifest.Env["AI_CLOUDHUB_WORKSPACE"]; v != "" {
			return v
		}
	}
	return s.mountSpec().MountPoint
}

type mountProc struct {
	cmd          *exec.Cmd
	cancel       func()
	expiresAt    time.Time
	driveID      string
	mode         string
	bindingMode  string // last binding.mode used for change detection
	confPath     string
	remotePath   string
	mountPoint   string
	sessionToken string
	rcloneBin    string
	waitCh       <-chan error // set when Wait runs in background after Start
}

func (m *mountProc) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	rcloneBin := m.rcloneBin
	if rcloneBin == "" {
		rcloneBin = "rclone"
	}
	// sync_workspace: push local changes back before release
	if m.mode == "sync_workspace" && m.confPath != "" && m.remotePath != "" && m.mountPoint != "" {
		log.Printf("sync_workspace push %s -> %s", m.mountPoint, m.remotePath)
		c := exec.Command(rcloneBin, "sync", m.mountPoint, m.remotePath, "--config", m.confPath, "--create-empty-src-dirs")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		_ = c.Run()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		if m.cmd.ProcessState == nil {
			// Windows: Signal(SIGTERM) is often unsupported — kill directly.
			if runtime.GOOS == "windows" {
				_ = m.cmd.Process.Kill()
			} else if err := m.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				_ = m.cmd.Process.Kill()
			}
			if m.waitCh != nil {
				select {
				case <-m.waitCh:
				case <-time.After(8 * time.Second):
					_ = m.cmd.Process.Kill()
					<-m.waitCh
				}
			} else {
				_, _ = m.cmd.Process.Wait()
			}
		}
	}
	// Best-effort unmount leftover (Linux fusermount; no-op when already gone).
	if m.mode == "mount" && m.mountPoint != "" && runtime.GOOS != "windows" {
		_ = exec.Command("fusermount", "-u", m.mountPoint).Run()
		_ = exec.Command("umount", m.mountPoint).Run()
	}
}

func startMount(stateDir, bindingID string, sess *sessionBundle, mode, rcloneBin string, winFspOK bool) (*mountProc, error) {
	if rcloneBin == "" {
		rcloneBin = "rclone"
	}
	if _, err := exec.LookPath(rcloneBin); err != nil {
		// LookPath may fail for absolute Windows paths; Stat absolute instead.
		if st, e2 := os.Stat(rcloneBin); e2 != nil || st.IsDir() {
			return nil, fmt.Errorf("rclone not found (%s) — install https://rclone.org/downloads/ or scripts\\windows\\install-deps.ps1", rcloneBin)
		}
	}
	dir := filepath.Join(stateDir, bindingID)
	_ = os.MkdirAll(dir, 0o700)
	confPath := filepath.Join(dir, "rclone.conf")
	spec := sess.mountSpec()
	if spec.RcloneConf == "" {
		return nil, fmt.Errorf("empty rclone_conf in session")
	}
	if err := os.WriteFile(confPath, []byte(spec.RcloneConf), 0o600); err != nil {
		return nil, err
	}
	if len(sess.Manifest) > 0 {
		_ = os.WriteFile(filepath.Join(dir, "manifest.json"), sess.Manifest, 0o600)
	}
	var envBody string
	for k, v := range sess.Session.Manifest.Env {
		envBody += k + "=" + v + "\n"
	}
	_ = os.WriteFile(filepath.Join(dir, "env"), []byte(envBody), 0o600)

	mp := spec.MountPoint
	if mp == "" {
		return nil, fmt.Errorf("empty mount_point")
	}

	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = resolveMode("", sess)
	}

	// Windows: drive letters are volume mounts — do not MkdirAll.
	if runtimeenv.IsWindowsDriveLetter(mp) {
		if mode == "sync_workspace" {
			return nil, fmt.Errorf("sync_workspace requires a directory mount_point, not drive letter %q — use e.g. C:\\Users\\…\\aihub-ws", mp)
		}
	} else {
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir mount_point %q: %w", mp, err)
		}
	}

	// Windows mount mode requires WinFsp (fail fast with install hint).
	if mode == "mount" && runtime.GOOS == "windows" && !winFspOK {
		return nil, fmt.Errorf("mount mode requires WinFsp — run scripts\\windows\\install-deps.ps1 (admin) or set binding mode=sync_workspace; see docs/WINDOWS.md")
	}

	mpProc := &mountProc{
		mode:       mode,
		confPath:   confPath,
		remotePath: spec.RemotePath,
		mountPoint: mp,
		rcloneBin:  rcloneBin,
	}

	if mode == "sync_workspace" {
		// pull remote -> local once; agent works on real local SSD
		log.Printf("sync_workspace pull %s -> %s", spec.RemotePath, mp)
		pull := exec.Command(rcloneBin, "sync", spec.RemotePath, mp, "--config", confPath, "--create-empty-src-dirs")
		pull.Stdout = os.Stdout
		pull.Stderr = os.Stderr
		if err := pull.Run(); err != nil {
			return nil, fmt.Errorf("sync pull: %w", err)
		}
		// no long-lived mount process; directory is local
		return mpProc, nil
	}

	cmd := exec.Command(rcloneBin, "mount", spec.RemotePath, mp,
		"--config", confPath,
		"--vfs-cache-mode", "full",
		"--dir-cache-time", "10s",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("rclone mount start: %w (WinFsp required on Windows; install-deps.ps1)", err)
	}
	// Fail fast if rclone dies immediately (common when WinFsp missing).
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			hint := ""
			if runtime.GOOS == "windows" {
				hint = " — check WinFsp (scripts\\windows\\install-deps.ps1) or use mode=sync_workspace"
			}
			return nil, fmt.Errorf("rclone mount exited: %w%s", err, hint)
		}
	case <-time.After(1500 * time.Millisecond):
		// still running — keep waitCh for stop()
		mpProc.waitCh = done
	}
	mpProc.cmd = cmd
	mpProc.cancel = func() {}
	return mpProc, nil
}

func listBindings(api, token, device string) ([]bindingDTO, error) {
	url := api + "/v1/bindings?device_id=" + device
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, b)
	}
	var out struct {
		Items []bindingDTO `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func issueSession(api, token, bindingID string) (*sessionBundle, error) {
	url := api + "/v1/bindings/" + bindingID + "/session"
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, body)
	}
	var sb sessionBundle
	if err := json.Unmarshal(body, &sb); err != nil {
		return nil, err
	}
	return &sb, nil
}

func refreshSession(api, token, sessionToken, driveID string) (*sessionBundle, error) {
	payload, _ := json.Marshal(map[string]string{
		"session_token": sessionToken,
		"drive_id":      driveID,
	})
	req, _ := http.NewRequest(http.MethodPost, api+"/v1/sessions/refresh", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, body)
	}
	var sb sessionBundle
	if err := json.Unmarshal(body, &sb); err != nil {
		return nil, err
	}
	return &sb, nil
}

func reportActual(api, token, bindingID, actual, lastErr string) error {
	url := api + "/v1/bindings/" + bindingID + "/report"
	payload, _ := json.Marshal(map[string]string{"actual": actual, "last_error": lastErr})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("report actual %s=%s: %v", bindingID, actual, err)
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		log.Printf("report actual %s=%s HTTP %d: %s", bindingID, actual, res.StatusCode, string(b))
		return fmt.Errorf("report HTTP %d: %s", res.StatusCode, string(b))
	}
	return nil
}

// postBarrier signals control plane that VFS/write cache is flushed after unmount.
func postBarrier(api, token, driveID, deviceID string) error {
	if driveID == "" {
		return nil
	}
	url := api + "/v1/drives/" + driveID + "/barrier"
	payload, _ := json.Marshal(map[string]string{
		"status":    "ok",
		"device_id": deviceID,
		"note":      "hubd unmount flush",
	})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("barrier %s: %v", driveID, err)
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		log.Printf("barrier %s: HTTP %d: %s", driveID, res.StatusCode, b)
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, b)
	}
	log.Printf("barrier ok drive=%s device=%s", driveID, deviceID)
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
