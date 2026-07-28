// Package install manages the Claude Code status-line integration.
// It composes with any existing status line instead of overwriting it, and
// records installation metadata so uninstall restores exactly what changed.
//
// Safety contracts (P0-4):
//
//   - Installation is transactional: if metadata cannot be written after the
//     wrapper and settings are updated, both are rolled back to their prior
//     on-disk state. Rollback is best-effort (each step is independent and
//     failure-tolerant); the goal is that a half-installed state is never
//     observable to a concurrent reader, but perfect atomicity across a crash
//     mid-rename is not guaranteed without a durable transaction journal.
//   - Reinstalls preserve the original pre-install status line across runs.
//     A second install no longer collapses the prior-history pointer. Reinstall
//     verifies that the current settings["statusLine"] still matches the value
//     we own — if the user changed it, the install is refused.
//   - Uninstall refuses to delete or replace a statusLine value the user
//     changed after install. The user is told to reconcile manually instead.
//   - Ownership is by exact value equality, never by substring matching.
//   - Original settings file mode is preserved across writes.
package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Metadata records one status-line installation.
//
// OwnedStatusLine is the exact JSON value the installer wrote into the user's
// settings["statusLine"]. It is the source of truth for "do we still own this
// configuration?" — a value-equality comparison, never a substring test.
//
// PreInstallStatusLine is the value of settings["statusLine"] that existed
// immediately before the FIRST installation. It is preserved across reinstalls
// so the user can always roll back to their original configuration.
type Metadata struct {
	InstalledAt          time.Time       `json:"installed_at"`
	WrapperPath          string          `json:"wrapper_path"`
	BinaryPath           string          `json:"binary_path"`
	SettingsPath         string          `json:"settings_path"`
	SettingsMode         uint32          `json:"settings_mode"`
	HadPrevious          bool            `json:"had_previous"`
	PreInstallStatusLine json.RawMessage `json:"pre_install_status_line,omitempty"`
	// SettingsHashBefore/After are kept for diagnostic purposes. They are NOT
	// consulted for ownership decisions — OwnedStatusLine is.
	SettingsHashBefore string          `json:"settings_hash_before"`
	SettingsHashAfter  string          `json:"settings_hash_after"`
	OwnedStatusLine    json.RawMessage `json:"owned_status_line"`
}

const wrapperName = "statusline-freeinference.sh"

// ErrComplexStatusLine is returned when an existing status line cannot be
// composed safely; installation stops without changing anything.
var ErrComplexStatusLine = errors.New("existing statusLine is too complex to compose safely")

// ErrDriftedStatusLine is returned when uninstall or reinstall detects that
// the user has changed the statusLine value since installation. We refuse to
// destroy the user's customization automatically.
var ErrDriftedStatusLine = errors.New("statusLine was changed after installation; refusing to overwrite the user's customization")

func claudeDir(home string) string { return filepath.Join(home, ".claude") }
func settingsPath(home string) string {
	return filepath.Join(claudeDir(home), "settings.json")
}
func wrapperPath(home string) string { return filepath.Join(claudeDir(home), wrapperName) }
func metadataPath(home string) string {
	return filepath.Join(home, ".config", "freeinference-companion", "installations", "claude-statusline.json")
}

