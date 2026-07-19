// Package sandbox provides runtime path jail helpers (ROADMAP-2.0 stage A).
package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PathJail restricts filesystem paths to a workspace root.
type PathJail struct {
	// Root is the allowed mount / workspace (e.g. /workspace).
	Root string
}

// NewPathJail cleans root. Empty root defaults to /workspace.
func NewPathJail(root string) *PathJail {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "/workspace"
	}
	// Clean but keep absolute if possible.
	if !filepath.IsAbs(root) && !isWindowsDrive(root) {
		// relative roots still cleaned
	}
	return &PathJail{Root: filepath.Clean(root)}
}

// Allow reports whether path is under Root (after clean).
// path may be absolute or relative to Root.
func (j *PathJail) Allow(path string) error {
	if j == nil {
		return fmt.Errorf("sandbox: nil jail")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("sandbox: empty path")
	}
	// Reject null bytes and obvious escapes in raw form.
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("sandbox: invalid path")
	}

	root := j.Root
	var candidate string
	if filepath.IsAbs(path) || isWindowsDrive(path) {
		candidate = filepath.Clean(path)
	} else {
		// relative → under root
		candidate = filepath.Clean(filepath.Join(root, path))
	}

	// filepath.Rel fails if on different volumes (Windows); treat as deny.
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return fmt.Errorf("sandbox: path outside workspace")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("sandbox: path outside workspace: %s", path)
	}
	return nil
}

// AllowAll checks every path; returns first error.
func (j *PathJail) AllowAll(paths ...string) error {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := j.Allow(p); err != nil {
			return err
		}
	}
	return nil
}

func isWindowsDrive(p string) bool {
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	}
	return false
}
