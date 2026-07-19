package sandbox

import "testing"

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
