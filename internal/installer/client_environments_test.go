package installer

import (
	"io"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIntegrationFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func pluginFixture(t *testing.T, name, marker string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	writeIntegrationFixture(t, filepath.Join(root, ".client-plugin", "plugin.json"), `{"name":"freeinference-companion"}`)
	writeIntegrationFixture(t, filepath.Join(root, "scripts", "run-hook.sh"), "#!/bin/sh\n"+marker+"\n")
	return root
}

func integrationByID(t *testing.T, metadata *ClientEnvironmentMetadata, client, root string) *ClientIntegration {
	t.Helper()
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := range metadata.Integrations {
		canonical, err := filepath.Abs(metadata.Integrations[i].ConfigRoot)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Integrations[i].Client == client && filepath.Clean(canonical) == filepath.Clean(absolute) {
			return &metadata.Integrations[i]
		}
	}
	return nil
}

func TestReconcileInstallsFreeInferenceProfilesAndRecordsOwnership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	claudeRoot := filepath.Join(home, ".config", "claude-profiles", "fi-profile-a")
	codexRoot := filepath.Join(home, ".custom-codex")
	writeIntegrationFixture(t, filepath.Join(claudeRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	writeIntegrationFixture(t, filepath.Join(codexRoot, "config.toml"), "model_provider = \"fi\"\n\n[model_providers.fi]\nbase_url = \"https://freeinference.org/v1\"\n")
	claudeSource := pluginFixture(t, "claude", "claude-v1")
	codexSource := pluginFixture(t, "codex", "codex-v1")

	results, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: claudeSource,
			clientenv.ClientCodex:      codexSource,
		},
		version:   "v0.2.0",
		discovery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, path := range []string{
		filepath.Join(claudeRoot, "plugins", "freeinference-companion", "scripts", "run-hook.sh"),
		filepath.Join(codexRoot, "plugins", "freeinference-companion", "scripts", "run-hook.sh"),
		filepath.Join(codexRoot, "plugins", "freeinference-companion-marketplace", ".agents", "plugins", "marketplace.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("integration target missing: %s: %v", path, err)
		}
	}
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if integrationByID(t, metadata, "claude-code", claudeRoot) == nil || integrationByID(t, metadata, "codex", codexRoot) == nil {
		t.Fatalf("ownership records missing: %+v", metadata)
	}
}

func TestReconcileUpdatesPreviouslyRecordedProfileEvenWhenNoLongerDiscovered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "old-profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// First integration records ownership.
	claudeRoot := filepath.Join(home, "old-profile")
	writeIntegrationFixture(t, filepath.Join(claudeRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	source := pluginFixture(t, "claude", "v1")
	if _, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: source},
		version:       "v0.1.0",
		discovery:     true,
	}); err != nil {
		t.Fatal(err)
	}
	// Configuration no longer identifies the profile, but Companion still owns
	// its plugin and must upgrade it rather than strand the old copy.
	if err := os.Remove(filepath.Join(claudeRoot, "settings.json")); err != nil {
		t.Fatal(err)
	}
	newSource := pluginFixture(t, "claude-new", "v2")
	if _, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: newSource},
		version:       "v0.2.0",
		discovery:     true,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(claudeRoot, "plugins", "freeinference-companion", "scripts", "run-hook.sh"))
	if err != nil || !strings.Contains(string(data), "v2") {
		t.Fatalf("owned profile was not upgraded: %q, %v", data, err)
	}
}

func TestReconcileRefusesUnownedExistingCompanionDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	claudeRoot := filepath.Join(home, "claude-profile")
	writeIntegrationFixture(t, filepath.Join(claudeRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	foreign := filepath.Join(claudeRoot, "plugins", "freeinference-companion")
	writeIntegrationFixture(t, filepath.Join(foreign, "keep.txt"), "foreign")
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1")},
		version:       "v0.2.0",
		discovery:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Warning == "" || !strings.Contains(results[0].Warning, "unowned") {
		t.Fatalf("results = %+v", results)
	}
	data, err := os.ReadFile(filepath.Join(foreign, "keep.txt"))
	if err != nil || string(data) != "foreign" {
		t.Fatalf("foreign plugin changed: %q, %v", data, err)
	}
}

func TestReconcileDoesNotOverwriteModifiedOwnedProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	claudeRoot := filepath.Join(home, "claude-profile")
	writeIntegrationFixture(t, filepath.Join(claudeRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	if _, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1")},
		version:       "v0.1.0",
		discovery:     true,
	}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(claudeRoot, "plugins", "freeinference-companion", "scripts", "run-hook.sh")
	writeIntegrationFixture(t, target, "#!/bin/sh\nuser-edited\n")
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude-new", "v2")},
		version:       "v0.2.0",
		discovery:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Warning, "changed after installation") {
		t.Fatalf("modified owned plugin was replaced: %+v", results)
	}
	if data, _ := os.ReadFile(target); !strings.Contains(string(data), "user-edited") {
		t.Fatal("user modification was lost")
	}
	// A drifted owned copy must remain owned so an explicit repair decision can
	// handle it later; reconciliation must not silently forget it.
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if integrationByID(t, metadata, "claude-code", claudeRoot) == nil {
		t.Fatal("modified owned plugin was forgotten")
	}
}