// InstallClaudeStatusLine installs the composed status line.
// binaryPath is the resolved fi binary to embed in the wrapper; when empty,
// the wrapper falls back to resolving `fi` from PATH at runtime.
//
// This function is transactional: any failure after the first mutation rolls
// all touched files back to their prior on-disk state. Rollback is best-effort;
// perfect atomicity across a crash mid-rename is not guaranteed without a
// durable transaction journal.
//
// On reinstall, the current settings["statusLine"] is compared with the
// recorded OwnedStatusLine. If the user changed it, installation is refused
// with ErrDriftedStatusLine.
func InstallClaudeStatusLine(home, binaryPath string, stdout io.Writer) error {
	settingsFile := settingsPath(home)
	wrapper := wrapperPath(home)

	settings, originalBytes, originalMode, err := readSettingsAndMode(settingsFile)
	if err != nil {
		return fmt.Errorf("settings file is malformed; refusing to modify it: %w", err)
	}

	// Load any existing metadata first. Reinstall must NOT collapse the prior
	// history pointer — the pre-install value recorded on the first install is
	// authoritative across all subsequent reinstalls.
	existingMeta, haveExistingMeta, metaErr := loadMetadata(metadataPath(home))
	if metaErr != nil {
		return fmt.Errorf("installation metadata is corrupted; refusing to modify. Repair with: fi status-line reset, then reinstall: %w", metaErr)
	}

	// Determine the pre-install status line value. On a fresh install this is
	// the current settings["statusLine"]. On a reinstall it is whatever the
	// first install captured — never "current settings" (the user may have
	// changed it; that is their right and we must not destroy it).
	var (
		preInstallStatusLine json.RawMessage
		hadPrevious          bool
	)
	if haveExistingMeta {
		// Preserve the original pre-install baseline across reinstalls.
		hadPrevious = existingMeta.HadPrevious
		preInstallStatusLine = existingMeta.PreInstallStatusLine
	} else {
		preInstallStatusLine, hadPrevious, err = extractPreInstall(settings)
		if err != nil {
			return err
		}
	}

	// Compose the new statusLine value we want to own.
	ownedStatusLine, err := json.Marshal(map[string]any{
		"type":    "command",
		"command": wrapper,
	})
	if err != nil {
		return fmt.Errorf("encode owned status line: %w", err)
	}

	// Reinstall drift check: if we have existing metadata, verify that the
	// current settings["statusLine"] still matches what we own. If the user
	// changed it after install, refuse to overwrite their customization.
	if haveExistingMeta {
		currentStatusLine, present := settings["statusLine"]
		currentBytes, _ := json.Marshal(currentStatusLine)
		if !present || !bytes.Equal(normalizeJSON(currentBytes), normalizeJSON(existingMeta.OwnedStatusLine)) {
			return fmt.Errorf("%w: the current statusLine does not match our recorded value; reconcile manually or run `fi status-line reset` first", ErrDriftedStatusLine)
		}
	}

	// Build the new wrapper. It composes with the pre-install value (not the
	// current settings["statusLine"]) so reinstalls stay stable.
	script := buildWrapper(binaryPath, preInstallStatusLine)

	// Read the prior wrapper bytes (if any) so we can roll it back on a later
	// failure.
	priorWrapper, priorWrapperMode, _ := readFileAndMode(wrapper)

	// Capture prior metadata bytes for rollback too.
	metaFile := metadataPath(home)
	priorMeta, priorMetaMode, _ := readFileAndMode(metaFile)

	// ---- Mutation phase begins. Anything that fails here must roll back. ----

	if err := os.MkdirAll(claudeDir(home), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
		return fmt.Errorf("write wrapper: %w", err)
	}

	// Stash a structured copy of the new settings for the atomic write. We
	// must round-trip through JSON to preserve the user's existing keys
	// without disturbing them.
	settings["statusLine"] = mustUnmarshal(ownedStatusLine)
	newSettingsBytes, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		rollbackInstall(wrapper, priorWrapper, priorWrapperMode, settingsFile, originalBytes, originalMode, metaFile, priorMeta, priorMetaMode)
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := writeFilePreservingMode(settingsFile, newSettingsBytes, originalMode); err != nil {
		rollbackInstall(wrapper, priorWrapper, priorWrapperMode, settingsFile, originalBytes, originalMode, metaFile, priorMeta, priorMetaMode)
		return fmt.Errorf("write settings: %w", err)
	}

	// Write the new metadata. If this fails, roll back the wrapper AND the
	// settings file to their prior state so we never leave a half-installation.
	newMeta := &Metadata{
		InstalledAt:          time.Now().UTC(),
		WrapperPath:          wrapper,
		BinaryPath:           binaryPath,
		SettingsPath:         settingsFile,
		SettingsMode:         modeToUint32(originalMode),
		HadPrevious:          hadPrevious,
		PreInstallStatusLine: preInstallStatusLine,
		SettingsHashBefore:   hashBytes(originalBytes),
		SettingsHashAfter:    hashBytes(newSettingsBytes),
		OwnedStatusLine:      ownedStatusLine,
	}
	if err := writeMetadata(metaFile, newMeta); err != nil {
		rollbackInstall(wrapper, priorWrapper, priorWrapperMode, settingsFile, originalBytes, originalMode, metaFile, priorMeta, priorMetaMode)
		return fmt.Errorf("write installation metadata (rolled back): %w", err)
	}

	fmt.Fprintf(stdout, "Status line installed.\n  Wrapper: %s\n  Config:  %s\n", wrapper, settingsFile)
	if hadPrevious {
		fmt.Fprintln(stdout, "  Composed with your existing status line (both run).")
	}
	fmt.Fprintln(stdout, "Restart Claude Code to see the FreeInference status line.")
	return nil
}

