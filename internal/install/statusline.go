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

	"github.com/b-a-m-n/freeinference-companion/internal/state"
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

// Scope controls where the status-line integration is installed.
type InstallScope int

const (
	ScopeUser    InstallScope = iota // ~/.claude/settings.json
	ScopeProject                     // <project>/.claude/settings.json
	ScopeLocal                       // <project>/.claude/settings.local.json
)

func (s InstallScope) String() string {
	switch s {
	case ScopeUser:
		return "user"
	case ScopeProject:
		return "project"
	case ScopeLocal:
		return "local"
	default:
		return "unknown"
	}
}

func claudeDir(home string) string { return filepath.Join(home, ".claude") }
func settingsPath(home string) string {
	return filepath.Join(claudeDir(home), "settings.json")
}
func settingsLocalPath(home string) string {
	return filepath.Join(claudeDir(home), "settings.local.json")
}
func wrapperPath(home string) string { return filepath.Join(claudeDir(home), wrapperName) }

// metadataPath returns the path to the installation metadata file for a given
// scope and base directory. Each installation identity gets its own metadata
// file so that installing into multiple scopes (user, project A, project B,
// local) does not overwrite each other's ownership and rollback information.
func metadataPath(scope InstallScope, base string) string {
	name := fmt.Sprintf("claude-statusline-%s.json", scope)
	switch scope {
	case ScopeUser:
		return filepath.Join(base, ".config", "freeinference-companion", "installations", name)
	default:
		return filepath.Join(base, ".claude", ".freeinference-install-"+name)
	}
}

// settingsPathForScope returns the settings file path for the given scope and
// base directory (home for user scope, project root for project/local).
func settingsPathForScope(scope InstallScope, base string) string {
	switch scope {
	case ScopeUser:
		return settingsPath(base)
	case ScopeProject:
		return filepath.Join(base, ".claude", "settings.json")
	case ScopeLocal:
		return filepath.Join(base, ".claude", "settings.local.json")
	default:
		return settingsPath(base)
	}
}

// wrapperPathForScope returns the wrapper script path for the given scope.
// Project/local installs use the project's .claude/ directory.
func wrapperPathForScope(scope InstallScope, base string) string {
	switch scope {
	case ScopeUser:
		return wrapperPath(base)
	case ScopeProject, ScopeLocal:
		return filepath.Join(base, ".claude", wrapperName)
	default:
		return wrapperPath(base)
	}
}

// StatusLineStatus describes the status-line integration at one explicit
// installation scope. It is derived from the same scope resolver used by
// installation and removal, so status never inspects a different target.
type StatusLineStatus struct {
	Scope        string `json:"scope"`
	SettingsPath string `json:"settings_path"`
	Wrapper      string `json:"wrapper"`
	Installed    bool   `json:"installed"`
	Executable   bool   `json:"executable"`
	Referenced   bool   `json:"referenced"`
	Status       string `json:"status"`
}

// InspectClaudeStatusLine reports whether the exact wrapper for scope is
// executable and is the configured statusLine command. No substring matching
// is used because a similarly named user command is not our installation.
func InspectClaudeStatusLine(home string, scope InstallScope, projectRoot string) (StatusLineStatus, error) {
	if scope == ScopeUser {
		projectRoot = home
	}
	settingsFile := settingsPathForScope(scope, projectRoot)
	wrapper := wrapperPathForScope(scope, projectRoot)
	result := StatusLineStatus{
		Scope:        scope.String(),
		SettingsPath: settingsFile,
		Wrapper:      wrapper,
		Status:       "not_installed",
	}

	if info, err := os.Stat(wrapper); err == nil {
		result.Installed = true
		result.Executable = info.Mode()&0111 != 0
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("stat wrapper: %w", err)
	}

	settings, _, _, err := readSettingsAndMode(settingsFile)
	if err != nil {
		return result, fmt.Errorf("read settings: %w", err)
	}
	if raw, ok := settings["statusLine"]; ok {
		if command, ok := commandPathFromStatusLine(raw); ok {
			result.Referenced = samePath(command, wrapper)
		}
	}

	switch {
	case !result.Installed:
		result.Status = "not_installed"
	case !result.Executable:
		result.Status = "installed_not_executable"
	case !result.Referenced:
		result.Status = "installed_not_referenced"
	default:
		result.Status = "installed"
	}
	return result, nil
}

// installerLockPath returns the path to the advisory lock serializing
// install/uninstall operations so two concurrent installer processes cannot
// race across wrapper, settings, and metadata.
func installerLockPath(home string) string {
	return filepath.Join(home, ".config", "freeinference-companion", "installations", "installer.lock")
}

