//go:build windows

package state

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func acquireFileLock(lock *FileLock, blocking bool) error {
	f, err := os.OpenFile(lock.path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	if err := validateLockFile(f); err != nil {
		_ = f.Close()
		return err
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !blocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{}); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return ErrLockBusy
		}
		return fmt.Errorf("acquire lock: %w", err)
	}
	lock.f = f
	return nil
}

func releaseFileLock(lock *FileLock) error {
	if lock.f == nil {
		return nil
	}
	if err := windows.UnlockFileEx(windows.Handle(lock.f.Fd()), 0, 1, 0, &windows.Overlapped{}); err != nil {
		_ = lock.f.Close()
		lock.f = nil
		return fmt.Errorf("unlock: %w", err)
	}
	if err := lock.f.Close(); err != nil {
		lock.f = nil
		return fmt.Errorf("close lock: %w", err)
	}
	lock.f = nil
	return nil
}
