//go:build linux || darwin

package cli

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openDoctorFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open doctor file: invalid file descriptor")
	}
	return f, nil
}
