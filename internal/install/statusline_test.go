package install

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadSettingsMap reads and parses the settings file. Exits the test on error.
func loadSettingsMap(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(settingsPath(home))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings not valid JSON: %v", err)
	}
	return settings
}

// ---- P0-4 regression tests: metadata errors, malformed settings, drift ----

// TestLoadMetadata_DistinguishesNotFoundFromOtherErrors verifies that
// loadMetadata treats file-not-found as "absent" (false, nil) but treats
// other read errors (permission denied, I/O) as a hard error so the installer
// does not silently destroy rollback history.
func TestLoadMetadata_DistinguishesNotFoundFromOtherErrors(t *testing.T) {
	home := t.TempDir()
	metaFile := metadataPath(ScopeUser, home)

	// Missing file → (zero, false, nil).
	_, ok, err := loadMetadata(metaFile)
	if err != nil || ok {
		t.Fatalf("missing metadata: ok=%v, err=%v", ok, err)
	}

	// Directory in place of file → must be an error, not "absent".
	if err := os.MkdirAll(metaFile, 0700); err != nil {
		t.Fatal(err)
	}
	_, ok, err = loadMetadata(metaFile)
	if err == nil || ok {
		t.Fatalf("directory-at-metadata-path must be an error: ok=%v, err=%v", ok, err)
	}
}

// TestUninstallRefusesOnMalformedSettings verifies the P0-4 fix: a malformed
// settings file causes uninstall to refuse every mutation — wrapper and
// metadata must remain intact (no partial uninstall).
func TestUninstallRefusesOnMalformedSettings(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Corrupt the settings file.
	if err := os.WriteFile(settingsPath(home), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	err := UninstallClaudeStatusLine(home, ScopeUser, home, io.Discard)
	if err == nil {
		t.Fatal("uninstall must refuse on malformed settings")
	}
	// Wrapper must remain intact.
	if _, statErr := os.Stat(wrapperPath(home)); statErr != nil {
		t.Errorf("wrapper was removed despite malformed settings: %v", statErr)
	}
	// Metadata must remain intact.
	if _, statErr := os.Stat(metadataPath(ScopeUser, home)); statErr != nil {
		t.Errorf("metadata was removed despite malformed settings: %v", statErr)
	}
}

// TestUninstallRetainsMetadataOnDrift verifies the P0-4 fix: when uninstall
// detects a drifted statusLine (user customized it), metadata must NOT be
// deleted — it is the authoritative record needed for manual reconciliation.
func TestUninstallRetainsMetadataOnDrift(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Simulate the user replacing our statusLine with their own.
	userCustomized := map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "/my/custom/statusline"},
	}
	data, _ := json.Marshal(userCustomized)
	if err := os.WriteFile(settingsPath(home), data, 0644); err != nil {
		t.Fatal(err)
	}
	err := UninstallClaudeStatusLine(home, ScopeUser, home, io.Discard)
	if !errors.Is(err, ErrDriftedStatusLine) {
		t.Fatalf("expected ErrDriftedStatusLine, got: %v", err)
	}
	// Metadata must remain intact after a drifted uninstall.
	if _, statErr := os.Stat(metadataPath(ScopeUser, home)); statErr != nil {
		t.Errorf("metadata was deleted on drifted uninstall: %v", statErr)
	}
}

// TestReinstallRepairsWrapperMode verifies that a reinstall whose wrapper
// lost its executable bits is repaired (chmod 0755).
func TestReinstallRepairsWrapperMode(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Strip executable bits from the wrapper.
	if err := os.Chmod(wrapperPath(home), 0644); err != nil {
		t.Fatal(err)
	}
	// Reinstall — must repair the mode.
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	info, err := os.Stat(wrapperPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("reinstall did not repair wrapper executable bits: mode=%o", info.Mode())
	}
}

func TestInstallFresh(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "/opt/bin/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}

	wrapper := wrapperPath(home)
	info, err := os.Stat(wrapper)
	if err != nil {
		t.Fatalf("wrapper missing: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("wrapper must be executable")
	}
	content, _ := os.ReadFile(wrapper)
	if !strings.Contains(string(content), "/opt/bin/fi") {
		t.Error("wrapper should embed the resolved binary path")
	}
	if !strings.Contains(string(content), "status --compact --client claude-code") {
		t.Error("wrapper should invoke fi status with the canonical client name")
	}

	settings := loadSettingsMap(t, home)
	sl, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatal("statusLine not set")
	}
	if sl["command"] != wrapper {
		t.Errorf("statusLine command = %v", sl["command"])
	}

	// Metadata recorded.
	if _, err := os.Stat(metadataPath(ScopeUser, home)); err != nil {
		t.Error("installation metadata missing")
	}
}

