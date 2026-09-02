package installer

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testServer returns an HTTP server that serves a mock marketplace manifest
// and a mock platform ZIP file. The caller should call server.Close() when done.
func testServer(t *testing.T, version string, platform PlatformKey) (manifestURL string, zipHash string, server *httptest.Server) {
	t.Helper()
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")

	// Create a ZIP file with a mock binary and plugin directories.
	zipData, zipHash := createTestZIP(t, version)

	// Create the handler first (we don't have the server URL yet, so use a placeholder).
	zipDataRef := zipData
	zipHashRef := zipHash

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	// Create the server.
	server = httptest.NewServer(handler)

	// Build the real manifest body now that we have the URL.
	manifestBody := createManifestBody(t, version, server.URL+"/", zipHashRef)
	zipDataRef = zipData

	// Replace handler with the real one.
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/marketplace.json" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(manifestBody)
			return
		}
		if strings.HasSuffix(r.URL.Path, "release.zip") {
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipDataRef)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Write(emptyZIP())
	})
	server.Config.Handler = handler

	return server.URL + "/marketplace.json", zipHashRef, server
}

func createManifestBody(t *testing.T, version, serverBase, zipHash string) []byte {
	t.Helper()
	manifest := map[string]any{
		"version": version,
		"platforms": map[string]map[string]string{
			"linux-amd64": {
				"url":    serverBase + "release.zip",
				"sha256": zipHash,
			},
		},
		"plugin_urls": map[string]string{
			"claude-code": serverBase + "plugin-claude.zip",
			"codex":       serverBase + "plugin-codex.zip",
		},
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// createTestZIP creates a ZIP file containing a mock binary and plugin directories.
func createTestZIP(t *testing.T, version string) ([]byte, string) {
	t.Helper()
	buf := &strings.Builder{}
	w := zip.NewWriter(buf)

	// Mock binary.
	f, _ := w.Create("bin/freeinference")
	f.Write([]byte("mock-binary-" + version))

	// Mock plugin directories.
	f, _ = w.Create("plugins/claude-code/.claude-plugin/plugin.json")
	f.Write([]byte(`{"name":"freeinference-companion"}`))
	f, _ = w.Create("plugins/claude-code/hooks/hooks.json")
	f.Write([]byte(`{"hooks":{}}`))
	f, _ = w.Create("plugins/claude-code/scripts/run-hook.sh")
	f.Write([]byte("#!/usr/bin/env bash\nexit 0\n"))
	f, _ = w.Create("plugins/claude-code/package.json")
	f.Write([]byte(`{"name":"freeinference-companion"}`))

	f, _ = w.Create("plugins/codex/.codex-plugin/plugin.json")
	f.Write([]byte(`{"name":"freeinference-companion"}`))
	f, _ = w.Create("plugins/codex/hooks/hooks.json")
	f.Write([]byte(`{"hooks":{}}`))
	f, _ = w.Create("plugins/codex/scripts/run-hook.sh")
	f.Write([]byte("#!/usr/bin/env bash\nexit 0\n"))

	w.Close()

	hash := sha256.Sum256([]byte(buf.String()))
	return []byte(buf.String()), hex.EncodeToString(hash[:])
}

// emptyZIP returns a minimal valid empty ZIP.
func emptyZIP() []byte {
	return []byte{
		0x50, 0x4b, 0x05, 0x06, // local file header sig
		0x00, 0x00, 0x00, 0x00, // version, flags, compression, modtime, moddate
		0x00, 0x00, 0x00, 0x00, // crc32, compressed size, uncompressed size
		0x00, 0x00, // filename length, extra field length
		0x00, 0x00, 0x00, 0x00, // central directory offset, size
	}
}

func TestInstallFresh(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}

	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}

	_, err = Install(Options{
		ManifestURL:     manifestURL,
		Platform:        "linux-amd64",
		ExistingVersion: "",
		DryRun:          false,
	}, stdout, stderr)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// Verify binary was installed.
	if _, err := os.Stat(paths.BinaryPath); err != nil {
		t.Logf("Binary path: %s", paths.BinaryPath)
		t.Logf("stderr: %s", stderr.String())
		t.Errorf("binary not installed at %s: %v", paths.BinaryPath, err)
	}

	// Verify plugins were extracted.
	for _, dir := range []string{paths.ClaudePluginDir, paths.CodexPluginDir} {
		pluginPath := filepath.Join(dir, "freeinference-companion")
		t.Logf("Checking plugin path: %s", pluginPath)
		if _, err := os.Stat(pluginPath); err != nil {
			t.Errorf("plugin not extracted to %s: %v", pluginPath, err)
		}
	}
	codexPlugin := filepath.Join(paths.CodexPluginDir, "freeinference-companion")
	for _, rel := range []string{".codex-plugin/plugin.json", "hooks/hooks.json", "scripts/run-hook.sh"} {
		if _, err := os.Stat(filepath.Join(codexPlugin, rel)); err != nil {
			t.Errorf("Codex plugin artifact %s not extracted: %v", rel, err)
		}
	}
	marketplace := filepath.Join(paths.CodexMarketplaceDir, ".agents", "plugins", "marketplace.json")
	if _, err := os.Stat(marketplace); err != nil {
		t.Errorf("Codex marketplace manifest not created: %v", err)
	}
	marketplacePlugin := filepath.Join(paths.CodexMarketplaceDir, "plugins", "freeinference-companion", ".codex-plugin", "plugin.json")
	if _, err := os.Stat(marketplacePlugin); err != nil {
		t.Errorf("Codex marketplace plugin not created: %v", err)
	}

	// Verify version output.
	if !strings.Contains(stdout.String(), "v0.2.0") {
		t.Errorf("expected version v0.2.0 in output:\n%s", stdout.String())
	}
}

