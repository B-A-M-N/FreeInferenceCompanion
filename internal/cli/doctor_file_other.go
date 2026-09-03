//go:build !linux && !darwin

package cli

import "os"

// Other release targets do not provide the Unix O_NOFOLLOW primitive. The
// descriptor is still fstat'ed by readDoctorFile before bounded reading.
func openDoctorFile(path string) (*os.File, error) {
	return os.Open(path)
}
