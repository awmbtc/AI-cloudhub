package main

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestResolveModePrecedence(t *testing.T) {
	sess := &sessionBundle{}
	sess.Session.Mode = "sync_workspace"
	sess.Session.Manifest.Env = map[string]string{"AI_CLOUDHUB_MODE": "mount"}

	if got := resolveMode("sync_workspace", sess); got != "sync_workspace" {
		t.Fatalf("binding wins: %q", got)
	}
	if got := resolveMode("", sess); got != "sync_workspace" {
		t.Fatalf("session.mode wins over env: %q", got)
	}
	sess.Session.Mode = ""
	if got := resolveMode("", sess); got != "mount" {
		t.Fatalf("env: %q", got)
	}
	sess.Session.Manifest.Env = nil
	if got := resolveMode("", sess); got != "mount" {
		t.Fatalf("default: %q", got)
	}
	if got := resolveMode("  mount  ", nil); got != "mount" {
		t.Fatalf("trim: %q", got)
	}
}

func TestMountDeadProcessState(t *testing.T) {
	if mountDead(nil) {
		t.Fatal("nil")
	}
	if mountDead(&mountProc{mode: "sync_workspace"}) {
		t.Fatal("sync never dead via process")
	}

	// finished process → ProcessState set
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit", "0")
	} else {
		cmd = exec.Command("true")
	}
	if err := cmd.Run(); err != nil {
		t.Skip("cannot run exit 0 helper:", err)
	}
	mp := &mountProc{mode: "mount", cmd: cmd}
	if !mountDead(mp) {
		t.Fatal("want dead when ProcessState set")
	}
}

func TestMountPointUnreachable(t *testing.T) {
	if !mountPointUnreachable("", time.Second) {
		t.Fatal("empty path should be unreachable")
	}
	// Temp dir is always listable.
	dir := t.TempDir()
	if mountPointUnreachable(dir, 2*time.Second) {
		t.Fatalf("temp dir should be reachable: %s", dir)
	}
	// Nonexistent path.
	if !mountPointUnreachable(dir+"/does-not-exist-xyz", 2*time.Second) {
		t.Fatal("missing path should be unreachable")
	}
}

func TestForceRemountOnRefreshEnv(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH", "")
	if forceRemountOnRefresh() {
		t.Fatal("default off")
	}
	t.Setenv("AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH", "1")
	if !forceRemountOnRefresh() {
		t.Fatal("want true for 1")
	}
	t.Setenv("AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH", "true")
	if !forceRemountOnRefresh() {
		t.Fatal("want true for true")
	}
}

func TestMountDeadAfterKill(t *testing.T) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "-n", "30", "127.0.0.1")
	} else {
		cmd = exec.Command("sleep", "30")
	}
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start long process:", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("kill wait timeout")
	}
	mp := &mountProc{mode: "mount", cmd: cmd, waitCh: done}
	if !mountDead(mp) {
		t.Fatal("want dead after kill")
	}
}