func TestInstallComposesWithExisting(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "/usr/local/bin/my-old-status"},
		"otherKey":   "preserved",
	}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(settingsPath(home), data, 0644); err != nil {
		t.Fatal(err)
	}

	if err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}

	content, _ := os.ReadFile(wrapperPath(home))
	if !strings.Contains(string(content), "/usr/local/bin/my-old-status") {
		t.Error("wrapper should replay stdin to the previous status command")
	}
	settings := loadSettingsMap(t, home)
	if settings["otherKey"] != "preserved" {
		t.Error("unrelated settings must be preserved")
	}

	// Metadata remembers the previous status line.
	metaData, _ := os.ReadFile(metadataPath(ScopeUser, home))
	var meta Metadata
	json.Unmarshal(metaData, &meta)
	if !meta.HadPrevious {
		t.Error("metadata should record the previous status line")
	}
}

func TestInstallRefusesComplexStatusLine(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	original := `{"statusLine": "not-an-object"}`
	if err := os.WriteFile(settingsPath(home), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard)
	if err == nil {
		t.Fatal("complex status line must stop installation")
	}

	data, _ := os.ReadFile(settingsPath(home))
	if string(data) != original {
		t.Error("refused install must not modify settings")
	}
	if _, statErr := os.Stat(wrapperPath(home)); statErr == nil {
		t.Error("refused install must not write a wrapper")
	}
}

func TestInstallRefusesMalformedSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	original := "{invalid json"
	if err := os.WriteFile(settingsPath(home), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	if err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard); err == nil {
		t.Fatal("malformed settings must stop installation")
	}
	data, _ := os.ReadFile(settingsPath(home))
	if string(data) != original {
		t.Error("malformed settings must remain untouched")
	}
}

func TestUninstallRestoresPrevious(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "/usr/local/bin/my-old-status"},
		"otherKey":   42.0,
	}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(settingsPath(home), data, 0644); err != nil {
		t.Fatal(err)
	}

	if err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := UninstallClaudeStatusLine(home, ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	settings := loadSettingsMap(t, home)
	sl, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatal("previous statusLine should have been restored")
	}
	if sl["command"] != "/usr/local/bin/my-old-status" {
		t.Errorf("restored command = %v", sl["command"])
	}
	if settings["otherKey"] != 42.0 {
		t.Error("unrelated settings must survive install/uninstall")
	}
	if _, err := os.Stat(wrapperPath(home)); err == nil {
		t.Error("wrapper should be removed")
	}
}

func TestUninstallRemovesKeyWhenNoPrevious(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := UninstallClaudeStatusLine(home, ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	settings := loadSettingsMap(t, home)
	if _, ok := settings["statusLine"]; ok {
		t.Error("statusLine key should be removed when there was no previous one")
	}
}

func TestInstallIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	settings := loadSettingsMap(t, home)
	sl := settings["statusLine"].(map[string]any)
	if sl["command"] != filepath.Join(claudeDir(home), wrapperName) {
		t.Errorf("reinstall should keep our wrapper: %v", sl["command"])
	}
}

// TestInstallAtomicSettingsUpdate verifies that after InstallClaudeStatusLine
// returns, the settings file is parseable JSON — the temp+rename path never
// leaves a partial write visible to concurrent readers.
func TestInstallAtomicSettingsUpdate(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	// Seed a settings file with unrelated keys that must survive.
	seed := map[string]any{"theme": "dark", "permissions": map[string]any{"allow": []string{"Bash(ls)"}}}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(settingsPath(home), data, 0644); err != nil {
		t.Fatal(err)
	}

	if err := InstallClaudeStatusLine(home, "/opt/bin/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Immediately readable as JSON — no partial state observable.
	settings := loadSettingsMap(t, home)
	if settings["theme"] != "dark" {
		t.Errorf("unrelated key lost: %+v", settings)
	}
	if _, ok := settings["statusLine"]; !ok {
		t.Error("statusLine should be present")
	}

	// File permissions preserved: not group/world-writable. The atomic
	// temp+rename path must never relax permissions beyond what existed.
	info, err := os.Stat(settingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0022 != 0 {
		t.Errorf("settings file is group/world writable: %o", perm)
	}
}

// TestInstallFallsBackToPATH verifies that when no binary path is recorded,
// the wrapper still resolves `fi` from PATH at runtime rather than failing.
func TestInstallFallsBackToPATH(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	content, _ := os.ReadFile(wrapperPath(home))
	body := string(content)
	if !strings.Contains(body, "command -v fi") {
		t.Errorf("wrapper should fall back to PATH lookup when binary is empty: %s", body)
	}
	// And never embed an empty path that would break the wrapper.
	if strings.Contains(body, `""`) {
		// Quote pairs are fine in legitimate shell syntax, but a literal
		// empty-quoted executable path would be a bug.
		if strings.Contains(body, ` [[ -x "" ]]`) || strings.Contains(body, ` "" `) {
			t.Errorf("wrapper embeds an empty path: %s", body)
		}
	}
}

// TestInstallWithMissingBinaryOnDisk verifies that even when the recorded
// binary path does not exist at hook time, the wrapper still falls through to
// PATH lookup and exits cleanly.
func TestInstallWithMissingBinaryOnDisk(t *testing.T) {
	home := t.TempDir()
	// Point at a path that doesn't exist.
	if err := InstallClaudeStatusLine(home, "/definitely/not/installed/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	content, _ := os.ReadFile(wrapperPath(home))
	body := string(content)
	if !strings.Contains(body, "command -v fi") {
		t.Errorf("wrapper must fall back to PATH when the recorded binary is missing: %s", body)
	}
}

// ---- P0-4 regression tests: ownership, drift, rollback, mode preservation ----

// TestReinstallPreservesOriginalPreInstallValue is the core P0-4 regression.
// The previous implementation set hadPrevious=false on a second install when
// the current settings already pointed at our wrapper. That destroyed the
// record of the user's original status line. Reinstalls must keep the
// original pre-install baseline across all subsequent installs.
func TestReinstallPreservesOriginalPreInstallValue(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "/usr/local/bin/precious"},
	}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(settingsPath(home), data, 0644); err != nil {
		t.Fatal(err)
	}

	// First install records the precious original.
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("first install: %v", err)
	}
	metaAfter1, _ := loadMetadataForTest(t, home)
	if !metaAfter1.HadPrevious || string(metaAfter1.PreInstallStatusLine) == "" {
		t.Fatal("first install must record the precious original")
	}

	// Second install — must NOT collapse the prior-history pointer.
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	metaAfter2, _ := loadMetadataForTest(t, home)
	if !metaAfter2.HadPrevious {
		t.Errorf("reinstall collapsed HadPrevious — original pre-install value would be lost on uninstall")
	}
	if string(metaAfter2.PreInstallStatusLine) == "" {
		t.Errorf("reinstall erased PreInstallStatusLine — uninstall would lose the user's original config")
	}
	if !strings.Contains(string(metaAfter2.PreInstallStatusLine), "/usr/local/bin/precious") {
		t.Errorf("reinstall did not preserve the precious original: %s", metaAfter2.PreInstallStatusLine)
	}

	// Uninstall must restore the precious original, not delete the key.
	if err := UninstallClaudeStatusLine(home, ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	settings := loadSettingsMap(t, home)
	sl, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatal("uninstall after reinstall must restore the precious original status line")
	}
	if sl["command"] != "/usr/local/bin/precious" {
		t.Errorf("restored command = %v, want /usr/local/bin/precious", sl["command"])
	}
}

