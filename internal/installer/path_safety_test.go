package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
)

func TestReconcileRefusesSymlinkedPluginParentWithoutExternalWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, "claude-profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`)
	external := filepath.Join(home, "outside")
	if err := os.MkdirAll(external, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "plugins")); err != nil {
		t.Fatal(err)
	}
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1")},
		version:       "v0.2.0",
		discovery:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Warning, "escapes trusted configuration root") {
		t.Fatalf("symlinked parent accepted: %+v", results)
	}
	entries, err := os.ReadDir(external)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("external destination modified: %v", entries)
	}
	if _, err := os.Lstat(filepath.Join(root, "plugins", "freeinference-companion")); !os.IsNotExist(err) {
		t.Fatalf("plugin target created through symlink: %v", err)
	}
}
