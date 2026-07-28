package state

import (
	"testing"
	"time"
)

func TestFileLockContention(t *testing.T) {
	path := t.TempDir() + "/lock"

	first := NewFileLock(path)
	if err := first.Acquire(); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	second := NewFileLock(path)
	start := time.Now()
	err := second.Acquire()
	if !IsLockBusy(err) {
		t.Errorf("expected ErrLockBusy, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("non-blocking acquire must return immediately")
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	third := NewFileLock(path)
	if err := third.Acquire(); err != nil {
		t.Errorf("acquire after release should succeed: %v", err)
	}
	_ = third.Release()
}

func TestFileLockReleaseIdempotent(t *testing.T) {
	l := NewFileLock(t.TempDir() + "/lock")
	if err := l.Release(); err != nil {
		t.Errorf("release without acquire should be a no-op: %v", err)
	}
}
