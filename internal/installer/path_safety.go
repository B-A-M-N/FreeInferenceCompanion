package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrPathEscapesRoot reports an attempt to traverse outside the trusted root,
// including traversal through a symlinked ancestor.
var ErrPathEscapesRoot = errors.New("path escapes trusted configuration root")

// normalizeRoot returns the trusted absolute configuration root.
func normalizeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("configuration root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("configuration root is not a real directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

// ensureContainedPath rejects every symlinked component between root and the
// final path component. The target itself is not followed; callers decide
// whether an existing final symlink is an owned replacement candidate.
func ensureContainedPath(root, target string) error {
	trustedRoot, err := normalizeRoot(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	targetAbs = filepath.Clean(targetAbs)
	relative, err := filepath.Rel(trustedRoot, targetAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s", ErrPathEscapesRoot, targetAbs)
	}
	if relative == "." {
		return fmt.Errorf("%w: target is the configuration root", ErrPathEscapesRoot)
	}
	current := trustedRoot
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlinked path component %s", ErrPathEscapesRoot, current)
		}
		if !info.IsDir() && current != targetAbs {
			return fmt.Errorf("%w: non-directory path component %s", ErrPathEscapesRoot, current)
		}
	}
	return nil
}

// safeMkdirAll creates directories only through non-symlinked ancestors below
// the trusted root. It deliberately avoids traversing an existing symlink.
func safeMkdirAll(root, targetDir string) error {
	if err := ensureContainedPath(root, targetDir); err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	// A race could have substituted a symlink after validation. Re-read the
	// complete chain before returning a writable path to the transaction.
	return ensureContainedPath(root, targetDir)
}

// ensureNoSymlinkedAncestors checks every existing component above path. It is
// used immediately before filesystem mutations that cannot be scoped to a
// trusted root handle on every supported platform. Callers repeat the check
// after creating missing parents and immediately before each rename.
func ensureNoSymlinkedAncestors(path string) error {
	absolute, err := canonicalMutationPath(path)
	if err != nil {
		return err
	}
	current := filepath.Dir(filepath.Clean(absolute))
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing symlinked ancestor %s", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("refusing non-directory ancestor %s", current)
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// canonicalMutationPath accounts for macOS's system compatibility links while
// keeping user-controlled symlinks subject to the no-follow checks above. The
// temporary directory returned by macOS commonly begins with /var, which is a
// stable link to /private/var (and /tmp likewise maps to /private/tmp).
func canonicalMutationPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || runtime.GOOS != "darwin" {
		return filepath.Clean(absolute), err
	}
	absolute = filepath.Clean(absolute)
	for _, prefix := range []string{"/var", "/tmp"} {
		if absolute != prefix && !strings.HasPrefix(absolute, prefix+string(filepath.Separator)) {
			continue
		}
		info, statErr := os.Lstat(prefix)
		if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, resolveErr := filepath.EvalSymlinks(prefix)
		if resolveErr != nil {
			return "", resolveErr
		}
		suffix := strings.TrimPrefix(absolute, prefix)
		suffix = strings.TrimPrefix(suffix, string(filepath.Separator))
		absolute = filepath.Join(resolved, suffix)
		break
	}
	return filepath.Clean(absolute), nil
}