// TestUninstallRefusesToDeleteUserCustomization verifies the drift-detection
// branch. If the user changes statusLine AFTER install (i.e. to a value the
// installer does not own and that does not match the recorded pre-install
// value), uninstall must refuse to delete it rather than silently destroying
// their customization.
func TestUninstallRefusesToDeleteUserCustomization(t *testing.T) {
	home := t.TempDir()
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Simulate the user replacing our statusLine with their own.
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	userCustomized := map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "/my/custom/statusline"},
	}
	data, _ := json.Marshal(userCustomized)
	if err := os.WriteFile(settingsPath(home), data, 0644); err != nil {
		t.Fatal(err)
	}

	err := UninstallClaudeStatusLine(home, ScopeUser, home, io.Discard)
	if !errors.Is(err, ErrDriftedStatusLine) {
		t.Fatalf("uninstall with drifted statusLine must return ErrDriftedStatusLine, got: %v", err)
	}
	// The user's custom value must remain intact.
	settings := loadSettingsMap(t, home)
	sl, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatal("drifted statusLine must be preserved")
	}
	if sl["command"] != "/my/custom/statusline" {
		t.Errorf("user customization destroyed by uninstall: %+v", sl)
	}
}

// TestInstallPreservesCustomFileMode verifies that the installer does NOT
// force the settings file to mode 0644 — the previous behavior. Whatever mode
// the user had on their settings file is preserved.
func TestInstallPreservesCustomFileMode(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	// User has an unusually restrictive mode on their settings file (e.g.
	// 0600). The installer must preserve it, not relax to 0644.
	if err := os.WriteFile(settingsPath(home), []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	info, err := os.Stat(settingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("custom file mode not preserved: got %o, want 0600", perm)
	}
}

// TestReinstallAfterUserKeepsCustomOriginal verifies that when the user has
// NOT changed the statusLine (it still points at our wrapper), a reinstall is
// safe and does not destroy history. This complements the drift test by
// asserting the no-drift path stays an idempotent refresh.
func TestReinstallAfterUserKeepsCustomOriginal(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "/orig"},
	}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(settingsPath(home), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaudeStatusLine(home, "/opt/fi", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	// User does NOT touch statusLine — a reinstall should refresh cleanly.
	if err := InstallClaudeStatusLine(home, "/opt/fi/v2", ScopeUser, home, io.Discard); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	// Wrapper should now embed the v2 path.
	content, _ := os.ReadFile(wrapperPath(home))
	if !strings.Contains(string(content), "/opt/fi/v2") {
		t.Errorf("reinstall did not refresh the wrapper binary path: %s", string(content))
	}
}

func loadMetadataForTest(t *testing.T, home string) (Metadata, bool) {
	t.Helper()
	m, ok, _ := loadMetadata(metadataPath(ScopeUser, home))
	return m, ok
}
