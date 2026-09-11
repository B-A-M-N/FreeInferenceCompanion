//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package state

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func acquireFileLock(lock *FileLock, blocking bool) error {
	f, err := os.OpenFile(lock.path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	if err := validateLockFile(f); err != nil {
		_ = f.Close()
		return err
	}
	flags := syscall.LOCK_EX
	if !blocking {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), flags); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
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
	if err := syscall.Flock(int(lock.f.Fd()), syscall.LOCK_UN); err != nil {
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
