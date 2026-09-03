//go:build linux || darwin

package tracing

import (
	"os"
	"syscall"
)

func openReceiptNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
