//go:build !linux && !darwin

package tracing

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Platforms without openat/O_NOFOLLOW still validate every component before
// use. Release artifacts currently target Linux and macOS, where the Unix
// implementation provides descriptor-based no-follow creation.
func ensurePrivateDirNoFollow(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == string(filepath.Separator) {
		return errors.New("trace receipt directory path must be an absolute non-root path")
	}
	if err := os.MkdirAll(clean, 0700); err != nil {
		return err
	}
	current := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(clean, current), current)
	privateStart := len(components) - 2
	for i, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("trace receipt directory is not a private directory")
		}
		if i >= privateStart {
			if err := os.Chmod(current, 0700); err != nil {
				return err
			}
		}
	}
	return nil
}