func TestReconcileOnlyDiscoversFreeInferenceEnvironments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	fiRoot := filepath.Join(home, ".config", "engines", "fi")
	otherRoot := filepath.Join(home, ".config", "engines", "other")
	writeIntegrationFixture(t, filepath.Join(fiRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	writeIntegrationFixture(t, filepath.Join(otherRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://api.openai.com/v1"}}`)
	nonFICodex := filepath.Join(home, ".another-codex")
	writeIntegrationFixture(t, filepath.Join(nonFICodex, "config.toml"), "model_provider = \"openai\"\n\n[model_providers.openai]\nbase_url = \"https://api.openai.com/v1\"\n")
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1"), clientenv.ClientCodex: pluginFixture(t, "codex", "v1")},
		version:       "v0.2.0",
		discovery:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ConfigRoot != fiRoot {
		t.Fatalf("non-FreeInference environments were integrated: %+v", results)
	}
}

func TestReconcileNoDiscoveryKeepsCanonicalOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	claudeRoot := filepath.Join(home, "claude-profile")
	writeIntegrationFixture(t, filepath.Join(claudeRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1")},
		version:       "v0.2.0",
		discovery:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("no-discovery mode integrated extra environments: %+v", results)
	}
	if _, err := os.Lstat(filepath.Join(claudeRoot, "plugins")); !os.IsNotExist(err) {
		t.Fatalf("no-discovery mode mutated profile: %v", err)
	}
}

var _ io.Writer = io.Discard

func TestInstallReconcilesNewProfilesAtSameVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()

	first, err := Install(Options{ManifestURL: manifestURL, Platform: "linux-amd64"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("initial install: %v", err)
	}
	if len(first.EnvironmentIntegrations) != 0 {
		t.Fatalf("initial install unexpectedly integrated profiles: %+v", first.EnvironmentIntegrations)
	}

	claudeRoot := filepath.Join(home, ".config", "claude-profiles", "fi-profile-a")
	codexRoot := filepath.Join(home, ".custom-codex")
	writeIntegrationFixture(t, filepath.Join(claudeRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	writeIntegrationFixture(t, filepath.Join(codexRoot, "config.toml"), "model_provider = \"fi\"\n\n[model_providers.fi]\nbase_url = \"https://freeinference.org/v1\"\n")

	second, err := Install(Options{ManifestURL: manifestURL, Platform: "linux-amd64"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("same-version install: %v", err)
	}
	if !second.AlreadyLatest || !second.IntegrationsChanged {
		t.Fatalf("same-version result = %+v", second)
	}
	if len(second.EnvironmentIntegrations) != 2 {
		t.Fatalf("same-version integrations = %+v warnings=%+v", second.EnvironmentIntegrations, second.Warnings)
	}
	claudePlugin := filepath.Join(claudeRoot, "plugins", "freeinference-companion", ".claude-plugin", "plugin.json")
	if _, err := os.Stat(claudePlugin); err != nil {
		t.Errorf("profile plugin missing after same-version install: %s: %v", claudePlugin, err)
	}
	codexPlugin := filepath.Join(codexRoot, "plugins", "freeinference-companion", ".codex-plugin", "plugin.json")
	if _, err := os.Stat(codexPlugin); err != nil {
		t.Errorf("Codex profile plugin missing after same-version install: %s: %v", codexPlugin, err)
	}
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if integrationByID(t, metadata, "claude-code", claudeRoot) == nil || integrationByID(t, metadata, "codex", codexRoot) == nil {
		t.Fatalf("same-version ownership records missing: %+v", metadata.Integrations)
	}
}

func TestUninstallClientEnvironmentsRemovesOnlyRecordedOwnedPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	claudeRoot := filepath.Join(home, "claude-profile")
	writeIntegrationFixture(t, filepath.Join(claudeRoot, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	if _, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1")},
		version:       "v0.2.0",
		discovery:     true,
	}); err != nil {
		t.Fatal(err)
	}
	pluginRoot := filepath.Join(claudeRoot, "plugins", "freeinference-companion")
	foreign := filepath.Join(claudeRoot, "plugins", "foreign-plugin")
	writeIntegrationFixture(t, filepath.Join(foreign, "keep.txt"), "foreign")
	removed, warnings := UninstallClientEnvironments(home, io.Discard)
	if len(warnings) != 0 || len(removed) != 1 || removed[0] != pluginRoot {
		t.Fatalf("uninstall result removed=%v warnings=%v", removed, warnings)
	}
	if _, err := os.Lstat(pluginRoot); !os.IsNotExist(err) {
		t.Fatalf("owned profile plugin survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "keep.txt")); err != nil {
		t.Fatalf("foreign plugin removed: %v", err)
	}
	if _, err := os.Lstat(clientEnvironmentMetadataPath(home)); !os.IsNotExist(err) {
		t.Fatalf("environment metadata survived: %v", err)
	}
}

func TestCodexProfileRegistrationUsesTargetCODEXHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "codex-env.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$CODEX_HOME $*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	codexRoot := filepath.Join(home, ".custom-codex")
	writeIntegrationFixture(t, filepath.Join(codexRoot, "config.toml"), "model_provider = \"fi\"\n\n[model_providers.fi]\nbase_url = \"https://freeinference.org/v1\"\n")
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1"),
			clientenv.ClientCodex:      pluginFixture(t, "codex", "v1"),
		},
		version:   "v0.2.0",
		discovery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, result := range results {
		if filepath.Clean(result.ConfigRoot) == filepath.Clean(codexRoot) && result.Warning == "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom Codex profile was not reconciled: %+v", results)
	}
	t.Setenv("CODEX_HOME", filepath.Join(home, "host-codex"))
	// Exercise the canonical wrapper too. It must target canonical ~/.codex,
	// while alternate reconciliation must target ~/.custom-codex.
	canonicalPaths := Paths{CodexHome: clientenv.CanonicalRoot(home, clientenv.ClientCodex), CodexMarketplaceDir: filepath.Join(clientenv.CanonicalRoot(home, clientenv.ClientCodex), "plugins", "freeinference-companion-marketplace")}
	registerCodexMarketplaceStatus(canonicalPaths, io.Discard)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 {
		t.Fatalf("Codex subprocess invocations = %q", string(data))
	}
	targetCount := 0
	canonicalRoot := clientenv.CanonicalRoot(home, clientenv.ClientCodex)
	canonicalCount := 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, codexRoot+" "):
			targetCount++
		case strings.HasPrefix(line, canonicalRoot+" "):
			canonicalCount++
		}
	}
	if targetCount != 2 || canonicalCount != 2 {
		t.Fatalf("target/canonical CODEX_HOME invocations = %q", string(data))
	}
	if strings.Contains(string(data), filepath.Join(home, "host-codex")) {
		t.Fatalf("host CODEX_HOME leaked into registration: %q", string(data))
	}
}