func TestInstallChecksumMismatch(t *testing.T) {
	// Just test the VerifyChecksum function directly with wrong hash.
	data := []byte("test data")
	err := VerifyChecksum(data, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Error("expected checksum mismatch error")
	}
}

func TestInstallRefusesToOverwriteUnownedBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.BinaryPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BinaryPath, []byte("user-owned-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()
	if _, err := Install(Options{ManifestURL: manifestURL, Platform: "linux-amd64"}, io.Discard, io.Discard); err == nil {
		t.Fatal("installer must refuse to overwrite a binary without ownership metadata")
	}
	data, err := os.ReadFile(paths.BinaryPath)
	if err != nil || string(data) != "user-owned-binary" {
		t.Fatalf("unowned binary changed: %q, %v", data, err)
	}
}

func TestUninstallPreservesTargetsNotOwnedByPartialInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.BinaryPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BinaryPath, []byte("user-owned-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()
	if _, err := Install(Options{ManifestURL: manifestURL, Platform: "linux-amd64", NoBin: true}, io.Discard, io.Discard); err != nil {
		t.Fatalf("partial install: %v", err)
	}
	if err := Uninstall(paths, io.Discard, io.Discard); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	data, err := os.ReadFile(paths.BinaryPath)
	if err != nil || string(data) != "user-owned-binary" {
		t.Fatalf("unowned binary removed or changed: %q, %v", data, err)
	}
	if _, err := os.Stat(paths.ClaudePluginPath); !os.IsNotExist(err) {
		t.Fatalf("owned Claude plugin survived uninstall: %v", err)
	}
}

