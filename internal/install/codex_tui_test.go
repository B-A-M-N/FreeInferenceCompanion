package install

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexTUIInstallPreservesAndRestoresFooter(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := `model_provider = "freeinference"

[tui]
status_line = ["model", "git-branch"] # user footer

[profiles.fast]
model = "glm-5.1"
`
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	status, err := InspectCodexTUI(home, configPath)
	if err != nil {
		t.Fatalf("inspect installed: %v", err)
	}
	if status.Status != "installed" || !status.Installed || !status.Referenced {
		t.Fatalf("installed status = %+v", status)
	}
	for _, item := range []string{"model", "git-branch", "model-with-reasoning", "context-remaining", "current-dir"} {
		if !containsString(status.StatusLine, item) {
			t.Errorf("installed footer missing %q: %v", item, status.StatusLine)
		}
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `[profiles.fast]`) || !strings.Contains(string(updated), "# user footer") {
		t.Error("install did not preserve unrelated config or inline comment")
	}

	if err := UninstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Errorf("uninstall did not restore original config\nwant:\n%s\ngot:\n%s", original, restored)
	}
}

func TestCodexTUIInstallRefusesDrift(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[tui]\nstatus_line = [\"model\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[tui]\nstatus_line = [\"my-custom-footer\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := UninstallCodexTUI(home, configPath, io.Discard); !errors.Is(err, ErrDriftedCodexTUI) {
		t.Fatalf("uninstall drift error = %v", err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); !errors.Is(err, ErrDriftedCodexTUI) {
		t.Fatalf("reinstall drift error = %v", err)
	}
}

func TestCodexTUIUninstallRefusesDifferentConfigPath(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	otherPath := filepath.Join(home, ".codex", "other.toml")
	original := "[tui]\nstatus_line = [\"model\"]\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(otherPath, []byte("[tui]\nstatus_line = [\"model\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := UninstallCodexTUI(home, otherPath, io.Discard); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall error for different config path = %v, want not found", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("mismatched uninstall changed the installed config: %v", err)
	}
}

func TestCodexTUIInstallCreatesNativeFooterWhenMissing(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	if !strings.Contains(contents, "[tui]") || !strings.Contains(contents, "context-remaining") {
		t.Fatalf("native footer missing: %s", contents)
	}
	status, err := InspectCodexTUI(home, configPath)
	if err != nil || status.Status != "installed" {
		t.Fatalf("status = %+v, err=%v", status, err)
	}
}

func TestCodexTUIRejectsMultilineStatusArray(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	original := "[tui]\nstatus_line = [\n  \"model\",\n  \"context-remaining\"\n]\n"
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); err == nil {
		t.Fatal("multiline status_line should be rejected")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Error("rejected install modified config")
	}
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func TestCodexTUIFootersAreScopedByConfigRoot(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, ".codex", "config.toml")
	second := filepath.Join(home, ".harvardcodex", "config.toml")
	firstOriginal := "[tui]\nstatus_line = [\"model\"]\n"
	secondOriginal := "[tui]\nstatus_line = [\"git-branch\"]\n"

	for path, original := range map[string]string{first: firstOriginal, second: secondOriginal} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
	}

	if err := InstallCodexTUI(home, first, io.Discard); err != nil {
		t.Fatalf("install first: %v", err)
	}
	if err := InstallCodexTUI(home, second, io.Discard); err != nil {
		t.Fatalf("install second: %v", err)
	}
	firstStatus, err := InspectCodexTUI(home, first)
	if err != nil || firstStatus.Status != "installed" {
		t.Fatalf("first status=%+v err=%v", firstStatus, err)
	}
	secondStatus, err := InspectCodexTUI(home, second)
	if err != nil || secondStatus.Status != "installed" {
		t.Fatalf("second status=%+v err=%v", secondStatus, err)
	}

	if err := UninstallCodexTUI(home, first, io.Discard); err != nil {
		t.Fatalf("uninstall first: %v", err)
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != firstOriginal {
		t.Fatalf("first config = %q, want %q", data, firstOriginal)
	}
	secondAfterFirst, err := InspectCodexTUI(home, second)
	if err != nil || secondAfterFirst.Status != "installed" {
		t.Fatalf("second after first uninstall status=%+v err=%v", secondAfterFirst, err)
	}
}

