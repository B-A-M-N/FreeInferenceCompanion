package state

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrLockBusy is returned when a lock file is already held by another process.
// Callers (especially hooks) should treat this as a fail-open condition.
var ErrLockBusy = errors.New("lock busy")

// IsLockBusy reports whether err is (or wraps) ErrLockBusy.
func IsLockBusy(err error) bool {
	return errors.Is(err, ErrLockBusy)
}

// FileLock is a non-blocking advisory file lock.
type FileLock struct {
	path string
	f    *os.File
}

// NewFileLock creates a FileLock for the given path but does not acquire it.
func NewFileLock(path string) *FileLock {
	return &FileLock{path: path}
}

// Acquire opens the lock file and acquires an exclusive non-blocking flock.
// Returns ErrLockBusy if the lock is already held by another process.
// Validates the lock file is a regular file (not a symlink) after opening.
// Uses O_NOFOLLOW to prevent symlink-following attacks on the lock file itself.
func (l *FileLock) Acquire() error {
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	// Validate the lock file is a regular file with correct permissions
	if err := validateLockFile(f); err != nil {
		f.Close()
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLockBusy
		}
		return fmt.Errorf("acquire lock: %w", err)
	}
	l.f = f
	return nil
}

// AcquireBlocking opens the lock file and acquires an exclusive blocking flock.
// Unlike Acquire, this blocks until the lock is available. Use this for
// background workers (not hooks) where a brief wait is acceptable.
// Validates the lock file is a regular file (not a symlink) after opening.
// Uses O_NOFOLLOW to prevent symlink-following attacks on the lock file itself.
func (l *FileLock) AcquireBlocking() error {
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	// Validate the lock file is a regular file with correct permissions
	if err := validateLockFile(f); err != nil {
		f.Close()
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return fmt.Errorf("acquire blocking lock: %w", err)
	}
	l.f = f
	return nil
}

// validateLockFile ensures the lock file is a regular file with 0600 permissions.
func validateLockFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat lock file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("lock file is not a regular file")
	}
	// Check permissions (allow 0600 or 0644 for backward compat with existing locks)
	mode := info.Mode().Perm()
	if mode != 0600 && mode != 0644 {
		return fmt.Errorf("lock file has unsafe permissions: %o", mode)
	}
	return nil
}

// Release releases the flock and closes the file.
func (l *FileLock) Release() error {
	if l.f == nil {
		return nil
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		l.f.Close()
		l.f = nil
		return fmt.Errorf("unlock: %w", err)
	}
	if err := l.f.Close(); err != nil {
		l.f = nil
		return fmt.Errorf("close lock: %w", err)
	}
	l.f = nil
	return nil
}
