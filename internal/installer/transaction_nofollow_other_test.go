//go:build !linux && !darwin

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFallbackMutationParentRejectsSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(root, "external")
	linked := filepath.Join(root, "linked")
	if err := os.MkdirAll(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, linked); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	_, err := openMutationParent(filepath.Join(linked, "child"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("fallback mutation parent accepted symlinked ancestor: %v", err)
	}
}