// ErrInstallerLocked is returned when another install/uninstall operation is
// already in progress.
var ErrInstallerLocked = errors.New("another install/uninstall operation is in progress")

// withInstallerLock runs fn while holding the exclusive installer lock at lockPath.
// Returns ErrInstallerLocked if the lock cannot be acquired.
func withInstallerLock(lockPath string, fn func() error) error {
	lockDir := filepath.Dir(lockPath)
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return fmt.Errorf("create lock directory: %w", err)
	}
	lock := state.NewFileLock(lockPath)
	if err := lock.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			return ErrInstallerLocked
		}
		return fmt.Errorf("acquire installer lock: %w", err)
	}
	defer lock.Release()
	return fn()
}

// InstallClaudeStatusLine installs the composed status line.
// binaryPath is the resolved freeinference binary to embed in the wrapper; when empty,
// the wrapper falls back to resolving `freeinference` from PATH at runtime.
// scope controls installation target (user/project/local).
// projectRoot is the project directory for project/local scopes.
//
// This function is transactional: any failure after the first mutation rolls
// all touched files back to their prior on-disk state. Rollback is best-effort;
// perfect atomicity across a crash mid-rename is not guaranteed without a
// durable transaction journal.
//
// On reinstall, the current settings["statusLine"] is compared with the
// recorded OwnedStatusLine. If the user changed it, installation is refused
// with ErrDriftedStatusLine.
func InstallClaudeStatusLine(home, binaryPath string, scope InstallScope, projectRoot string, stdout io.Writer) error {
	if scope == ScopeUser {
		projectRoot = home
	}
	lockPath := installerLockPathForScope(home, scope, projectRoot)
	err := withInstallerLock(lockPath, func() error {
		return installClaudeStatusLineLocked(home, scope, projectRoot, binaryPath, stdout)
	})
	return err
}

// installerLockPathForScope returns the lock file path for the given scope.
func installerLockPathForScope(base string, scope InstallScope, projectRoot string) string {
	switch scope {
	case ScopeUser:
		return installerLockPath(base)
	default:
		return filepath.Join(projectRoot, ".claude", "freeinference-statusline.lock")
	}
}

// installClaudeStatusLineLocked holds the installer lock and performs the
// transactional installation.
func installClaudeStatusLineLocked(home string, scope InstallScope, projectRoot, binaryPath string, stdout io.Writer) error {
	settingsFile := settingsPathForScope(scope, projectRoot)
	wrapper := wrapperPathForScope(scope, projectRoot)

	// Determine the base directory for metadata storage. User-scope metadata
	// lives under ~/.config/freeinference-companion/installations/; project/local
	// metadata lives in .claude/ under the project root.
	metaBase := home
	if scope != ScopeUser {
		metaBase = projectRoot
	}
	metaFile := metadataPath(scope, metaBase)

	settings, originalBytes, originalMode, err := readSettingsAndMode(settingsFile)
	if err != nil {
		return fmt.Errorf("settings file is malformed; refusing to modify it: %w", err)
	}

	// Load any existing metadata first. Reinstall must NOT collapse the prior
	// history pointer — the pre-install value recorded on the first install is
	// authoritative across all subsequent reinstalls.
	existingMeta, haveExistingMeta, metaErr := loadMetadata(metaFile)
	if metaErr != nil {
		return fmt.Errorf("installation metadata is corrupted; refusing to modify. Repair: back up and delete %s, then reinstall: %w", metaFile, metaErr)
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
		preInstallStatusLine, hadPrevious, err = extractPreInstall(settings, wrapper)
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
			return fmt.Errorf("%w: the current statusLine does not match our recorded value; reconcile manually (edit settings.json to remove or adjust the statusLine key) and then reinstall", ErrDriftedStatusLine)
		}
	}

	// Build the new wrapper. It composes with the pre-install value (not the
	// current settings["statusLine"]) so reinstalls stay stable.
	script := buildWrapper(binaryPath, preInstallStatusLine)

	// Read the prior wrapper bytes (if any) so we can roll it back on a later
	// failure.
	priorWrapper, priorWrapperMode, _ := readFileAndMode(wrapper)

	// Capture prior metadata bytes for rollback too.
	priorMeta, priorMetaMode, _ := readFileAndMode(metaFile)

	// ---- Mutation phase begins. Anything that fails here must roll back. ----

	if err := os.MkdirAll(filepath.Dir(wrapper), 0755); err != nil {
		return err
	}
	// Atomic wrapper write: temp file + explicit mode + atomic rename. A
	// concurrent Claude process must never observe a truncated wrapper script.
	if err := writeFileAtomic(wrapper, []byte(script), 0755); err != nil {
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

	fmt.Fprintf(stdout, "Status line installed (scope: %s).\n  Wrapper: %s\n  Config:  %s\n", scope, wrapper, settingsFile)
	if hadPrevious {
		fmt.Fprintln(stdout, "  Composed with your existing status line (both run).")
	}
	switch scope {
	case ScopeUser:
		fmt.Fprintln(stdout, "  This wrapper runs in every Claude Code project for this user.")
		fmt.Fprintln(stdout, "  (Remains invisible unless a FreeInference endpoint and key are active.)")
	case ScopeProject:
		fmt.Fprintln(stdout, "  This wrapper runs only in this project.")
	case ScopeLocal:
		fmt.Fprintln(stdout, "  This wrapper runs only in this project (local-only, git-ignored).")
	}
	fmt.Fprintln(stdout, "Restart Claude Code to see the FreeInference status line.")
	return nil
}

