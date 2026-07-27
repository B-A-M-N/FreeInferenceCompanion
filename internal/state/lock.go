package state

import (
	"fmt"
	"os"
	"syscall"
)

// Lock is a non-blocking advisory file lock.
type Lock struct {
	path string
	f    *os.File
}

// NewLock creates a Lock for the given path but does not acquire it.
func NewLock(path string) *Lock {
	return &Lock{path: path}
}

// TryAcquire attempts to acquire an exclusive non-blocking lock.
// Returns true if acquired, false if already held by another process.
func (l *Lock) TryAcquire() (bool, error) {
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false, fmt.Errorf("open lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return false, nil // busy
	}

	l.f = f
	return true, nil
}

// Release releases the lock.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlock: %w", err)
	}
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("close lock: %w", err)
	}
	l.f = nil
	return nil
}