func TestCodexTUIUpgradeRemovesObsoleteManagedItems(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := "[tui]\nstatus_line = [\"model\", \"git-branch\"] # user footer\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	legacy := appendUniqueCodexTUIItems([]string{"model", "git-branch"},
		"model-with-reasoning", "context-remaining", "current-dir",
		"project-name", "run-state", "reasoning", "permissions", "approval-mode",
		"workspace-headline", "context-window-size", "context-used", "used-tokens",
		"total-input-tokens", "total-output-tokens", "codex-version")
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("first install: %v", err)
	}
	metaPath, err := codexTUIMetadataPath(home, configPath)
	if err != nil {
		t.Fatal(err)
	}
	meta, found, err := loadCodexTUIMetadata(metaPath)
	if err != nil || !found {
		t.Fatalf("metadata=%+v found=%t err=%v", meta, found, err)
	}
	meta.OwnedItems = legacy
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	legacyContents, setErr := setCodexTUIStatusLine(string(configData), legacy)
	if setErr != nil {
		t.Fatal(setErr)
	}
	if err := os.WriteFile(configPath, []byte(legacyContents), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	for _, obsolete := range []string{"project-name", "run-state", "workspace-headline", "used-tokens", "codex-version"} {
		if strings.Contains(contents, `"`+obsolete+`"`) {
			t.Errorf("upgrade retained obsolete managed item %q: %s", obsolete, contents)
		}
	}
	for _, wanted := range []string{"model", "git-branch", "model-with-reasoning", "context-remaining", "current-dir"} {
		if !strings.Contains(contents, `"`+wanted+`"`) {
			t.Errorf("upgrade missing %q: %s", wanted, contents)
		}
	}
	if !strings.Contains(contents, "# user footer") {
		t.Error("upgrade lost user inline comment")
	}

	if err := UninstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("uninstall after upgrade: %v", err)
	}
	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("uninstall after upgrade = %q, want %q", data, original)
	}
}

func TestCodexTUIDoesNotAddWorkspacePermissionsItems(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := "[tui]\nstatus_line = [\"model\"]\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	for _, unwanted := range []string{"permissions", "approval-mode", "workspace-headline"} {
		if strings.Contains(contents, `"`+unwanted+`"`) {
			t.Errorf("native footer unexpectedly contains %q: %s", unwanted, contents)
		}
	}
}

func TestCodexTUIMigratesMatchingSingletonMetadata(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := "[tui]\nstatus_line = [\"model\", \"git-branch\"] # user\n"
	desired := appendUniqueCodexTUIItems([]string{"model", "git-branch"}, "model-with-reasoning", "context-remaining", "current-dir")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	legacyPath := legacyCodexTUIMetadataPath(home)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	meta := &codexTUIMetadata{
		InstalledAt:   time.Now().UTC(),
		ConfigPath:    configPath,
		HadPrevious:   true,
		PreviousItems: []string{"model", "git-branch"},
		OwnedItems:    append(desired, "obsolete-native"),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	legacyConfig := string(setCodexTUITest(t, original, meta.OwnedItems))
	if err := os.WriteFile(configPath, []byte(legacyConfig), 0600); err != nil {
		t.Fatal(err)
	}

	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("upgrade migrated install: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy metadata still exists: err=%v", err)
	}
	scopedPath, err := codexTUIMetadataPath(home, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scopedPath); err != nil {
		t.Fatalf("scoped metadata missing: %v", err)
	}
	if err := UninstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("uninstall after migration: %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	expectedWithoutOriginalLine := "[tui]\nstatus_line = [\"model\",\"git-branch\"] # user\n"
	if string(got) != expectedWithoutOriginalLine {
		t.Fatalf("restored=%q want=%q", got, expectedWithoutOriginalLine)
	}
}

func setCodexTUITest(t *testing.T, contents string, items []string) string {
	t.Helper()
	out, err := setCodexTUIStatusLine(contents, items)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCodexTUIMigratesMatchingSingletonMetadataPreservesExactLine(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := "[tui]\nstatus_line = [\"model\", \"git-branch\"] # user\n"
	legacyItems := appendUniqueCodexTUIItems([]string{"model", "git-branch"}, "model-with-reasoning", "context-remaining", "current-dir", "obsolete-native")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	legacyConfig, err := setCodexTUIStatusLine(original, legacyItems)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(legacyConfig), 0600); err != nil {
		t.Fatal(err)
	}
	legacyPath := legacyCodexTUIMetadataPath(home)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	meta := &codexTUIMetadata{
		InstalledAt:   time.Now().UTC(),
		ConfigPath:    configPath,
		HadPrevious:   true,
		PreviousItems: []string{"model", "git-branch"},
		PreviousLine:  "status_line = [\"model\", \"git-branch\"] # user\n",
		OwnedItems:    legacyItems,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := UninstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("exact restored=%q want=%q", got, original)
	}
}