func TestAddClientIntegrationRegistersExplicitArbitraryRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.CoreClaudePluginPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(paths.CoreClaudePluginPath, pluginFixture(t, "core-claude", "v1")); err != nil {
		t.Fatal(err)
	}
	metadata := metadataForPaths(paths, "v0.2.0", "https://example.test", strings.Repeat("a", 64), "v0.2.0")
	metadata.ClaudePluginSHA256, _ = pathDigest(paths.ClaudePluginPath)
	if err := SaveInstallationMetadata(paths.MetadataPath(), metadata); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "weird-explicit-profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	integration, err := AddClientIntegration(home, clientenv.ClientClaudeCode, root, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if integration.Client != string(clientenv.ClientClaudeCode) || filepath.Clean(integration.ConfigRoot) != filepath.Clean(root) || integration.Version != "v0.2.0" {
		t.Fatalf("integration summary = %+v", integration)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "freeinference-companion", "scripts", "run-hook.sh")); err != nil {
		t.Fatalf("explicit plugin not installed: %v", err)
	}
}

func TestAddClientIntegrationRejectsNonFreeInferenceRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "ordinary-profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://api.openai.com/v1"}}`)
	if _, err := AddClientIntegration(home, clientenv.ClientClaudeCode, root, io.Discard); err == nil {
		t.Fatal("non-FreeInference explicit root was accepted")
	}
	if _, err := os.Lstat(filepath.Join(root, "plugins")); !os.IsNotExist(err) {
		t.Fatalf("rejected explicit root was mutated: %v", err)
	}
}

func TestRemoveClientIntegrationRemovesOnlySelectedEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	keepRoot := filepath.Join(home, ".config", "claude-code", "keep")
	removeRoot := filepath.Join(home, ".config", "claude-code", "remove")
	for _, root := range []string{keepRoot, removeRoot} {
		writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	}
	if _, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1"),
		},
		version:   "v0.2.0",
		discovery: true,
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveClientIntegration(home, clientenv.ClientClaudeCode, removeRoot, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || filepath.Clean(removed[0]) != filepath.Clean(filepath.Join(removeRoot, "plugins", "freeinference-companion")) {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Lstat(filepath.Join(removeRoot, "plugins", "freeinference-companion")); !os.IsNotExist(err) {
		t.Fatalf("selected plugin survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(keepRoot, "plugins", "freeinference-companion", "scripts", "run-hook.sh")); err != nil {
		t.Fatalf("unselected integration removed: %v", err)
	}
	remaining, err := ListClientIntegrations(home)
	if err != nil || len(remaining) != 1 || filepath.Clean(remaining[0].ConfigRoot) != filepath.Clean(keepRoot) {
		t.Fatalf("remaining integrations = %+v, err=%v", remaining, err)
	}
	if _, err := RemoveClientIntegration(home, clientenv.ClientClaudeCode, removeRoot, io.Discard); err == nil {
		t.Fatal("missing integration remove unexpectedly succeeded")
	}
}

func TestReconcileMissingRecordedEnvironmentIsMarkedMissingAndMetadataPruned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "temporary-profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, "temporary-profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	if _, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1"),
		},
		version:   "v0.1.0",
		discovery: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Remove the whole profile before the next reconciliation. This proves the
	// stale record is pruned instead of being misreported as installed.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	// The environment is neither exported nor discoverable now.
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v2"),
		},
		version:   "v0.2.0",
		discovery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("missing recorded environment produced results: %+v", results)
	}
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Integrations) != 0 {
		t.Fatalf("missing recorded environment was not pruned: %+v", metadata.Integrations)
	}
}

func TestReconcileSymlinkedTargetPluginPathIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, "claude-profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	realPlugin := filepath.Join(home, "real-plugin")
	if err := os.MkdirAll(realPlugin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPlugin, filepath.Join(root, "plugins", "freeinference-companion")); err != nil {
		t.Fatal(err)
	}
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1"),
		},
		version:   "v0.2.0",
		discovery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Warning, "unsafe target path") {
		t.Fatalf("symlinked plugin target accepted: %+v", results)
	}
	info, err := os.Lstat(filepath.Join(root, "plugins", "freeinference-companion"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink target changed: %v", err)
	}
}

func TestReconcileOneProfileFailureKeepsOtherProfileSuccessful(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "broken-profile"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".good-codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	broken := filepath.Join(home, "broken-profile")
	writeIntegrationFixture(t, filepath.Join(broken, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`)
	writeIntegrationFixture(t, filepath.Join(broken, "plugins", "freeinference-companion", "foreign.txt"), "foreign")
	good := filepath.Join(home, ".good-codex")
	writeIntegrationFixture(t, filepath.Join(good, "config.toml"), "model_provider = \"fi\"\n\n[model_providers.fi]\nbase_url = \"https://freeinference.org/v1\"\n")
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1"),
			clientenv.ClientCodex:      pluginFixture(t, "codex", "v1"),
		},
		version:   "v0.2.0",
		discovery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var warned, installed bool
	for _, result := range results {
		switch filepath.Clean(result.ConfigRoot) {
		case filepath.Clean(broken):
			if result.Warning != "" {
				warned = true
			}
		case filepath.Clean(good):
			if result.Action == "installed" {
				installed = true
			}
		}
	}
	if !warned || !installed {
		t.Fatalf("independent profile handling failed: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(good, "plugins", "freeinference-companion", "scripts", "run-hook.sh")); err != nil {
		t.Fatalf("healthy profile was not integrated: %v", err)
	}
}
