package config

import "os"

// OpenNoFollow opens a configuration file through the same platform-specific
// no-follow path used by Config.Load. Callers must still fstat and bound reads
// from the returned descriptor.
func OpenNoFollow(path string) (*os.File, error) {
	return openConfigNoFollow(path)
}
