//go:build linux

package sandbox

import "testing"

func TestRunnerAllowlistNetDenyStripsSocket(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_SECCOMP_PROFILE", "strict")
	t.Setenv("AI_CLOUDHUB_SECCOMP_NET", "")
	if !hasName(runnerAllowlist(), "socket") {
		t.Fatal("strict should allow unrestricted socket name")
	}

	t.Setenv("AI_CLOUDHUB_SECCOMP_NET", "deny")
	list := runnerAllowlist()
	if hasName(list, "socket") || hasName(list, "socketpair") {
		t.Fatalf("netdeny must strip unrestricted socket names: %v", list)
	}
	if !hasName(list, "openat") {
		t.Fatal("openat should remain")
	}
}
