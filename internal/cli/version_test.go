package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

// TestVersionConsistency verifies that the CLI version, adapter plugin
// version, and both plugin manifest versions all agree. This is the
// single-source-of-truth guard.
func TestVersionConsistency(t *testing.T) {
	if Version == "" {
		t.Fatal("cli.Version is empty")
	}

	// All version sources must agree.
	want := version.Version
	if Version != want {
		t.Errorf("cli.Version = %q, want %q (version.Version)", Version, want)
	}
	if adapters.PluginVersion != want {
		t.Errorf("adapters.PluginVersion = %q, want %q", adapters.PluginVersion, want)
	}

	// Plugin manifests must match.
	manifestPaths := []string{
		"../../plugin.json",
		"../../plugins/claude-code/.claude-plugin/plugin.json",
		"../../plugins/codex/.codex-plugin/plugin.json",
	}
	for _, p := range manifestPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", err, p)
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("parse %s: %v", err, p)
		}
		if manifest.Version != want {
			t.Errorf("%s version = %q, want %q", filepath.Base(filepath.Dir(p)), manifest.Version, want)
		}
	}
}
