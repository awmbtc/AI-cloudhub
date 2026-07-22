// runner — AI-cloudhub cloud runtime (BYOC).
//
// Modes:
//  1) One-shot: set AI_CLOUDHUB_DRIVE_ID (or BINDING_ID) and optional command after --
//  2) Worker:   AI_CLOUDHUB_WORKER=1  → poll claim next job, run, complete (still user compute)
//
// Never run as a platform multi-tenant mega-pool (docs/DECISIONS.md D-001).
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
	"strings"
	"syscall"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/sandbox"
)

func main() {
	api := env("AI_CLOUDHUB_API", "http://127.0.0.1:8080")
	token := os.Getenv("AI_CLOUDHUB_TOKEN")
	if token == "" {
		log.Fatal("AI_CLOUDHUB_TOKEN required")
	}
	mountPoint := env("AI_CLOUDHUB_MOUNT", "/workspace")

	if env("AI_CLOUDHUB_WORKER", "") == "1" || env("AI_CLOUDHUB_WORKER", "") == "true" {
		runWorker(api, token, mountPoint)
		return
	}

	driveID := os.Getenv("AI_CLOUDHUB_DRIVE_ID")
	bindingID := os.Getenv("AI_CLOUDHUB_BINDING_ID")
	if driveID == "" && bindingID == "" {
		log.Fatal("set AI_CLOUDHUB_DRIVE_ID / BINDING_ID, or AI_CLOUDHUB_WORKER=1")
	}
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if err := runOnce(api, token, mountPoint, driveID, bindingID, "", args); err != nil {
		log.Fatal(err)
	}
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
			log.Printf("claimed job %s drive=%s cmd=%v", j.ID, j.DriveID, j.Command)
			mode := j.Mode
			if mode != "" {
				_ = os.Setenv("AI_CLOUDHUB_MODE", mode)
			}
			err = runOnce(api, token, mountPoint, j.DriveID, j.BindingID, j.ID, j.Command)
			ok := err == nil
			note := ""
			if err != nil {
				note = err.Error()
				log.Printf("job %s failed: %v", j.ID, err)
			}
			_ = completeJob(api, token, j.ID, ok, note)
		}
	}
}

type jobDTO struct {
	ID        string   `json:"id"`
	DriveID   string   `json:"drive_id"`
	BindingID string   `json:"binding_id"`
	Mode      string   `json:"mode"`
	Command   []string `json:"command"`
	Status    string   `json:"status"`
}

func claimNext(api, token string) (*jobDTO, error) {
	req, _ := http.NewRequest(http.MethodPost, api+"/v1/jobs/next/claim", nil)
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
	var j jobDTO
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func completeJob(api, token, id string, ok bool, note string) error {
	payload, _ := json.Marshal(map[string]interface{}{"ok": ok, "note": note})
	req, _ := http.NewRequest(http.MethodPost, api+"/v1/jobs/"+id+"/complete", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return nil
}

func runOnce(api, token, mountPoint, driveID, bindingID, jobID string, args []string) error {
	log.Printf("AI-cloudhub runner (BYOC) api=%s mount=%s job=%s", api, mountPoint, jobID)

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
		return fmt.Errorf("session: %w", err)
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
		return err
	}
	spec := bundle.Spec
	if spec.RcloneConf == "" {
		spec = bundle.Session.Spec
	}
	if spec.MountPoint != "" {
		mountPoint = spec.MountPoint
	}

	if _, err := exec.LookPath("rclone"); err != nil {
		return fmt.Errorf("rclone not found in PATH")
	}

	state := filepath.Join(os.TempDir(), "ai-cloudhub-runner")
	_ = os.MkdirAll(state, 0o700)
	confPath := filepath.Join(state, "rclone.conf")
	if err := os.WriteFile(confPath, []byte(spec.RcloneConf), 0o600); err != nil {
		return err
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
			return fmt.Errorf("sync pull: %w", err)
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
			return fmt.Errorf("mount: %w", err)
		}
		for i := 0; i < 40; i++ {
			if st, err := os.Stat(mountPoint); err == nil && st.IsDir() {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
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
	extra := map[string]string{}
	for k, v := range bundle.Session.Manifest.Env {
		extra[k] = v
	}
	extra["AI_CLOUDHUB_WORKSPACE"] = mountPoint
	extra["AI_CLOUDHUB_MODE"] = mode
	if jobID != "" {
		extra["AI_CLOUDHUB_JOB_ID"] = jobID
	}
	jailOn := env("AI_CLOUDHUB_JAIL", "1") != "0" && env("AI_CLOUDHUB_JAIL", "1") != "false"
	var childEnv []string
	if jailOn {
		passTok := env("AI_CLOUDHUB_PASS_TOKEN", "") == "1" || env("AI_CLOUDHUB_PASS_TOKEN", "") == "true"
		// Soft network policy: AI_CLOUDHUB_NETWORK=deny strips proxy env (not a kernel netns).
		netDeny := strings.EqualFold(env("AI_CLOUDHUB_NETWORK", ""), "deny") ||
			env("AI_CLOUDHUB_NETWORK", "") == "0" || env("AI_CLOUDHUB_NETWORK", "") == "off"
		childEnv = sandbox.FilterOSEnviron(extra, sandbox.EnvFilter{PassToken: passTok, DenyNetwork: netDeny})
		log.Printf("sandbox v1 env filter on (keys=%d pass_token=%v network_deny=%v)", len(childEnv), passTok, netDeny)
	} else {
		childEnv = os.Environ()
		for k, v := range extra {
			childEnv = append(childEnv, k+"="+v)
		}
	}

	if len(args) == 0 {
		log.Printf("ready mode=%s path=%s (no command); waiting signal", mode, mountPoint)
		<-sig
		return nil
	}

	// Path jail: reject command args that resolve outside workspace.
	if jailOn {
		jail := sandbox.NewPathJail(mountPoint)
		if err := jail.Allow(mountPoint); err != nil {
			return fmt.Errorf("jail mount: %w", err)
		}
		for _, a := range args[1:] {
			// only check path-looking args (absolute or containing / or ..)
			if a == "" || (!strings.Contains(a, "/") && !strings.Contains(a, `\`) && !strings.Contains(a, "..")) {
				continue
			}
			if err := jail.Allow(a); err != nil {
				return fmt.Errorf("path jail: arg %q: %w", a, err)
			}
		}
	}

	// Optional in-process seccomp (Linux pure-Go BPF; no-op elsewhere).
	// Apply after env/path jail and mount setup, immediately before agent.
	// AI_CLOUDHUB_SECCOMP=1|true|yes; on error continue unless AI_CLOUDHUB_SECCOMP_STRICT=1.
	if sandbox.Enabled() {
		if err := sandbox.ApplyRunnerDefault(); err != nil {
			if sandbox.Strict() {
				return fmt.Errorf("seccomp: %w", err)
			}
			log.Printf("seccomp: apply failed (continuing): %v", err)
		} else {
			log.Printf("seccomp: filter applied profile=%s (no_new_privs+tsync)", sandbox.EffectiveProfile())
		}
	}

	agent := exec.Command(args[0], args[1:]...)
	agent.Dir = mountPoint
	agent.Env = childEnv
	agent.Stdout = os.Stdout
	agent.Stderr = os.Stderr
	agent.Stdin = os.Stdin
	if err := agent.Run(); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return nil
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
