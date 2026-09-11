package clientenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func find(t *testing.T, environments []Environment, client Client, root string) (Environment, bool) {
	t.Helper()
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range environments {
		clean, err := filepath.Abs(environment.ConfigRoot)
		if err != nil {
			t.Fatal(err)
		}
		if environment.Client == client && filepath.Clean(clean) == filepath.Clean(absolute) {
			return environment, true
		}
	}
	return Environment{}, false
}

func TestDiscoverIncludesCanonicalAndExportedEnvironments(t *testing.T) {
	home := t.TempDir()
	alternateClaude := filepath.Join(home, "claude-profile")
	alternateCodex := filepath.Join(home, "codex-profile")
	environments, warnings := DiscoverWithEnv(home, []string{
		"CLAUDE_CONFIG_DIR=" + alternateClaude,
		"CODEX_HOME=" + alternateCodex,
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	for _, expected := range []struct {
		client Client
		root   string
		source DiscoverySource
	}{
		{ClientClaudeCode, filepath.Join(home, ".claude"), SourceCanonical},
		{ClientClaudeCode, alternateClaude, SourceEnvironment},
		{ClientCodex, filepath.Join(home, ".codex"), SourceCanonical},
		{ClientCodex, alternateCodex, SourceEnvironment},
	} {
		got, ok := find(t, environments, expected.client, expected.root)
		if !ok {
			t.Fatalf("%s environment %s missing: %+v", expected.client, expected.root, environments)
		}
		if got.Source != expected.source {
			t.Fatalf("source for %s = %s, want %s", expected.root, got.Source, expected.source)
		}
	}
}

func TestDiscoverFindsStructuralClaudeXDGEnvironment(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "client-engines", "fi-profile-a")
	write(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`, 0600)
	environments, warnings := DiscoverWithEnv(home, nil)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, ok := find(t, environments, ClientClaudeCode, root); !ok {
		t.Fatalf("structural Claude environment not discovered: %+v", environments)
	}
}

func TestDiscoverIgnoresUnrelatedClaudeSettings(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "project", "editor")
	write(t, filepath.Join(root, "settings.json"), `{"editor.formatOnSave":true}`, 0600)
	write(t, filepath.Join(root, "nested", "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`, 0600)
	environments, warnings := DiscoverWithEnv(home, nil)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, ok := find(t, environments, ClientClaudeCode, root); ok {
		t.Fatal("unrelated settings object was accepted")
	}
	if _, ok := find(t, environments, ClientClaudeCode, filepath.Join(root, "nested")); ok {
		t.Fatal("discovery exceeded the configured depth")
	}
}

func TestDiscoverFindsHiddenStructuralCodexHome(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".custom-codex")
	config := `model_provider = "openai"\n\n[model_providers.openai]\nbase_url = "https://freeinference.org/v1"\n`
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(strings.ReplaceAll(config, `\n`, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	// Multiple model profiles remain one environment identity.
	write(t, filepath.Join(root, "glm.config.toml"), `model_provider = "glm"\n`, 0600)
	write(t, filepath.Join(root, "fi.config.toml"), `model_provider = "freeinference"\n`, 0600)
	environments, warnings := DiscoverWithEnv(home, nil)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, ok := find(t, environments, ClientCodex, root); !ok {
		t.Fatalf("structural Codex home not discovered: %+v", environments)
	}
	count := 0
	for _, environment := range environments {
		if environment.Client == ClientCodex && strings.HasSuffix(environment.ConfigRoot, ".custom-codex") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Codex profile files created %d environment identities, want 1", count)
	}
}

func TestDiscoverRejectsSymlinkedConfigurationRoot(t *testing.T) {
	home := t.TempDir()
	realRoot := filepath.Join(home, "real-claude")
	if err := os.MkdirAll(realRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, filepath.Join(home, "linked-claude")); err != nil {
		t.Fatal(err)
	}
	_, warnings := DiscoverWithEnv(home, []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(home, "linked-claude")})
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "symlink") {
		t.Fatalf("warnings = %v, want symlink rejection", warnings)
	}
}
