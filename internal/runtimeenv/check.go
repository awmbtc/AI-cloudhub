package runtimeenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Report is a preflight check for hubd/runner hosts.
type Report struct {
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	RcloneOK     bool     `json:"rclone_ok"`
	RclonePath   string   `json:"rclone_path,omitempty"`
	RcloneVer    string   `json:"rclone_version,omitempty"`
	FuseHint     string   `json:"fuse_hint,omitempty"`
	WinFspOK     bool     `json:"winfsp_ok,omitempty"`
	WinFspHint   string   `json:"winfsp_hint,omitempty"`
	InstallHint  string   `json:"install_hint,omitempty"`
	OK           bool     `json:"ok"`
	Warnings     []string `json:"warnings,omitempty"`
	Errors       []string `json:"errors,omitempty"`
}

// Check inspects the local machine for mount readiness.
func Check() Report {
	r := Report{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
	path, err := exec.LookPath("rclone")
	if err != nil {
		r.Errors = append(r.Errors, "rclone not found in PATH — install https://rclone.org/downloads/")
		if runtime.GOOS == "windows" {
			r.InstallHint = windowsInstallHint()
			r.Errors = append(r.Errors, "Windows: run scripts\\windows\\install-deps.ps1 (or install-deps.bat) to install rclone + WinFsp")
		}
	} else {
		r.RcloneOK = true
		r.RclonePath = path
		if out, err := exec.Command(path, "version").CombinedOutput(); err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) > 0 {
				r.RcloneVer = strings.TrimSpace(lines[0])
			}
		}
	}

	switch runtime.GOOS {
	case "windows":
		r.WinFspHint = "Windows mount requires WinFsp: https://winfsp.dev/rel/ — install then restart shell"
		found, where := detectWinFsp()
		r.WinFspOK = found
		if found {
			r.WinFspHint = "WinFsp detected: " + where
		} else {
			r.InstallHint = windowsInstallHint()
			r.Warnings = append(r.Warnings,
				"WinFsp not detected; rclone mount may fail on Windows — run scripts\\windows\\install-deps.ps1 (mount-only; sync_workspace still works)")
		}
	case "darwin":
		r.FuseHint = "macOS: install macFUSE (https://osxfuse.github.io/) or use rclone mount backend supported on your OS version"
		if _, err := exec.LookPath("mount_macfuse"); err != nil {
			if _, err2 := os.Stat("/Library/Filesystems/macfuse.fs"); err2 != nil {
				r.Warnings = append(r.Warnings, "macFUSE not clearly installed; mount mode may fail — try mode=sync_workspace")
			}
		}
	case "linux":
		r.FuseHint = "Linux: need FUSE (fusermount). Most distros: apt install fuse3 / fuse"
		if _, err := exec.LookPath("fusermount"); err != nil {
			if _, err2 := exec.LookPath("fusermount3"); err2 != nil {
				r.Warnings = append(r.Warnings, "fusermount not found; mount mode may fail — try mode=sync_workspace")
			}
		}
	}

	r.OK = len(r.Errors) == 0
	return r
}

// windowsInstallHint points users at the repo installer scripts.
func windowsInstallHint() string {
	return "Install deps: powershell -ExecutionPolicy Bypass -File scripts\\windows\\install-deps.ps1  (or double-click scripts\\windows\\install-deps.bat). See docs/WINDOWS.md"
}

// detectWinFsp best-effort: common install paths + optional registry via reg.exe.
func detectWinFsp() (bool, string) {
	var candidates []string
	for _, root := range []string{
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramFiles"),
		`C:\Program Files (x86)`,
		`C:\Program Files`,
	} {
		if root == "" {
			continue
		}
		bin := filepath.Join(root, "WinFsp", "bin")
		candidates = append(candidates,
			filepath.Join(bin, "launcher-x64.exe"),
			filepath.Join(bin, "launcher-x86.exe"),
			filepath.Join(bin, "winfsp-x64.dll"),
			filepath.Join(bin, "winfsp-x86.dll"),
		)
	}
	seen := map[string]struct{}{}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return true, c
		}
	}

	if where, ok := detectWinFspRegistry(); ok {
		return true, where
	}
	return false, ""
}

func detectWinFspRegistry() (string, bool) {
	// reg.exe is always present on Windows; ignore errors on other platforms
	keys := []string{
		`HKLM\SOFTWARE\WinFsp`,
		`HKLM\SOFTWARE\WOW6432Node\WinFsp`,
		`HKLM\SYSTEM\CurrentControlSet\Services\WinFsp.Launcher`,
	}
	for _, k := range keys {
		cmd := exec.Command("reg", "query", k)
		if out, err := cmd.CombinedOutput(); err == nil && len(out) > 0 {
			return "registry:" + k, true
		}
	}
	return "", false
}

// RequireOK returns error if hard requirements fail.
func RequireOK() error {
	r := Check()
	if !r.OK {
		return fmt.Errorf("runtimeenv: %s", strings.Join(r.Errors, "; "))
	}
	return nil
}
