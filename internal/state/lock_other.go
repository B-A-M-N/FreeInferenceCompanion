//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package state

import (
	"fmt"
	"os"
)

// Unsupported targets retain the file ownership and validation contract, but
// do not claim inter-process advisory locking that their standard library does
// not expose. Production targets use lock_unix.go or lock_windows.go.
func acquireFileLock(lock *FileLock, _ bool) error {
	f, err := os.OpenFile(lock.path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	if err := validateLockFile(f); err != nil {
		_ = f.Close()
		return err
	}
	lock.f = f
	return nil
}

func releaseFileLock(lock *FileLock) error {
	if lock.f == nil {
		return nil
	}
	if err := lock.f.Close(); err != nil {
		lock.f = nil
		return fmt.Errorf("close lock: %w", err)
	}
	lock.f = nil
	return nil
}