// UninstallClaudeStatusLine restores the previous statusLine value (or
// removes the key) without touching the rest of the settings file.
//
// Ownership-aware: if the current statusLine does NOT match what we own
// (the user changed it after install), we refuse to delete or replace it.
// The user must reconcile manually — the printed instructions tell them how.
func UninstallClaudeStatusLine(home string, stdout io.Writer) error {
	settingsFile := settingsPath(home)
	wrapper := wrapperPath(home)
	metaFile := metadataPath(home)

	settings, _, mode, err := readSettingsAndMode(settingsFile)
	if err != nil {
		// Malformed settings: still remove our wrapper, but do not touch settings.
		os.Remove(wrapper)
		return fmt.Errorf("settings file is malformed; removed wrapper only: %w", err)
	}

	meta, haveMeta, metaErr := loadMetadata(metaFile)
	if metaErr != nil {
		return fmt.Errorf("installation metadata is corrupted; refusing to modify. Repair with: fi status-line reset, then reinstall: %w", metaErr)
	}

	// Decide what to do with settings["statusLine"] based on ownership.
	currentStatusLine, statusLinePresent := settings["statusLine"]
	currentStatusLineBytes, _ := json.Marshal(currentStatusLine)
	modified := false // tracks whether settings map was mutated

	if statusLinePresent {
		if haveMeta && bytes.Equal(normalizeJSON(currentStatusLineBytes), normalizeJSON(meta.OwnedStatusLine)) {
			// We still own the current value. Restore the pre-install value
			// (or delete the key entirely if there was none).
			if meta.HadPrevious && len(meta.PreInstallStatusLine) > 0 {
				settings["statusLine"] = mustUnmarshal(meta.PreInstallStatusLine)
			} else {
				delete(settings, "statusLine")
			}
			modified = true
		} else if haveMeta && meta.HadPrevious && bytes.Equal(normalizeJSON(currentStatusLineBytes), normalizeJSON(meta.PreInstallStatusLine)) {
			// Edge case: the user manually restored the pre-install value.
			// Leave it exactly as-is — do NOT delete or modify settings["statusLine"].
		} else {
			// The current statusLine does not match what we own NOR the
			// recorded pre-install value. The user has customized it since
			// install; we must not destroy their work. Print the recorded
			// pre-install value and ask them to reconcile manually.
			fmt.Fprintln(stdout, "Your Claude settings have a statusLine value the installer did not put there.")
			fmt.Fprintln(stdout, "Refusing to overwrite it. To finish uninstalling manually:")
			fmt.Fprintf(stdout, "  - Edit %s and remove or adjust the statusLine key.\n", settingsFile)
			if meta.HadPrevious && len(meta.PreInstallStatusLine) > 0 {
				fmt.Fprintf(stdout, "  - The value that existed before install was: %s\n", string(meta.PreInstallStatusLine))
			}
			fmt.Fprintf(stdout, "  - Then remove %s and %s.\n", wrapper, metaFile)
			// Still drop our metadata — it is no longer authoritative.
			os.Remove(metaFile)
			return ErrDriftedStatusLine
		}
	}

	// Atomic settings write only if we modified the settings map. The wrapper
	// and metadata are removed only AFTER the settings write succeeds — if
	// settings write fails, we leave the (still-owned) wrapper in place so the
	// user's status line keeps working.
	if modified {
		newSettingsBytes, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return fmt.Errorf("encode settings: %w", err)
		}
		if err := writeFilePreservingMode(settingsFile, newSettingsBytes, mode); err != nil {
			return fmt.Errorf("write settings: %w", err)
		}
	}

	os.Remove(wrapper)
	os.Remove(metaFile)

	fmt.Fprintln(stdout, "Status line uninstalled.")
	return nil
}

