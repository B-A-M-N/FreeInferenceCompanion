package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeMkdirAllRejectsNestedSymlinkAncestor(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "profile")
	external := filepath.Join(home, "outside")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "nested", "plugins")); err != nil {
		t.Fatal(err)
	}
	err := safeMkdirAll(root, filepath.Join(root, "nested", "plugins", "freeinference-companion"))
	if err == nil || !strings.Contains(err.Error(), ErrPathEscapesRoot.Error()) {
		t.Fatalf("nested symlink accepted: %v", err)
	}
	entries, readErr := os.ReadDir(external)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("external tree changed: %v", entries)
	}
}

func TestOpenRootRefusesSymlinkedChildCreation(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "profile")
	external := filepath.Join(home, "outside")
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "plugins", "escape")); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.MkdirAll(filepath.Join("plugins", "escape", "child"), 0755); err == nil {
		t.Fatal("os.Root followed a symlink outside its tree")
	}
}
