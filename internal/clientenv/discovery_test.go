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

func TestDiscoverIncludesCanonicalAndExportedFIRoots(t *testing.T) {
	home := t.TempDir()
	alternateClaude := filepath.Join(home, "claude-profile")
	alternateCodex := filepath.Join(home, "codex-profile")
	write(t, filepath.Join(alternateClaude, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`, 0600)
	write(t, filepath.Join(alternateCodex, "config.toml"), "model_provider = 'fi'\n\n[model_providers.fi]\nbase_url = 'https://freeinference.org/v1'\n", 0600)
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
	write(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`, 0600)
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
	write(t, filepath.Join(root, "nested", "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`, 0600)
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
	write(t, filepath.Join(root, "config.toml"), "model_provider = 'fi'\n\n[model_providers.fi]\nbase_url = 'https://freeinference.org/v1'\n", 0600)
	environments, warnings := DiscoverWithEnv(home, nil)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, ok := find(t, environments, ClientCodex, root); !ok {
		t.Fatalf("structural Codex home not discovered: %+v", environments)
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
	write(t, filepath.Join(realRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`, 0600)
	_, warnings := DiscoverWithEnv(home, []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(home, "linked-claude")})
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "symlink") {
		t.Fatalf("warnings = %v, want symlink rejection", warnings)
	}
}

func TestDiscoverWarnsForInvalidExplicitEnvironmentRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(root, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	environments, warnings := DiscoverWithEnv(home, []string{"CLAUDE_CONFIG_DIR=" + root})
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "not a directory") {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, ok := find(t, environments, ClientClaudeCode, root); ok {
		t.Fatalf("invalid explicit root was discovered: %+v", environments)
	}
}

func TestEnvironmentRootsRequireActualFreeInferenceConfig(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, "claude")
	codex := filepath.Join(home, "codex")
	write(t, filepath.Join(claude, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://api.openai.com/v1"}}`, 0600)
	write(t, filepath.Join(codex, "config.toml"), "model_provider = 'openai'\n\n[model_providers.openai]\nbase_url = 'https://api.openai.com/v1'\n", 0600)
	envs, warnings := DiscoverWithEnv(home, []string{"CLAUDE_CONFIG_DIR=" + claude, "CODEX_HOME=" + codex})
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	for _, env := range envs {
		if filepath.Clean(env.ConfigRoot) == filepath.Clean(claude) || filepath.Clean(env.ConfigRoot) == filepath.Clean(codex) {
			t.Fatalf("non-FI environment auto-discovered: %+v", envs)
		}
	}
}

func TestEnvironmentFIRoutesNormalizeTrailingSlash(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, "claude")
	codex := filepath.Join(home, "codex")
	write(t, filepath.Join(claude, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic///"}}`, 0600)
	write(t, filepath.Join(codex, "config.toml"), "model_provider = 'fi'\n\n[model_providers.fi]\nbase_url = 'https://freeinference.org/v1/'\n", 0600)
	envs, warnings := DiscoverWithEnv(home, []string{"CLAUDE_CONFIG_DIR=" + claude, "CODEX_HOME=" + codex})
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if _, ok := find(t, envs, ClientClaudeCode, claude); !ok {
		t.Fatalf("trailing-slash Claude route rejected: %+v", envs)
	}
	if _, ok := find(t, envs, ClientCodex, codex); !ok {
		t.Fatalf("single-quoted/trailing-slash Codex route rejected: %+v", envs)
	}
}

func TestDiscoverWarnsForLegacyClaudeRoute(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "legacy-claude")
	write(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`, 0600)
	environments, warnings := DiscoverWithEnv(home, []string{"CLAUDE_CONFIG_DIR=" + root})
	if _, ok := find(t, environments, ClientClaudeCode, root); ok {
		t.Fatalf("legacy Claude route was auto-integrated: %+v", environments)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "legacy FreeInference /v1") || !strings.Contains(warnings[0].Error(), "/anthropic") {
		t.Fatalf("legacy route warnings = %v", warnings)
	}
	if err := ValidateEnvironmentWithHome(ClientClaudeCode, root, home); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("legacy explicit validation error = %v", err)
	}
}

func TestClaudeRouteNormalizationRejectsNearMatches(t *testing.T) {
	for _, raw := range []string{
		"https://freeinference.org/anthropic?token=secret",
		"https://freeinference.org/anthropic#fragment",
		"https://freeinference.org/anthropic-extra",
		"https://freeinference.org/%61nthropic",
		"https://freeinference.org/ANTHROPIC",
		"https://freeinference.org:8443/anthropic",
		"https://user:pass@freeinference.org/anthropic",
	} {
		if SelectsFreeInferenceClaudeRoute(raw) {
			t.Errorf("near-match Claude route accepted: %q", raw)
		}
	}
}

func TestCodexConfigMalformedFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("model_provider = 'fi'\n[model_providers.fi\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if looksLikeCodexConfig(path) {
		t.Fatal("malformed TOML accepted")
	}
}
