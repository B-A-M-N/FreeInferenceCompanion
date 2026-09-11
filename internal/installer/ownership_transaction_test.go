package installer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"io"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
)

func enableMetadataSaveFailure(t *testing.T) {
	t.Helper()
	saveMetadataFailureHook = func() error { return errors.New("injected metadata save failure") }
	t.Cleanup(func() { saveMetadataFailureHook = nil })
}

func TestMetadataWriteFailureRollsBackNewInstalledEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, "claude-profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`)
	// The environment root is explicit, not discovered, so this proves the
	// metadata commit boundary rather than relying on discovery selection.
	enableMetadataSaveFailure(t)
	_, err := ReconcileClientEnvironments(reconcileOptions{
		home:          home,
		pluginSources: map[clientenv.Client]string{clientenv.ClientClaudeCode: pluginFixture(t, "claude", "v1")},
		version:       "v0.2.0",
		discovery:     true,
		explicit: []clientenv.Environment{{
			Client:     clientenv.ClientClaudeCode,
			ConfigRoot: root,
			Source:     clientenv.SourceExplicit,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "injected metadata save failure") {
		t.Fatalf("metadata failure not returned: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "plugins", "freeinference-companion")); !os.IsNotExist(statErr) {
		t.Fatalf("installed copy was left unowned: %v", statErr)
	}
	if _, statErr := os.Lstat(clientEnvironmentMetadataPath(home)); !os.IsNotExist(statErr) {
		t.Fatalf("partial ownership metadata remained: %v", statErr)
	}
}

func TestOwnershipWriterEnforcesExactSizeBoundary(t *testing.T) {
	base := ClientEnvironmentMetadata{SchemaVersion: 1}
	record := ClientIntegration{
		Client:          string(clientenv.ClientClaudeCode),
		ConfigRoot:      "/tmp/root",
		PluginPath:      "/tmp/root/plugins/freeinference-companion",
		PluginSHA256:    strings.Repeat("a", 64),
		Version:         "v0.2.0",
		DiscoverySource: "explicit",
		InstalledAt:     time.Now().UTC(),
	}
	base.Integrations = []ClientIntegration{record}
	valid := &ClientEnvironmentMetadata{SchemaVersion: 1, Integrations: []ClientIntegration{record}}
	data, err := json.MarshalIndent(valid, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxClientEnvironmentMetadata {
		t.Skipf("baseline ownership document exceeds configured limit: %d", len(data))
	}
	over := strings.Repeat(" ", maxClientEnvironmentMetadata-len(data)+1)
	var bloomed strings.Builder
	bloomed.Write(data)
	bloomed.WriteString(over)
	if err := validateSerializedMetadataSize([]byte(bloomed.String())); err == nil {
		t.Fatal("over-limit document accepted")
	}
	if err := validateSerializedMetadataSize(append(data, ' ')); err != nil {
		t.Fatalf("near-boundary document rejected: %v", err)
	}
	_ = base
}

func TestAddClientIntegrationUsesInstallerLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	paths, err := PathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataForPaths(paths, "v0.2.0", "https://example.test", strings.Repeat("a", 64), "v0.2.0")
	metadata.ClaudePluginVersion = "v0.2.0"
	if err := SaveInstallationMetadata(paths.MetadataPath(), metadata); err != nil {
		t.Fatal(err)
	}
	lock := state.NewFileLock(paths.lockPath())
	if err := os.MkdirAll(filepath.Dir(paths.lockPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := lock.Acquire(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	root := filepath.Join(home, "fi-profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`)
	if _, err := AddClientIntegration(home, clientenv.ClientClaudeCode, root, io.Discard); err == nil || !strings.Contains(err.Error(), "operation is in progress") {
		t.Fatalf("add did not honor installer lock: %v", err)
	}
}

func TestCoreUpgradeMetadataFailureRestoresByteIdenticalPluginAndDigest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	oldManifest, _, oldServer := testServer(t, "v0.2.0", "linux-amd64")
	defer oldServer.Close()
	if _, err := Install(Options{ManifestURL: oldManifest, Platform: "linux-amd64"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	oldMetadata, found, err := LoadInstallationMetadata(paths.MetadataPath())
	if err != nil || !found {
		t.Fatalf("read initial metadata: found=%v err=%v", found, err)
	}
	oldPlugin, err := os.ReadFile(filepath.Join(paths.claudePluginPath(), ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Inject at the ownership commit boundary, after filesystem replacements
	// have entered the transaction but before metadata can become durable.
	transactionFailureHook = func(target string) error {
		if target == paths.MetadataPath() {
			return errors.New("injected ownership metadata failure")
		}
		return nil
	}
	t.Cleanup(func() { transactionFailureHook = nil })
	newManifest, _, newServer := testServer(t, "v0.3.0", "linux-amd64")
	defer newServer.Close()
	if _, err := Update(Options{ManifestURL: newManifest, Platform: "linux-amd64"}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "injected ownership metadata failure") {
		t.Fatalf("metadata commit failure not returned: %v", err)
	}
	gotPlugin, err := os.ReadFile(filepath.Join(paths.claudePluginPath(), ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotPlugin) != string(oldPlugin) {
		t.Fatal("metadata failure left an upgraded plugin")
	}
	currentMetadata, found, err := LoadInstallationMetadata(paths.MetadataPath())
	if err != nil || !found {
		t.Fatalf("read restored metadata: found=%v err=%v", found, err)
	}
	if currentMetadata.ClaudePluginVersion != oldMetadata.ClaudePluginVersion {
		t.Fatalf("ownership version changed: got=%s want=%s", currentMetadata.ClaudePluginVersion, oldMetadata.ClaudePluginVersion)
	}
	matched, err := pathDigestMatches(paths.claudePluginPath(), currentMetadata.ClaudePluginSHA256)
	if err != nil || !matched {
		t.Fatalf("restored plugin does not match ownership digest: matched=%v err=%v", matched, err)
	}
}

func TestCoreMetadataFailureRollsBackNativeCodexRegistration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "codex-native.log")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	paths, err := PathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	transactionFailureHook = func(target string) error {
		if target == paths.MetadataPath() {
			return errors.New("injected native metadata commit failure")
		}
		return nil
	}
	t.Cleanup(func() { transactionFailureHook = nil })
	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()
	if _, err := Install(Options{ManifestURL: manifestURL, Platform: "linux-amd64"}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "injected native metadata commit failure") {
		t.Fatalf("metadata failure not returned: %v", err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "plugin marketplace add") || !strings.Contains(string(log), "plugin marketplace remove") || !strings.Contains(string(log), "plugin remove") {
		t.Fatalf("native registration was not rolled back: %s", log)
	}
	if _, err := os.Lstat(paths.CodexMarketplaceDir); !os.IsNotExist(err) {
		t.Fatalf("Codex marketplace survived transaction rollback: %v", err)
	}
}

func TestClientEnvironmentUpgradeMetadataFailureRestoresPriorPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "profile"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, "profile")
	writeIntegrationFixture(t, filepath.Join(root, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/anthropic"}}`)
	if _, err := ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude-v1", "v1"),
		},
		version:   "v0.1.0",
		discovery: true,
	}); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(root, "plugins", "freeinference-companion", "scripts", "run-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	enableMetadataSaveFailure(t)
	_, err = ReconcileClientEnvironments(reconcileOptions{
		home: home,
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: pluginFixture(t, "claude-v2", "v2"),
		},
		version:   "v0.2.0",
		discovery: true,
	})
	if err == nil || !strings.Contains(err.Error(), "injected metadata save failure") {
		t.Fatalf("metadata failure not returned: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "plugins", "freeinference-companion", "scripts", "run-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatalf("prior integration changed after metadata failure: got=%q want=%q", got, old)
	}
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if record := integrationByID(t, metadata, "claude-code", root); record == nil || record.Version != "v0.1.0" {
		t.Fatalf("prior ownership record changed: %+v", metadata.Integrations)
	}
}

func TestTransactionRejectsSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(outside, "staged")
	if err := os.WriteFile(staged, []byte("staged"), 0600); err != nil {
		t.Fatal(err)
	}
	tx := &installTransaction{}
	if err := tx.replace(filepath.Join(linked, "nested", "target"), filepath.Join(linked, "nested", "staged")); err == nil {
		t.Fatal("transaction followed symlinked ancestor")
	}
}
