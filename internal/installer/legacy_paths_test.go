package installer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeLegacyPathsDerivesHomeAndCanonicalValues(t *testing.T) {
	home := t.TempDir()
	partial := Paths{CodexHome: filepath.Join(home, ".codex")}
	normalized, err := partial.normalizeLegacyPaths()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := PathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Home != home || normalized.metadataPath != expected.MetadataPath() {
		t.Fatalf("normalized partial paths = %+v, metadata=%s want=%s", normalized, normalized.MetadataPath(), expected.MetadataPath())
	}
	if _, err := normalized.normalizeLegacyPaths(); err != nil {
		t.Fatalf("normalized paths are not stable: %v", err)
	}
}

func TestValidateMetadataPathAcceptsRecordedLegacyCodexHome(t *testing.T) {
	home := t.TempDir()
	legacyCodexHome := filepath.Join(home, "legacy-codex")
	paths, err := PathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	legacyPlugin := filepath.Join(legacyCodexHome, "plugins", "freeinference-companion")
	legacyMarketplace := filepath.Join(legacyCodexHome, "plugins", "freeinference-companion-marketplace")
	metadata := metadataForPaths(paths, "v0.1.0", "https://example.test", "hash", "v0.1.0")
	// The caller now supplies canonical Paths while the ownership record still
	// points at the old CODEX_HOME tree. This is the migration case rather than
	// a trivially self-consistent explicit Paths value.
	metadata.CodexPluginPath = legacyPlugin
	metadata.CodexMarketplacePath = legacyMarketplace
	if err := validateMetadataPaths(&metadata, paths); err != nil {
		t.Fatalf("recorded legacy paths rejected: %v", err)
	}
	otherHome := t.TempDir()
	other, err := PathsForHome(otherHome)
	if err != nil {
		t.Fatal(err)
	}
	otherMetadata := metadataForPaths(other, "v0.1.0", "https://example.test", "hash", "v0.1.0")
	if err := validateMetadataPaths(&otherMetadata, paths); err == nil {
		t.Fatal("metadata from a different home unexpectedly accepted")
	}
	if _, err := os.Lstat(legacyCodexHome); !os.IsNotExist(err) {
		t.Fatalf("validation mutated legacy root: %v", err)
	}
}

func TestValidateMetadataPathUsesRecordedLegacyCodexHomeByDefault(t *testing.T) {
	home := t.TempDir()
	canonical, err := PathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	legacy := canonical
	legacy.CodexHome = filepath.Join(home, "legacy-codex")
	legacy.CodexPluginPath = filepath.Join(legacy.CodexHome, "plugins", "freeinference-companion")
	legacy.CodexMarketplaceDir = filepath.Join(legacy.CodexHome, "plugins", "freeinference-companion-marketplace")
	metadata := metadataForPaths(legacy, "v0.1.0", "https://example.test", "hash", "v0.1.0")
	if err := validateMetadataPaths(&metadata, canonical); err != nil {
		t.Fatalf("default paths rejected recorded legacy install: %v", err)
	}
	resolved := pathsForRecordedCodex(canonical, &metadata)
	if resolved.CodexPluginPath != legacy.CodexPluginPath || resolved.CodexMarketplaceDir != legacy.CodexMarketplaceDir {
		t.Fatalf("legacy cleanup paths = %+v, want plugin=%s marketplace=%s", resolved, legacy.CodexPluginPath, legacy.CodexMarketplaceDir)
	}
}

func TestUninstallRemovesPreexistingLegacyCodexInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	canonical, err := PathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	legacyHome := filepath.Join(home, "legacy-codex")
	legacyPlugin := filepath.Join(legacyHome, "plugins", "freeinference-companion")
	legacyMarketplace := filepath.Join(legacyHome, "plugins", "freeinference-companion-marketplace")
	writeLegacy := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("legacy payload\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeLegacy(filepath.Join(legacyPlugin, ".codex-plugin", "plugin.json"))
	writeLegacy(filepath.Join(legacyMarketplace, ".agents", "plugins", "marketplace.json"))
	pluginDigest, err := pathDigest(legacyPlugin)
	if err != nil {
		t.Fatal(err)
	}
	marketplaceDigest, err := pathDigest(legacyMarketplace)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataForPaths(canonical, "v0.1.0", "https://example.test", strings.Repeat("a", 64), "v0.1.0")
	metadata.ManagedBinaryOwned = false
	metadata.ShimOwned = false
	metadata.ClaudePluginOwned = false
	metadata.CodexPluginPath = legacyPlugin
	metadata.CodexPluginOwned = true
	metadata.CodexPluginSHA256 = pluginDigest
	metadata.CodexPluginVersion = "v0.1.0"
	metadata.CodexMarketplacePath = legacyMarketplace
	metadata.CodexMarketplaceOwned = true
	metadata.CodexMarketplaceSHA256 = marketplaceDigest
	metadata.CodexMarketplaceVersion = "v0.1.0"
	if err := SaveInstallationMetadata(canonical.MetadataPath(), metadata); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(canonical, io.Discard, io.Discard); err != nil {
		t.Fatalf("legacy uninstall: %v", err)
	}
	if _, err := os.Lstat(legacyPlugin); !os.IsNotExist(err) {
		t.Fatalf("legacy Codex plugin survived uninstall: %v", err)
	}
	if _, err := os.Lstat(legacyMarketplace); !os.IsNotExist(err) {
		t.Fatalf("legacy Codex marketplace survived uninstall: %v", err)
	}
}
