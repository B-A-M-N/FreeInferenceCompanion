//go:build linux || darwin

package tracing

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ensurePrivateDirNoFollow creates and opens every component beneath the
// filesystem root without following symlinks. The two application-owned
// components at the end of the path are made private; system temp-directory
// parents such as /tmp are only verified, never chmod'ed.
func ensurePrivateDirNoFollow(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return errors.New("trace receipt directory path must be absolute")
	}
	relative := strings.TrimPrefix(clean, string(filepath.Separator))
	if relative == "" {
		return errors.New("trace receipt directory path cannot be filesystem root")
	}
	components := strings.Split(relative, string(filepath.Separator))
	privateStart := len(components) - 2

	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open filesystem root: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()

	for i, component := range components {
		if component == "" || component == "." || component == ".." {
			return errors.New("trace receipt directory path contains an unsafe component")
		}
		child, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(fd, component, 0700); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return fmt.Errorf("create trace receipt directory component %q: %w", component, mkdirErr)
			}
			child, openErr = unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			return fmt.Errorf("open trace receipt directory component %q: %w", component, openErr)
		}
		if i >= privateStart {
			if chmodErr := unix.Fchmod(child, 0700); chmodErr != nil {
				unix.Close(child)
				return fmt.Errorf("restrict trace receipt directory component %q: %w", component, chmodErr)
			}
		}
		unix.Close(fd)
		fd = child
	}
	return nil
}