func TestRegisterCodexMarketplaceUsesNativePluginManager(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "codex-args.log")
	fakeCodex := filepath.Join(fakeBin, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(fakeCodex, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	pluginSrc := filepath.Join(home, "plugin-source")
	if err := os.MkdirAll(filepath.Join(pluginSrc, ".codex-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginSrc, ".codex-plugin", "plugin.json"), []byte(`{"name":"freeinference-companion"}`), 0600); err != nil {
		t.Fatal(err)
	}
	paths := Paths{CodexMarketplaceDir: filepath.Join(home, "marketplace")}
	if err := registerCodexMarketplace(paths, pluginSrc, io.Discard); err != nil {
		t.Fatalf("register: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "plugin marketplace add") || !strings.Contains(string(args), "plugin add freeinference-companion@freeinference-companion-local") {
		t.Fatalf("Codex native manager was not invoked as expected: %s", args)
	}
}

func TestUpdateSkipsWhenLatest(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.BinaryPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BinaryPath, []byte("existing"), 0755); err != nil {
		t.Fatal(err)
	}
	metadata := metadataForPaths(paths, "v0.1.0", "https://example.test", strings.Repeat("a", 64), "v0.1.0")
	if err := SaveInstallationMetadata(paths.MetadataPath(), metadata); err != nil {
		t.Fatal(err)
	}
	manifestURL, _, server := testServer(t, "v0.1.0", "linux-amd64")
	defer server.Close()

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}

	result, err := Update(Options{
		ManifestURL: manifestURL,
		Platform:    "linux-amd64",
	}, stdout, stderr)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !result.AlreadyLatest {
		t.Error("expected AlreadyLatest to be true")
	}
}

func TestUpdateDownloadsNewVersion(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.BinaryPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BinaryPath, []byte("existing"), 0755); err != nil {
		t.Fatal(err)
	}
	metadata := metadataForPaths(paths, "v0.1.0", "https://example.test", strings.Repeat("a", 64), "v0.1.0")
	if err := SaveInstallationMetadata(paths.MetadataPath(), metadata); err != nil {
		t.Fatal(err)
	}
	manifestURL, _, server := testServer(t, "v0.3.0", "linux-amd64")
	defer server.Close()

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}

	_, err = Update(Options{
		ManifestURL: manifestURL,
		Platform:    "linux-amd64",
	}, stdout, stderr)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestUninstallRemovesBinaryAndPlugins(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()
	if _, err := Install(Options{ManifestURL: manifestURL, Platform: "linux-amd64"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("install fixture: %v", err)
	}

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}

	if err := Uninstall(paths, stdout, stderr); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	// Verify binary removed.
	if _, err := os.Stat(paths.BinaryPath); !os.IsNotExist(err) {
		t.Error("binary not removed")
	}

	// Verify plugins removed.
	for _, dir := range []string{paths.ClaudePluginDir, paths.CodexPluginDir} {
		pluginPath := filepath.Join(dir, "freeinference-companion")
		if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
			t.Errorf("plugin not removed: %s", pluginPath)
		}
	}
}

func TestExtractZIPPathTraversal(t *testing.T) {
	// Create a ZIP with a path traversal entry.
	tmpZip := filepath.Join(t.TempDir(), "traversal.zip")
	f, _ := os.Create(tmpZip)
	w := zip.NewWriter(f)
	// A malicious entry trying to escape destDir.
	hdr := &zip.FileHeader{
		Name:   "../../../etc/passwd",
		Method: zip.Store,
	}
	fw, _ := w.CreateHeader(hdr)
	fw.Write([]byte("malicious"))
	w.Close()
	f.Close()

	destDir := t.TempDir()
	err := extractZIP(tmpZip, destDir)
	if err == nil {
		t.Fatal("expected path traversal archive to be rejected")
	}

	// The traversal file should not have been extracted outside destDir.
	// Use a path that doesn't normally exist on any system.
	maliciousFile := filepath.Join(destDir, "..", "..", "..", "tmp", "pwned_by_traversal")
	if _, err := os.Stat(maliciousFile); !os.IsNotExist(err) {
		t.Errorf("path traversal file was extracted to %s", maliciousFile)
	}
	// Also verify the file was not extracted inside destDir (should still not exist).
	insidePath := filepath.Join(destDir, "etc", "passwd")
	if _, err := os.Stat(insidePath); !os.IsNotExist(err) {
		t.Errorf("traversal file was unexpectedly created at %s", insidePath)
	}
}

func TestFetchManifestInvalidURL(t *testing.T) {
	_, err := FetchManifest("not-a-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestManifestPlatformNotFound(t *testing.T) {
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"version": "v1.0.0",
			"platforms": map[string]any{
				"linux-amd64": map[string]string{
					"url":    "https://example.com/zip",
					"sha256": strings.Repeat("0", 64),
				},
			},
		})
	}))
	defer server.Close()

	m, err := FetchManifest(server.URL)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	_, err = m.Platform("nonexistent-platform")
	if err == nil {
		t.Error("expected error for unknown platform")
	}
}

func TestDownloadToInvalidURL(t *testing.T) {
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")
	_, err := DownloadTo("http://127.0.0.1:1/nonexistent", "/tmp/test.zip")
	// Should return an error (connection refused or similar).
	if err == nil {
		t.Error("expected error for unreachable URL")
	}
}

func TestInstallerRemoteURLsRequireHTTPS(t *testing.T) {
	if _, err := FetchManifest("http://example.com/marketplace.json"); err == nil {
		t.Fatal("expected HTTP manifest URL to be rejected")
	}
	if _, err := DownloadTo("file:///tmp/archive.zip", filepath.Join(t.TempDir(), "archive.zip")); err == nil {
		t.Fatal("expected non-HTTP(S) download URL to be rejected")
	}
}

