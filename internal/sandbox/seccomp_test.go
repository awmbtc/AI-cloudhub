package sandbox

import (
	"runtime"
	"testing"
)

func TestEnabledRespectsEnv(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_SECCOMP", "")
	if Enabled() {
		t.Fatal("empty env should be disabled")
	}

	for _, v := range []string{"1", "true", "TRUE", "yes", "Yes"} {
		t.Setenv("AI_CLOUDHUB_SECCOMP", v)
		if !Enabled() {
			t.Fatalf("Enabled() want true for %q", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off", "maybe"} {
		t.Setenv("AI_CLOUDHUB_SECCOMP", v)
		if Enabled() {
			t.Fatalf("Enabled() want false for %q", v)
		}
	}
}

func TestStrictRespectsEnv(t *testing.T) {
	t.Setenv("AI_CLOUDHUB_SECCOMP_STRICT", "")
	if Strict() {
		t.Fatal("empty env should be non-strict")
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP_STRICT", "1")
	if !Strict() {
		t.Fatal("strict=1")
	}
	t.Setenv("AI_CLOUDHUB_SECCOMP_STRICT", "false")
	if Strict() {
		t.Fatal("strict=false")
	}
}

func TestApplyRunnerDefaultNonLinuxNoop(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux applies a real filter; tested separately when needed")
	}
	if err := ApplyRunnerDefault(); err != nil {
		t.Fatalf("non-linux ApplyRunnerDefault: %v", err)
	}
}
