//go:build !linux && !darwin

package tracing

import "os"

// Platforms without O_NOFOLLOW cannot provide the same no-follow guarantee;
// callers still validate the descriptor and receipt before consuming it.
func openReceiptNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
