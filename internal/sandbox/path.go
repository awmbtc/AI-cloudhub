// Package sandbox provides runtime path jail helpers (ROADMAP-2.0 stage A).
package sandbox

import (
	"fmt"
	"os"
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
// When the path (or an existing parent) resolves via symlink, EvalSymlinks is
// applied and the resolved target is re-checked under Root (blocks symlink escape).
func (j *PathJail) Allow(path string) error {
	if j == nil {
		return fmt.Errorf("sandbox: nil jail")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("sandbox: empty path")
	}
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("sandbox: invalid path")
	}

	lexicalRoot := j.Root
	resolvedRoot := lexicalRoot
	if rr, err := filepath.EvalSymlinks(lexicalRoot); err == nil {
		resolvedRoot = rr
	}

	var candidate string
	if filepath.IsAbs(path) || isWindowsDrive(path) {
		candidate = filepath.Clean(path)
	} else {
		candidate = filepath.Clean(filepath.Join(resolvedRoot, path))
	}

	// Lexical containment against either form of root (macOS /var vs /private/var).
	if underRoot(lexicalRoot, candidate) != nil && underRoot(resolvedRoot, candidate) != nil {
		return fmt.Errorf("sandbox: path outside workspace: %s", path)
	}

	resolved, err := evalExisting(candidate)
	if err != nil {
		return nil
	}
	if underRoot(resolvedRoot, resolved) != nil && underRoot(lexicalRoot, resolved) != nil {
		return fmt.Errorf("sandbox: symlink escape: %s -> %s", path, resolved)
	}
	return nil
}


func underRoot(root, candidate string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return fmt.Errorf("outside")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("outside")
	}
	return nil
}

// evalExisting resolves symlinks for candidate when it exists, or for the
// longest existing parent so link/newfile still evaluates through the link.
func evalExisting(candidate string) (string, error) {
	if _, err := os.Lstat(candidate); err == nil {
		return filepath.EvalSymlinks(candidate)
	}
	dir := filepath.Dir(candidate)
	for {
		if _, err := os.Lstat(dir); err == nil {
			resolvedDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(dir, candidate)
			if err != nil {
				return "", err
			}
			return filepath.Clean(filepath.Join(resolvedDir, rel)), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
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
