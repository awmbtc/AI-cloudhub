//go:build !linux

package sandbox

// ApplyRunnerDefault is a no-op on non-Linux platforms.
// Seccomp BPF filters are Linux-only; BYOC runners on macOS/Windows rely on
// path jail + env filter instead (D-001: user machines only).
func ApplyRunnerDefault() error {
	return nil
}
