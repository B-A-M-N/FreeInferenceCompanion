package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

func TestDefaultManifestURLUsesCanonicalRepository(t *testing.T) {
	wantPrefix := version.RepositoryURL + "/releases/"
	if !strings.HasPrefix(defaultManifestURL, wantPrefix) {
		t.Fatalf("defaultManifestURL = %q, want prefix %q", defaultManifestURL, wantPrefix)
	}
	if !strings.HasSuffix(defaultManifestURL, "/latest/download/marketplace.json") {
		t.Fatalf("defaultManifestURL = %q, want latest marketplace manifest", defaultManifestURL)
	}
}

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

	// Plugin manifests must match. The repo-root plugin.json is optional
	// (marketplace metadata); the per-plugin manifests are authoritative.
	manifestPaths := []string{
		"../../plugins/claude-code/.claude-plugin/plugin.json",
		"../../plugins/freeinference-companion/.codex-plugin/plugin.json",
	}
	if _, err := os.Stat("../../plugin.json"); err == nil {
		manifestPaths = append([]string{"../../plugin.json"}, manifestPaths...)
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