// extractPreInstall inspects settings["statusLine"] to determine whether it
// can be composed with our wrapper. Returns the prior value (if any) and a
// flag indicating whether a prior value exists. Returns ErrComplexStatusLine
// when the existing value cannot be composed safely — in which case the
// caller must refuse installation without modifying anything.
func extractPreInstall(settings map[string]any) (json.RawMessage, bool, error) {
	raw, ok := settings["statusLine"]
	if !ok {
		return nil, false, nil
	}
	// If it already points at our wrapper, treat it as a no-op prior state
	// (this branch is only taken on fresh installs without existing metadata,
	// so finding our wrapper here is unusual but should not panic).
	rawBytes, _ := json.Marshal(raw)
	if strings.Contains(string(rawBytes), wrapperName) {
		return nil, false, nil
	}
	prev, ok := raw.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("%w: not a command object; remove it manually or back up the settings file", ErrComplexStatusLine)
	}
	prevType, _ := prev["type"].(string)
	prevCmd, _ := prev["command"].(string)
	if prevType != "command" || prevCmd == "" {
		return nil, false, fmt.Errorf("%w: unsupported statusLine shape; remove it manually or back up the settings file", ErrComplexStatusLine)
	}
	return rawBytes, true, nil
}

// buildWrapper renders the status-line wrapper. It reads stdin once and
// replays it to both the previous status command (if any) and fi.
func buildWrapper(binaryPath string, previous json.RawMessage) string {
	var prevCmd string
	if len(previous) > 0 {
		var prev struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(previous, &prev) == nil {
			prevCmd = prev.Command
		}
	}

	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# FreeInference Companion status line wrapper\n")
	b.WriteString("# Generated by: fi status-line install\n")
	b.WriteString("set -u\n")
	b.WriteString("input=\"$(cat)\"\n\n")

	// fi side: prefer the recorded binary path, fall back to PATH.
	b.WriteString("fi_out=\"\"\n")
	if binaryPath != "" {
		fmt.Fprintf(&b, "if [[ -x %s ]]; then\n", shellQuote(binaryPath))
		fmt.Fprintf(&b, "  fi_out=\"$(printf '%%s' \"$input\" | %s status --compact --client claude-code 2>/dev/null || true)\"\n", shellQuote(binaryPath))
		b.WriteString("elif command -v fi >/dev/null 2>&1; then\n")
		b.WriteString("  fi_out=\"$(printf '%s' \"$input\" | fi status --compact --client claude-code 2>/dev/null || true)\"\n")
		b.WriteString("fi\n")
	} else {
		b.WriteString("if command -v fi >/dev/null 2>&1; then\n")
		b.WriteString("  fi_out=\"$(printf '%s' \"$input\" | fi status --compact --client claude-code 2>/dev/null || true)\"\n")
		b.WriteString("fi\n")
	}

	// Previous status line side.
	if prevCmd != "" {
		b.WriteString("\nprev_out=\"\"\n")
		fmt.Fprintf(&b, "prev_out=\"$(printf '%%s' \"$input\" | %s 2>/dev/null || true)\"\n", prevCmd)
		b.WriteString("\nif [[ -n \"$fi_out\" && -n \"$prev_out\" ]]; then\n")
		b.WriteString("  printf '%s | %s\\n' \"$prev_out\" \"$fi_out\"\n")
		b.WriteString("elif [[ -n \"$fi_out\" ]]; then\n")
		b.WriteString("  printf '%s\\n' \"$fi_out\"\n")
		b.WriteString("elif [[ -n \"$prev_out\" ]]; then\n")
		b.WriteString("  printf '%s\\n' \"$prev_out\"\n")
		b.WriteString("else\n")
		b.WriteString("  printf '\\n'\n")
		b.WriteString("fi\n")
	} else {
		b.WriteString("\nif [[ -n \"$fi_out\" ]]; then\n")
		b.WriteString("  printf '%s\\n' \"$fi_out\"\n")
		b.WriteString("else\n")
		b.WriteString("  printf '\\n'\n")
		b.WriteString("fi\n")
	}
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// readSettingsAndMode reads the settings file, returning the parsed map, the
// raw bytes, and the file mode. Missing file returns an empty map, nil bytes,
// and a default mode (0600). Empty bytes also return an empty map.
func readSettingsAndMode(path string) (map[string]any, []byte, os.FileMode, error) {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil, 0600, nil
		}
		return nil, nil, 0, err
	}
	mode := os.FileMode(0600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
		if mode == 0 {
			mode = 0600
		}
	}
	if len(data) == 0 {
		return settings, data, mode, nil
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, nil, 0, err
	}
	return settings, data, mode, nil
}

