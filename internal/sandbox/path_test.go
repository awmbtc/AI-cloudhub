package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathJailAllow(t *testing.T) {
	j := NewPathJail("/workspace")
	if err := j.Allow("/workspace"); err != nil {
		t.Fatal(err)
	}
	if err := j.Allow("/workspace/out/a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := j.Allow("rel/file"); err != nil {
		t.Fatal(err)
	}
	if err := j.Allow("/etc/passwd"); err == nil {
		t.Fatal("expected deny /etc")
	}
	if err := j.Allow("../../../etc/passwd"); err == nil {
		t.Fatal("expected deny traversal")
	}
	if err := j.Allow("/workspace/../etc"); err == nil {
		t.Fatal("expected deny cleaned escape")
	}
}

func TestPathJailEmpty(t *testing.T) {
	j := NewPathJail("")
	if j.Root != "/workspace" {
		t.Fatalf("default root %s", j.Root)
	}
	if err := j.Allow(""); err == nil {
		t.Fatal("empty path")
	}
}

func TestPathJailSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// write a secret outside the jail
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlink not supported:", err)
	}
	j := NewPathJail(root)
	if err := j.Allow(filepath.Join(link, "secret.txt")); err == nil {
		t.Fatal("expected deny symlink escape")
	}
	// normal file under root still allowed
	ok := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(ok, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := j.Allow(ok); err != nil {
		t.Fatal(err)
	}
}
