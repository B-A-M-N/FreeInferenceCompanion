//go:build linux || darwin

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLoadRejectsFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := Load()
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("FIFO config must be rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("loading FIFO config blocked")
	}
	_ = os.Remove(path)
}
