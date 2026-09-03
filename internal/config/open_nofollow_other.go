//go:build !linux && !darwin

package config

import "os"

// Platforms without the Unix O_NOFOLLOW primitive still validate the opened
// descriptor in Config.Load. Release builds currently target Linux and macOS.
func openConfigNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
