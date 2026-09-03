//go:build !linux && !darwin

package config

import "os"

// Platforms without the Unix O_NOFOLLOW primitive cannot provide the same
// no-follow guarantee here. Config.Load still validates the opened descriptor
// and bounds content, but symlink replacement races remain unsupported.
func openConfigNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
