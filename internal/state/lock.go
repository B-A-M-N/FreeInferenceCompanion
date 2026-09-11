package state

import (
	"errors"
	"fmt"
	"os"
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
// Unix uses O_NOFOLLOW to prevent symlink-following attacks on the lock file;
// Windows uses its native file-locking API.
func (l *FileLock) Acquire() error {
	return acquireFileLock(l, false)
}

// AcquireBlocking opens the lock file and acquires an exclusive blocking flock.
// Unlike Acquire, this blocks until the lock is available. Use this for
// background workers (not hooks) where a brief wait is acceptable.
// Validates the lock file is a regular file (not a symlink) after opening.
// Unix uses O_NOFOLLOW to prevent symlink-following attacks on the lock file;
// Windows uses its native file-locking API.
func (l *FileLock) AcquireBlocking() error {
	return acquireFileLock(l, true)
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
	return releaseFileLock(l)
}
