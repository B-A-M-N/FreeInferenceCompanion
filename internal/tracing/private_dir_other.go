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
	volume := filepath.VolumeName(clean)
	root := volume + string(filepath.Separator)
	if volume == "" {
		root = string(filepath.Separator)
	}
	if clean == root {
		return errors.New("trace receipt directory path cannot be a volume root")
	}
	relative := strings.TrimPrefix(clean, root)
	if relative == clean || relative == "" {
		return errors.New("trace receipt directory path is not beneath its volume root")
	}
	current := root
	components := strings.Split(relative, string(filepath.Separator))
	privateStart := len(components) - 2
	for i, component := range components {
		if component == "" || component == "." || component == ".." {
			return errors.New("trace receipt directory path contains an unsafe component")
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0700); err != nil {
				return err
			}
			info, err = os.Lstat(current)
		}
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