// UninstallClaudeStatusLine restores the previous statusLine value (or
// removes the key) without touching the rest of the settings file.
// scope and projectRoot must match the original installation.
//
// Ownership-aware: if the current statusLine does NOT match what we own
// (the user changed it after install), we refuse to delete or replace it.
// The user must reconcile manually — the printed instructions tell them how.
func UninstallClaudeStatusLine(home string, scope InstallScope, projectRoot string, stdout io.Writer) error {
	if scope == ScopeUser {
		projectRoot = home
	}
	lockPath := installerLockPathForScope(home, scope, projectRoot)
	return withInstallerLock(lockPath, func() error {
		return uninstallClaudeStatusLineLocked(home, scope, projectRoot, stdout)
	})
}

// uninstallClaudeStatusLineLocked holds the installer lock and performs the
// ownership-aware uninstallation.
func uninstallClaudeStatusLineLocked(home string, scope InstallScope, projectRoot string, stdout io.Writer) error {
	settingsFile := settingsPathForScope(scope, projectRoot)
	wrapper := wrapperPathForScope(scope, projectRoot)
	metaBase := home
	if scope != ScopeUser {
		metaBase = projectRoot
	}
	metaFile := metadataPath(scope, metaBase)

	settings, _, mode, err := readSettingsAndMode(settingsFile)
	if err != nil {
		fmt.Fprintln(stdout, "Your Claude settings file is malformed; refusing to modify anything.")
		fmt.Fprintf(stdout, "  - Repair %s (it could not be parsed as JSON).\n", settingsFile)
		fmt.Fprintf(stdout, "  - Then re-run `freeinference status-line uninstall`.\n")
		return fmt.Errorf("settings file is malformed; refusing to modify anything: %w", err)
	}

	meta, haveMeta, metaErr := loadMetadata(metaFile)
	if metaErr != nil {
		return fmt.Errorf("installation metadata is corrupted; refusing to modify. Repair: back up and delete %s, then reinstall: %w", metaFile, metaErr)
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
			// Retain metadata: it is the authoritative record of the original
			// status line and ownership state. Deleting it would destroy the
			// user's ability to reconcile. They remove it manually once settings
			// are reconciled.
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

	if err := removeIfExists(wrapper); err != nil {
		return fmt.Errorf("remove wrapper: %w", err)
	}
	if err := removeIfExists(metaFile); err != nil {
		return fmt.Errorf("remove metadata: %w", err)
	}

	fmt.Fprintln(stdout, "Status line uninstalled.")
	return nil
}

// removeIfExists removes path if it exists. Reports an error only if removal
// fails for a reason other than "not found".
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// extractPreInstall inspects settings["statusLine"] to determine whether it
// can be composed with our wrapper. Returns the prior value (if any) and a
// flag indicating whether a prior value exists. Returns ErrComplexStatusLine
// when the existing value cannot be composed safely — in which case the
// caller must refuse installation without modifying anything.
func extractPreInstall(settings map[string]any, wrapper string) (json.RawMessage, bool, error) {
	raw, ok := settings["statusLine"]
	if !ok {
		return nil, false, nil
	}
	// If it already points at our wrapper, treat it as a no-op prior state
	// (this branch is only taken on fresh installs without existing metadata,
	// so finding our wrapper here is unusual but should not panic).
	// Ownership is by exact command-path equality, never substring matching.
	if cmd, ok := commandPathFromStatusLine(raw); ok && samePath(cmd, wrapper) {
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
	rawBytes, _ := json.Marshal(raw)
	return rawBytes, true, nil
}

// buildWrapper renders the status-line wrapper. It reads stdin once and
// replays it to both the previous status command (if any) and freeinference.
//
// P0-3: when freeinference produces no output (inactive or ineligible), the wrapper
// must not emit a blank line — the user's existing status line runs
// standalone. Only emit the previous output when freeinference actually produced
// something, so we never add an observable footer when FreeInference is
// inactive.
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
	b.WriteString("# Generated by: freeinference status-line install\n")
	b.WriteString("set -u\n")
	b.WriteString("input=\"$(cat)\"\n\n")

	// freeinference side: prefer the recorded binary path, fall back to PATH.
	b.WriteString("fi_out=\"\"\n")
	if binaryPath != "" {
		fmt.Fprintf(&b, "if [[ -x %s ]]; then\n", shellQuote(binaryPath))
		fmt.Fprintf(&b, "  fi_out=\"$(printf '%%s' \"$input\" | %s status --compact --color=always --client claude-code 2>/dev/null || true)\"\n", shellQuote(binaryPath))
		b.WriteString("elif command -v freeinference >/dev/null 2>&1; then\n")
		b.WriteString("  fi_out=\"$(printf '%s' \"$input\" | freeinference status --compact --color=always --client claude-code 2>/dev/null || true)\"\n")
		b.WriteString("elif [[ -x \"$HOME/.local/bin/freeinference\" ]]; then\n")
		b.WriteString("  fi_out=\"$(printf '%s' \"$input\" | \"$HOME/.local/bin/freeinference\" status --compact --color=always --client claude-code 2>/dev/null || true)\"\n")
		b.WriteString("fi\n")
	} else {
		b.WriteString("if command -v freeinference >/dev/null 2>&1; then\n")
		b.WriteString("  fi_out=\"$(printf '%s' \"$input\" | freeinference status --compact --color=always --client claude-code 2>/dev/null || true)\"\n")
		b.WriteString("elif [[ -x \"$HOME/.local/bin/freeinference\" ]]; then\n")
		b.WriteString("  fi_out=\"$(printf '%s' \"$input\" | \"$HOME/.local/bin/freeinference\" status --compact --color=always --client claude-code 2>/dev/null || true)\"\n")
		b.WriteString("fi\n")
	}

	// Previous status line side.
	if prevCmd != "" {
		b.WriteString("\nprev_out=\"\"\n")
		fmt.Fprintf(&b, "prev_out=\"$(printf '%%s' \"$input\" | %s 2>/dev/null || true)\"\n", shellQuote(prevCmd))
		b.WriteString("\nif [[ -n \"$fi_out\" && -n \"$prev_out\" ]]; then\n")
		b.WriteString("  printf '%s | %s\\n' \"$prev_out\" \"$fi_out\"\n")
		b.WriteString("elif [[ -n \"$fi_out\" ]]; then\n")
		b.WriteString("  printf '%s\\n' \"$fi_out\"\n")
		b.WriteString("elif [[ -n \"$prev_out\" ]]; then\n")
		b.WriteString("  printf '%s\\n' \"$prev_out\"\n")
		b.WriteString("fi\n")
	} else {
		// P0-3: no fallback printf '\\n' — when freeinference produces nothing,
		// output zero bytes so we do not add an observable footer.
		b.WriteString("\nif [[ -n \"$fi_out\" ]]; then\n")
		b.WriteString("  printf '%s\\n' \"$fi_out\"\n")
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

// writeFileAtomic writes data to path via a temp file, explicit mode, and
// atomic rename. The final file is forced to the given mode. This guarantees
// a concurrent reader never observes a truncated or partially-written file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0755
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wrapper-*")
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
// Returns (zero, false, nil) for missing file, (zero, false, err) for corrupt
// file or a read error other than "not found". The error distinguishes
// "no metadata" from "metadata present but unreadable" so callers refuse to
// install/uninstall when metadata exists but cannot be read — treating a
// permission-denied or I/O error as "absent" would silently destroy rollback
// history.
func loadMetadata(path string) (Metadata, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Metadata{}, false, nil
		}
		// Permission denied, I/O error, directory in place of file, etc.
		// Metadata may exist; we must not pretend it is absent.
		return Metadata{}, false, fmt.Errorf("read metadata: %w", err)
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, false, err
	}
	return m, true, nil
}

// commandPathFromStatusLine extracts the normalized command path from a
// statusLine value if it is a command object. Returns ("", false) otherwise.
func commandPathFromStatusLine(raw any) (string, bool) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	if t, _ := obj["type"].(string); t != "command" {
		return "", false
	}
	cmd, _ := obj["command"].(string)
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

// samePath reports whether two file paths refer to the same location, after
// cleaning. Returns false if either path is empty or cannot be evaluated.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	// Resolve symlinks where possible for an exact comparison.
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

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
