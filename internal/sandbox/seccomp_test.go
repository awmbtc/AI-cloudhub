package sandbox

import (
	"runtime"
	"testing"
)

func TestEnabledAndStrict(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_SECCOMP", "")
	if Enabled() {
		t.Fatal("expected disabled")
	}
	for _, v := range []string{"1", "true", "YES"} {
		t.Setenv("AI_CLOUDHUB_SECCOMP", v)
		if !Enabled() {
			t.Fatalf("Enabled for %q", v)
		}
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP", "0")
	if Enabled() {
		t.Fatal("0 should disable")
	}

	t.Setenv("AI_CLOUDHUB_SECCOMP_STRICT", "")
	if Strict() {
		t.Fatal("strict off")
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP_STRICT", "1")
	if !Strict() {
		t.Fatal("strict on")
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP_STRICT", "false")
	if Strict() {
		t.Fatal("false")
	}
}

func TestProfileNameAndNetDeny(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_SECCOMP_PROFILE", "")
	t.Setenv("AI_CLOUDHUB_SECCOMP_NET", "")
	if ProfileName() != "default" {
		t.Fatal(ProfileName())
	}
	if NetDeny() {
		t.Fatal("net off")
	}

	t.Setenv("AI_CLOUDHUB_SECCOMP_PROFILE", "strict")
	if ProfileName() != "strict" {
		t.Fatal(ProfileName())
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP_PROFILE", "netdeny")
	if ProfileName() != "netdeny" || !NetDeny() {
		t.Fatalf("p=%s net=%v", ProfileName(), NetDeny())
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP_PROFILE", "default")
	t.Setenv("AI_CLOUDHUB_SECCOMP_NET", "deny")
	if !NetDeny() {
		t.Fatal("SECCOMP_NET=deny")
	}
	if EffectiveProfile() != "default+netdeny" {
		t.Fatal(EffectiveProfile())
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP_PROFILE", "netdeny")
	if EffectiveProfile() != "netdeny" {
		t.Fatal(EffectiveProfile())
	}
}

func TestApplyRunnerDefaultNonLinuxNoop(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux applies real filter; skip noop assertion")
	}
	if err := ApplyRunnerDefault(); err != nil {
		t.Fatalf("non-linux ApplyRunnerDefault: %v", err)
	}
}
