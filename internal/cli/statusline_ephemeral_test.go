package cli

import "testing"

// Wrappers must never pin binaries in ephemeral storage (Go build cache,
// /tmp) — the file can be garbage-collected and the wrapper silently
// degrades (audit 2026-08-22 P1-2).
func TestResolveSelfPathRejectsEphemeral(t *testing.T) {
	cases := map[string]bool{
		"/home/u/.local/bin/freeinference":    false,
		"/usr/local/bin/freeinference":        false,
		"/home/u/.cache/go-build/cc/abc-d/fi": true,
		"/tmp/TestSomething123/001/fi":        true,
		"/var/tmp/scratch/fi":                 true,
		"/home/user/go/pkg/somebin":           false,
	}
	for path, wantEphemeral := range cases {
		if got := isEphemeralPath(path); got != wantEphemeral {
			t.Errorf("isEphemeralPath(%q) = %v, want %v", path, got, wantEphemeral)
		}
	}
}