func TestFetchManifestRejectsMalformedReleaseMetadata(t *testing.T) {
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"not-a-version","platforms":{"linux-amd64":{"url":"http://127.0.0.1/release.zip","sha256":"not-a-sha"}}}`))
	}))
	defer server.Close()
	if _, err := FetchManifest(server.URL); err == nil {
		t.Fatal("expected malformed manifest to be rejected")
	}
}

func TestDefaultPaths(t *testing.T) {
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	if paths.BinaryPath == "" {
		t.Error("BinaryPath should not be empty")
	}
	if paths.LocalBin == "" {
		t.Error("LocalBin should not be empty")
	}
}

func TestInstallDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}

	result, err := Install(Options{
		ManifestURL:     manifestURL,
		Platform:        "linux-amd64",
		ExistingVersion: "",
		DryRun:          true,
	}, stdout, stderr)
	if err != nil {
		t.Fatalf("dry-run install: %v", err)
	}
	if result.Version != "v0.2.0" {
		t.Errorf("expected version v0.2.0, got %s", result.Version)
	}
	// Dry run should not set BinaryPath.
	if result.BinaryPath != "" {
		t.Error("dry run should not set BinaryPath")
	}
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(paths.BinaryPath)); !os.IsNotExist(err) {
		t.Errorf("dry-run created binary directory: %v", err)
	}
	if _, err := os.Stat(paths.ClaudePluginDir); !os.IsNotExist(err) {
		t.Errorf("dry-run created plugin directory: %v", err)
	}
	if !strings.Contains(stdout.String(), "Dry run: no files will be downloaded or changed.") {
		t.Errorf("dry-run output missing no-change confirmation:\n%s", stdout.String())
	}
}

func TestUpdateDryRunDoesNotMutate(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.BinaryPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BinaryPath, []byte("old-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	manifestURL, _, server := testServer(t, "v0.2.0", "linux-amd64")
	defer server.Close()
	stdout := &strings.Builder{}
	result, err := Update(Options{
		ManifestURL:     manifestURL,
		Platform:        "linux-amd64",
		ExistingVersion: "v0.1.0",
		DryRun:          true,
	}, stdout, &strings.Builder{})
	if err != nil {
		t.Fatalf("dry-run update: %v", err)
	}
	if result.Updated || result.BinaryPath != "" || len(result.Plugins) != 0 {
		t.Errorf("dry-run reported mutations: %#v", result)
	}
	data, err := os.ReadFile(paths.BinaryPath)
	if err != nil || string(data) != "old-binary" {
		t.Errorf("dry-run changed binary: %q, %v", data, err)
	}
	if _, err := os.Stat(paths.BinaryPath + ".backup-v0.1.0"); !os.IsNotExist(err) {
		t.Errorf("dry-run created backup: %v", err)
	}
}

// TestIsNewer verifies the version comparison logic.
func TestIsNewer(t *testing.T) {
	tests := []struct {
		existing    string
		latest      string
		expectNewer bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.1.0", "v0.1.1", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.0", false},
		{"v1.0.0", "v2.0.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"", "v1.0.0", true},
		{"v0.1.0", "", false},
	}
	for _, tt := range tests {
		m := &MarketplaceManifest{Version: tt.latest}
		got := m.IsNewer(tt.existing)
		if got != tt.expectNewer {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.existing, tt.latest, got, tt.expectNewer)
		}
	}
}

// TestEnsureInPath verifies PATH checking logic.
func TestEnsureInPath(t *testing.T) {
	tmpDir := t.TempDir()
	paths := Paths{
		LocalBin: filepath.Join(tmpDir, "localbin"),
	}

	// Not in PATH.
	t.Setenv("PATH", "/usr/bin:/bin")
	inPath, msg := paths.EnsureInPath()
	if inPath {
		t.Error("should not be in PATH")
	}
	if msg == "" {
		t.Error("expected PATH message")
	}

	// Add to PATH.
	t.Setenv("PATH", paths.LocalBin+":"+os.Getenv("PATH"))
	inPath, msg = paths.EnsureInPath()
	if !inPath {
		t.Error("should be in PATH")
	}
	if msg != "" {
		t.Error("expected empty message when in PATH")
	}
}

// TestVerifyChecksumValid verifies that correct checksums pass.
func TestVerifyChecksumValid(t *testing.T) {
	data := []byte("hello world")
	h := sha256.Sum256(data)
	expected := hex.EncodeToString(h[:])
	err := VerifyChecksum(data, expected)
	if err != nil {
		t.Errorf("valid checksum rejected: %v", err)
	}
}