// readFileAndMode reads a file and its mode. Missing file → (nil, 0, os.ErrNotExist).
func readFileAndMode(path string) ([]byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}

// writeFilePreservingMode writes data atomically (temp + rename) and forces
// the final file to the given mode. If mode is 0, defaults to 0600.
func writeFilePreservingMode(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0600
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "settings-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// rollbackInstall restores the on-disk state after a failed mutation phase.
// Each step is best-effort: we are already on an error path, and the best we
// can do is approximate the prior state. Missing prior content means "remove
// what we created".
func rollbackInstall(
	wrapper string, priorWrapper []byte, priorWrapperMode os.FileMode,
	settingsFile string, priorSettings []byte, priorSettingsMode os.FileMode,
	metaFile string, priorMeta []byte, priorMetaMode os.FileMode,
) {
	if priorWrapper == nil {
		_ = os.Remove(wrapper)
	} else {
		mode := priorWrapperMode
		if mode == 0 {
			mode = 0755
		}
		_ = os.WriteFile(wrapper, priorWrapper, mode)
	}
	if priorSettings == nil {
		_ = os.Remove(settingsFile)
	} else {
		_ = writeFilePreservingMode(settingsFile, priorSettings, priorSettingsMode)
	}
	if priorMeta == nil {
		_ = os.Remove(metaFile)
	} else {
		_ = writeFilePreservingMode(metaFile, priorMeta, priorMetaMode)
	}
}

func writeMetadata(path string, meta *Metadata) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeFilePreservingMode(path, data, 0600)
}

// LoadMetadata reads and parses installation metadata.
// Returns (zero, false, nil) for missing file, (zero, false, err) for corrupt file.
// The error distinguishes "no metadata" from "corrupt metadata" so callers can
// refuse to install/uninstall when metadata exists but is malformed.
func loadMetadata(path string) (Metadata, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, false, nil
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, false, err
	}
	return m, true, nil
}

// mustUnmarshal is the JSON-decode equivalent used when the bytes are known
// good (e.g. they were produced by json.Marshal in the same call).
func mustUnmarshal(b []byte) any {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		// Should be impossible by construction.
		panic(fmt.Sprintf("internal: mustUnmarshal got bad bytes: %v", err))
	}
	return v
}

// normalizeJSON re-marshals arbitrary JSON bytes through a canonical encoder
// so that key ordering and whitespace differences do not cause ownership
// false-negatives. Two semantically equal statusLine values should compare
// equal after normalization.
func normalizeJSON(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return bytes.TrimSpace(b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return bytes.TrimSpace(b)
	}
	return out
}

func modeToUint32(m os.FileMode) uint32 {
	if m == 0 {
		return 0600
	}
	return uint32(m.Perm())
}

func hashBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
