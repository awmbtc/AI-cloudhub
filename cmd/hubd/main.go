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
	"syscall"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/runtimeenv"
)

func main() {
	api := env("AI_CLOUDHUB_API", "http://127.0.0.1:8080")
	token := os.Getenv("AI_CLOUDHUB_TOKEN")
	if token == "" {
		log.Fatal("AI_CLOUDHUB_TOKEN required")
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
	// Hard fail only when rclone is missing. On Windows, missing WinFsp is a
	// warning: mount mode may fail, but mode=sync_workspace can still work.
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
		log.Printf("warning: WinFsp not detected — FUSE mount may fail")
		log.Printf("install WinFsp+rclone: powershell -ExecutionPolicy Bypass -File scripts\\windows\\install-deps.ps1")
		log.Printf("docs: docs/WINDOWS.md — or use mode=sync_workspace without WinFsp")
	}

	log.Printf("AI-cloudhub hubd starting api=%s device=%s", api, device)

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
		log.Printf("mount binding %s -> %s", id, b.MountPoint)
		sess, err := issueSession(api, token, id)
		if err != nil {
			log.Printf("session %s: %v", id, err)
			_ = reportActual(api, token, id, "error", err.Error())
			return
		}
		mp, err := startMount(stateDir, id, sess)
		if err != nil {
			log.Printf("mount %s: %v", id, err)
			_ = reportActual(api, token, id, "error", err.Error())
			return
		}
		mp.expiresAt = sess.Session.ExpiresAt
		mp.sessionToken = sess.Session.Token
		mp.driveID = b.DriveID
		active[id] = mp
		_ = reportActual(api, token, id, "mounted", "")
		log.Printf("mounted %s drive=%s workspace=%s expires=%s", id, b.DriveID, sess.workspace(), sess.Session.ExpiresAt.Format(time.RFC3339))
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
		// mount new or soft-refresh sessions expiring within 5 minutes
		for id, b := range want {
			mp, ok := active[id]
			if !ok {
				remount(id, b)
				continue
			}
			if !mp.expiresAt.IsZero() && time.Until(mp.expiresAt) < 5*time.Minute {
				log.Printf("refresh session for binding %s (expires soon)", id)
				if mp.sessionToken != "" && mp.driveID != "" {
					if nb, err := refreshSession(api, token, mp.sessionToken, mp.driveID); err == nil {
						spec := nb.mountSpec()
						if mp.confPath != "" && spec.RcloneConf != "" {
							if err := os.WriteFile(mp.confPath, []byte(spec.RcloneConf), 0o600); err == nil {
								mp.expiresAt = nb.Session.ExpiresAt
								mp.sessionToken = nb.Session.Token
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
		Spec      mountSpec `json:"spec"`
		Manifest  struct {
			Env map[string]string `json:"env"`
		} `json:"manifest"`
	} `json:"session"`
	Manifest json.RawMessage `json:"manifest"`
	Spec     mountSpec       `json:"spec"`
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
	confPath     string
	remotePath   string
	mountPoint   string
	sessionToken string
}

func (m *mountProc) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	// sync_workspace: push local changes back before release
	if m.mode == "sync_workspace" && m.confPath != "" && m.remotePath != "" && m.mountPoint != "" {
		log.Printf("sync_workspace push %s -> %s", m.mountPoint, m.remotePath)
		c := exec.Command("rclone", "sync", m.mountPoint, m.remotePath, "--config", m.confPath, "--create-empty-src-dirs")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		_ = c.Run()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Signal(syscall.SIGTERM)
		_, _ = m.cmd.Process.Wait()
	}
}

func startMount(stateDir, bindingID string, sess *sessionBundle) (*mountProc, error) {
	if _, err := exec.LookPath("rclone"); err != nil {
		return nil, fmt.Errorf("rclone not found in PATH")
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
	_ = os.MkdirAll(mp, 0o755)

	mode := "mount"
	if sess.Session.Manifest.Env != nil {
		if m := sess.Session.Manifest.Env["AI_CLOUDHUB_MODE"]; m != "" {
			mode = m
		}
	}
	// also check top-level mode field if present in JSON via session
	if mode == "" {
		mode = "mount"
	}

	mpProc := &mountProc{
		mode:       mode,
		confPath:   confPath,
		remotePath: spec.RemotePath,
		mountPoint: mp,
	}

	if mode == "sync_workspace" {
		// pull remote -> local once; agent works on real local SSD
		log.Printf("sync_workspace pull %s -> %s", spec.RemotePath, mp)
		pull := exec.Command("rclone", "sync", spec.RemotePath, mp, "--config", confPath, "--create-empty-src-dirs")
		pull.Stdout = os.Stdout
		pull.Stderr = os.Stderr
		if err := pull.Run(); err != nil {
			return nil, fmt.Errorf("sync pull: %w", err)
		}
		// no long-lived mount process; directory is local
		return mpProc, nil
	}

	cmd := exec.Command("rclone", "mount", spec.RemotePath, mp,
		"--config", confPath,
		"--vfs-cache-mode", "full",
		"--dir-cache-time", "10s",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
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
		return err
	}
	defer res.Body.Close()
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
