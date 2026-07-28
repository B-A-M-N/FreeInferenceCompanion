package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestVersionConsistency verifies that the CLI version constant matches the
// adapter plugin version and both plugin manifest versions. This is the
// single-source-of-truth guard for Fix-28.
func TestVersionConsistency(t *testing.T) {
	if Version == "" {
		t.Fatal("cli.Version is empty")
	}

	// Plugin manifests must match cli.Version.
	manifestPaths := []string{
		"../../plugins/claude-code/.claude-plugin/plugin.json",
		"../../plugins/codex/.codex-plugin/plugin.json",
	}
	for _, p := range manifestPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if manifest.Version != Version {
			t.Errorf("%s version = %q, want %q (cli.Version)", filepath.Base(filepath.Dir(p)), manifest.Version, Version)
		}
	}
}
