package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	codexTUIConfigMaxBytes = 1 << 20
	codexTUIItemMaxLen     = 128
	codexTUIItemMaxCount   = 64
)

var ErrDriftedCodexTUI = errors.New("codex tui.status_line was changed after installation; refusing to overwrite the user's customization")

// CodexTUIStatus describes the native Codex footer configuration. It is not
// a FreeInference telemetry surface: the values are rendered by Codex itself.
type CodexTUIStatus struct {
	ConfigPath string   `json:"config_path"`
	Configured bool     `json:"configured"`
	Installed  bool     `json:"installed"`
	Referenced bool     `json:"referenced"`
	StatusLine []string `json:"status_line,omitempty"`
	Status     string   `json:"status"`
}

type codexTUIMetadata struct {
	InstalledAt             time.Time `json:"installed_at"`
	ConfigPath              string    `json:"config_path"`
	HadPrevious             bool      `json:"had_previous"`
	PreviousItems           []string  `json:"previous_items,omitempty"`
	PreviousLine            string    `json:"previous_line,omitempty"`
	OwnedItems              []string  `json:"owned_items"`
	OriginalTrailingNewline bool      `json:"original_trailing_newline"`
	OriginalTrailingLineEnd string    `json:"original_trailing_line_end,omitempty"`
	CreatedTUITable         bool      `json:"created_tui_table,omitempty"`
}

func codexTUIMetadataPath(home, configPath string) (string, error) {
	canonical, err := canonicalCodexConfigPath(configPath)
	if err != nil {
		return "", err
	}
	return codexTUIMetadataPathForPath(home, canonical), nil
}

func codexTUIMetadataPathForPath(home, canonicalConfigPath string) string {
	digest := sha256.Sum256([]byte(canonicalConfigPath))
	name := hex.EncodeToString(digest[:])
	return filepath.Join(home, ".config", "freeinference-companion", "installations", "codex-tui", name+".json")
}

func legacyCodexTUIMetadataPath(home string) string {
	return filepath.Join(home, ".config", "freeinference-companion", "installations", "codex-tui.json")
}

// migrateLegacyCodexTUIMetadata moves the pre-multi-environment singleton
// record only when it names this exact configuration root. This preserves
// restoration for existing installs without allowing one root to adopt
// another root's ownership.
func migrateLegacyCodexTUIMetadata(home, canonicalConfigPath string) error {
	legacyPath := legacyCodexTUIMetadataPath(home)
	meta, found, err := loadCodexTUIMetadata(legacyPath)
	if err != nil || !found {
		return err
	}
	recorded, pathErr := canonicalCodexConfigPath(meta.ConfigPath)
	if pathErr != nil || recorded != canonicalConfigPath {
		return nil
	}
	scopedPath := codexTUIMetadataPathForPath(home, canonicalConfigPath)
	if err := os.MkdirAll(filepath.Dir(scopedPath), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(scopedPath, data, 0600); err != nil {
		return err
	}
	if err := os.Remove(legacyPath); err != nil {
		return err
	}
	return nil
}

func codexTUILockPath(home, configPath string) (string, error) {
	metadataPath, err := codexTUIMetadataPath(home, configPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(metadataPath, ".json") + ".lock", nil
}

func canonicalCodexConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("codex config path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve codex config path: %w", err)
	}
	return filepath.Clean(abs), nil
}

// InstallCodexTUI configures Codex's own footer with model/reasoning,
// remaining-context, and current-directory items. It preserves all existing
// items, writes atomically, and records ownership so uninstall cannot remove
// a value the user changed later.
func InstallCodexTUI(home, configPath string, stdout io.Writer) error {
	var err error
	configPath, err = canonicalCodexConfigPath(configPath)
	if err != nil {
		return err
	}
	lockPath, err := codexTUILockPath(home, configPath)
	if err != nil {
		return err
	}
	return withInstallerLock(lockPath, func() error {
		return installCodexTUILocked(home, configPath, stdout)
	})
}

func installCodexTUILocked(home, configPath string, stdout io.Writer) error {
	contents, mode, err := readCodexTUIConfig(configPath)
	if err != nil {
		return err
	}
	current, found, err := parseCodexTUIStatusLine(contents)
	if err != nil {
		return err
	}
	previousLine, _ := codexTUIStatusLineLine(contents)
	originalTrailingNewline := strings.HasSuffix(contents, "\n")
	originalTrailingLineEnd := trailingLineEnding(contents)
	_, hadTUITable := findCodexTUITable(contents)
	createdTUITable := !hadTUITable

	metaPath, err := codexTUIMetadataPath(home, configPath)
	if err != nil {
		return err
	}
	if err := migrateLegacyCodexTUIMetadata(home, configPath); err != nil {
		return fmt.Errorf("migrate codex footer metadata: %w", err)
	}
	existing, haveExisting, err := loadCodexTUIMetadata(metaPath)
	if err != nil {
		return fmt.Errorf("read codex footer metadata: %w", err)
	}
	if haveExisting {
		recordedPath, pathErr := canonicalCodexConfigPath(existing.ConfigPath)
		if pathErr != nil || recordedPath != configPath {
			return errors.New("codex footer metadata belongs to a different configuration")
		}
		if !found || !sameStrings(current, existing.OwnedItems) {
			return ErrDriftedCodexTUI
		}
	}

	previous := append([]string(nil), current...)
	hadPrevious := found
	if haveExisting {
		previous = append([]string(nil), existing.PreviousItems...)
		if len(previous) == 0 {
			previous = nil
		}
		hadPrevious = existing.HadPrevious
		previousLine = existing.PreviousLine
		originalTrailingNewline = existing.OriginalTrailingNewline
		if existing.OriginalTrailingLineEnd != "" {
			originalTrailingLineEnd = existing.OriginalTrailingLineEnd
		}
		createdTUITable = existing.CreatedTUITable
	}
	if haveExisting && !sameStrings(previous, current) {
		contents, err = setCodexTUIStatusLine(contents, previous)
		if err != nil {
			return err
		}
		current, _, err = parseCodexTUIStatusLine(contents)
		if err != nil {
			return err
		}
	}
	owned := appendUniqueCodexTUIItems(current, "model-with-reasoning", "context-remaining", "current-dir")
	newContents, err := setCodexTUIStatusLine(contents, owned)
	if err != nil {
		return err
	}

	priorBytes, priorMode, priorErr := readFileAndMode(configPath)
	if priorErr != nil && !os.IsNotExist(priorErr) {
		return fmt.Errorf("read codex config for rollback: %w", priorErr)
	}
	if priorMode == 0 {
		priorMode = mode
	}
	if err := writeFileAtomic(configPath, []byte(newContents), mode); err != nil {
		return fmt.Errorf("write codex config: %w", err)
	}

	meta := &codexTUIMetadata{
		InstalledAt:             time.Now().UTC(),
		ConfigPath:              configPath,
		HadPrevious:             hadPrevious,
		PreviousItems:           previous,
		PreviousLine:            previousLine,
		OwnedItems:              owned,
		OriginalTrailingNewline: originalTrailingNewline,
		OriginalTrailingLineEnd: originalTrailingLineEnd,
		CreatedTUITable:         createdTUITable,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		_ = rollbackCodexTUIConfig(configPath, priorBytes, priorMode, priorErr)
		return fmt.Errorf("encode codex footer metadata: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0700); err != nil {
		_ = rollbackCodexTUIConfig(configPath, priorBytes, priorMode, priorErr)
		return fmt.Errorf("create codex footer metadata directory: %w", err)
	}
	if err := writeFileAtomic(metaPath, append(metaBytes, '\n'), 0600); err != nil {
		_ = rollbackCodexTUIConfig(configPath, priorBytes, priorMode, priorErr)
		return fmt.Errorf("write codex footer metadata: %w", err)
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Configured Codex native footer in %s.\n", configPath)
		fmt.Fprintln(stdout, "Codex will render model, remaining context, and current directory itself.")
	}
	return nil
}

// UninstallCodexTUI restores the pre-install footer when the exact owned value
// is still present. User edits are never overwritten automatically.
func UninstallCodexTUI(home, configPath string, stdout io.Writer) error {
	var err error
	configPath, err = canonicalCodexConfigPath(configPath)
	if err != nil {
		return err
	}
	lockPath, err := codexTUILockPath(home, configPath)
	if err != nil {
		return err
	}
	return withInstallerLock(lockPath, func() error {
		metaPath, metaPathErr := codexTUIMetadataPath(home, configPath)
		if metaPathErr != nil {
			return metaPathErr
		}
		meta, found, metaErr := loadCodexTUIMetadata(metaPath)
		if metaErr != nil {
			return fmt.Errorf("read codex footer metadata: %w", metaErr)
		}
		if !found {
			return fmt.Errorf("codex footer metadata not found: %w", os.ErrNotExist)
		}
		recordedPath, pathErr := canonicalCodexConfigPath(meta.ConfigPath)
		if pathErr != nil || recordedPath != configPath {
			return errors.New("codex footer metadata belongs to a different configuration")
		}
		contents, mode, err := readCodexTUIConfig(configPath)
		if err != nil {
			return err
		}
		current, present, err := parseCodexTUIStatusLine(contents)
		if err != nil {
			return err
		}
		if !present || !sameStrings(current, meta.OwnedItems) {
			return ErrDriftedCodexTUI
		}
		priorBytes, priorMode, priorErr := readFileAndMode(configPath)
		if priorErr != nil {
			return fmt.Errorf("read codex config for rollback: %w", priorErr)
		}
		if priorMode == 0 {
			priorMode = mode
		}
		var restored string
		if meta.HadPrevious {
			if meta.PreviousLine != "" {
				restored, err = restoreCodexTUIStatusLineLine(contents, meta.PreviousLine)
			} else {
				restored, err = setCodexTUIStatusLine(contents, meta.PreviousItems)
			}
		} else if meta.CreatedTUITable {
			withoutStatus, removeErr := removeCodexTUIStatusLine(contents)
			if removeErr != nil {
				err = removeErr
			} else {
				restored, err = removeCreatedCodexTUITable(withoutStatus)
			}
		} else {
			restored, err = removeCodexTUIStatusLine(contents)
		}
		if err != nil {
			return err
		}
		if meta.CreatedTUITable {
			lineEnd := meta.OriginalTrailingLineEnd
			if lineEnd == "" && meta.OriginalTrailingNewline {
				lineEnd = "\n"
			}
			if lineEnd != "\n" && lineEnd != "\r\n" {
				// A newly-created [tui] table is separated from a file ending in
				// no newline or a bare CR by one LF added during installation.
				restored = strings.TrimSuffix(restored, "\n")
			}
		}
		if err := writeFileAtomic(configPath, []byte(restored), mode); err != nil {
			return fmt.Errorf("restore codex config: %w", err)
		}
		if err := os.Remove(metaPath); err != nil {
			_ = rollbackCodexTUIConfig(configPath, priorBytes, priorMode, nil)
			return fmt.Errorf("remove codex footer metadata: %w", err)
		}
		_ = os.Remove(filepath.Dir(metaPath))
		if stdout != nil {
			fmt.Fprintf(stdout, "Restored Codex native footer configuration in %s.\n", configPath)
		}
		return nil
	})
}

// InspectCodexTUI reports native footer state without changing files.
func InspectCodexTUI(home, configPath string) (CodexTUIStatus, error) {
	canonicalPath, err := canonicalCodexConfigPath(configPath)
	if err != nil {
		return CodexTUIStatus{ConfigPath: configPath, Status: "not_configured"}, err
	}
	status := CodexTUIStatus{ConfigPath: canonicalPath, Status: "not_configured"}
	configPath = canonicalPath
	metaPath, err := codexTUIMetadataPath(home, configPath)
	if err != nil {
		return status, err
	}
	contents, _, readErr := readCodexTUIConfig(configPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return status, nil
		}
		return status, readErr
	}
	items, found, err := parseCodexTUIStatusLine(contents)
	if err != nil {
		return status, err
	}
	status.Configured = found
	status.Referenced = found
	status.StatusLine = items
	meta, haveMeta, loadErr := loadCodexTUIMetadata(metaPath)
	if loadErr == nil && !haveMeta {
		legacyMeta, legacyFound, legacyErr := loadCodexTUIMetadata(legacyCodexTUIMetadataPath(home))
		if legacyErr == nil && legacyFound {
			recorded, recordedErr := canonicalCodexConfigPath(legacyMeta.ConfigPath)
			if recordedErr == nil && recorded == status.ConfigPath {
				meta, haveMeta = legacyMeta, true
			}
		}
	}
	if loadErr != nil {
		return status, loadErr
	}
	if !haveMeta {
		if found {
			status.Status = "configured_unmanaged"
		}
		return status, nil
	}
	recordedPath, pathErr := canonicalCodexConfigPath(meta.ConfigPath)
	if pathErr != nil || recordedPath != status.ConfigPath {
		if found {
			status.Status = "configured_unmanaged"
		}
		return status, nil
	}
	status.Installed = true
	if found && sameStrings(items, meta.OwnedItems) {
		status.Status = "installed"
	} else {
		status.Status = "drifted"
	}
	return status, nil
}

func readCodexTUIConfig(path string) (string, os.FileMode, error) {
	info, lstatErr := os.Lstat(path)
	if lstatErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", 0, errors.New("refusing to follow symlink for codex config")
		}
		if !info.Mode().IsRegular() {
			return "", 0, errors.New("codex config is not a regular file")
		}
	} else if !os.IsNotExist(lstatErr) {
		return "", 0, lstatErr
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0600, nil
		}
		return "", 0, fmt.Errorf("read codex config: %w", err)
	}
	if len(data) > codexTUIConfigMaxBytes {
		return "", 0, errors.New("codex config exceeds the supported size limit")
	}
	mode := os.FileMode(0600)
	if lstatErr == nil && info.Mode().Perm() != 0 {
		mode = info.Mode().Perm()
	}
	return string(data), mode, nil
}

func loadCodexTUIMetadata(path string) (*codexTUIMetadata, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, errors.New("codex footer metadata is not a regular file")
	}
	if info.Size() > codexTUIConfigMaxBytes {
		return nil, false, errors.New("codex footer metadata exceeds the supported size limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var meta codexTUIMetadata
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(&meta); err != nil {
		return nil, false, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, false, errors.New("codex footer metadata contains multiple JSON values")
		}
		return nil, false, err
	}
	if meta.ConfigPath == "" || meta.InstalledAt.IsZero() {
		return nil, false, errors.New("codex footer metadata is incomplete")
	}
	if len(meta.OwnedItems) == 0 || len(meta.OwnedItems) > codexTUIItemMaxCount {
		return nil, false, errors.New("codex footer metadata has invalid owned items")
	}
	return &meta, true, nil
}

func parseCodexTUIStatusLine(contents string) ([]string, bool, error) {
	lines := strings.SplitAfter(contents, "\n")
	inTUI := false
	for _, line := range lines {
		plain := strings.TrimRight(line, "\r\n")
		code, _ := splitCodexTOMLComment(plain)
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inTUI = strings.TrimSpace(trimmed[1:len(trimmed)-1]) == "tui"
			continue
		}
		if !inTUI {
			continue
		}
		key, value, ok := strings.Cut(code, "=")
		if !ok || strings.TrimSpace(key) != "status_line" {
			continue
		}
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
			return nil, false, errors.New("codex tui.status_line is not a supported single-line string array")
		}
		items, err := parseCodexTUIItems(value)
		if err != nil {
			return nil, false, fmt.Errorf("parse codex tui.status_line: %w", err)
		}
		if err := validateCodexTUIItems(items); err != nil {
			return nil, false, err
		}
		return items, true, nil
	}
	return nil, false, nil
}

func codexTUIStatusLineLine(contents string) (string, bool) {
	lines := strings.SplitAfter(contents, "\n")
	inTUI := false
	for _, line := range lines {
		plain := strings.TrimRight(line, "\r\n")
		code, _ := splitCodexTOMLComment(plain)
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inTUI = strings.TrimSpace(trimmed[1:len(trimmed)-1]) == "tui"
			continue
		}
		if !inTUI {
			continue
		}
		key, _, ok := strings.Cut(code, "=")
		if ok && strings.TrimSpace(key) == "status_line" {
			return line, true
		}
	}
	return "", false
}

func restoreCodexTUIStatusLineLine(contents, replacement string) (string, error) {
	lines := strings.SplitAfter(contents, "\n")
	inTUI := false
	for i, line := range lines {
		plain := strings.TrimRight(line, "\r\n")
		code, _ := splitCodexTOMLComment(plain)
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inTUI = strings.TrimSpace(trimmed[1:len(trimmed)-1]) == "tui"
			continue
		}
		if !inTUI {
			continue
		}
		key, _, ok := strings.Cut(code, "=")
		if ok && strings.TrimSpace(key) == "status_line" {
			lines[i] = replacement
			return strings.Join(lines, ""), nil
		}
	}
	return "", errors.New("codex tui.status_line disappeared during uninstall")
}

func setCodexTUIStatusLine(contents string, items []string) (string, error) {
	if err := validateCodexTUIItems(items); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	lines := strings.SplitAfter(contents, "\n")
	inTUI := false
	tuiIndex := -1
	insertAt := -1
	for i, line := range lines {
		plain := strings.TrimRight(line, "\r\n")
		code, _ := splitCodexTOMLComment(plain)
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inTUI && insertAt == -1 {
				insertAt = i
			}
			inTUI = strings.TrimSpace(trimmed[1:len(trimmed)-1]) == "tui"
			if inTUI {
				tuiIndex = i
			}
			continue
		}
		if inTUI {
			key, _, ok := strings.Cut(code, "=")
			if ok && strings.TrimSpace(key) == "status_line" {
				lines[i] = replaceCodexTUIValue(line, string(encoded))
				return strings.Join(lines, ""), nil
			}
		}
	}
	if inTUI && insertAt == -1 {
		insertAt = len(lines)
	}
	line := "status_line = " + string(encoded) + "\n"
	if tuiIndex >= 0 {
		if insertAt < 0 {
			insertAt = len(lines)
		}
		// An existing [tui] table may end in an unterminated assignment.
		// Terminate it before inserting status_line.
		if insertAt == len(lines) && insertAt > 0 && lines[insertAt-1] != "" && !strings.HasSuffix(lines[insertAt-1], "\n") {
			lines[insertAt-1] += "\n"
		}
		lines = append(lines, "")
		copy(lines[insertAt+1:], lines[insertAt:])
		lines[insertAt] = line
		return strings.Join(lines, ""), nil
	}
	separator := ""
	if contents != "" && !strings.HasSuffix(contents, "\n") {
		separator = "\n"
	}
	return contents + separator + "[tui]\n" + line, nil
}

func removeCodexTUIStatusLine(contents string) (string, error) {
	lines := strings.SplitAfter(contents, "\n")
	inTUI := false
	for i, line := range lines {
		plain := strings.TrimRight(line, "\r\n")
		code, _ := splitCodexTOMLComment(plain)
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inTUI = strings.TrimSpace(trimmed[1:len(trimmed)-1]) == "tui"
			continue
		}
		if inTUI {
			key, _, ok := strings.Cut(code, "=")
			if ok && strings.TrimSpace(key) == "status_line" {
				lines = append(lines[:i], lines[i+1:]...)
				return strings.Join(lines, ""), nil
			}
		}
	}
	return contents, nil
}

func findCodexTUITable(contents string) (int, bool) {
	lines := strings.SplitAfter(contents, "\n")
	for i, line := range lines {
		plain := strings.TrimRight(line, "\r\n")
		code, _ := splitCodexTOMLComment(plain)
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") &&
			strings.TrimSpace(trimmed[1:len(trimmed)-1]) == "tui" {
			return i, true
		}
	}
	return -1, false
}

func removeCreatedCodexTUITable(contents string) (string, error) {
	lines := strings.SplitAfter(contents, "\n")
	start, found := findCodexTUITable(contents)
	if !found {
		return "", errors.New("codex tui table disappeared during uninstall")
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		plain := strings.TrimRight(lines[i], "\r\n")
		code, _ := splitCodexTOMLComment(plain)
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			end = i
			break
		}
	}
	for _, line := range lines[start+1 : end] {
		plain := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		if plain != "" {
			return "", errors.New("created codex tui table contains unexpected user content")
		}
	}
	return strings.Join(append(lines[:start], lines[end:]...), ""), nil
}

func trailingLineEnding(contents string) string {
	switch {
	case strings.HasSuffix(contents, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(contents, "\n"):
		return "\n"
	case strings.HasSuffix(contents, "\r"):
		return "\r"
	default:
		return ""
	}
}

func parseCodexTUIItems(value string) ([]string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return nil, errors.New("value is not a single-line string array")
	}
	inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if inner == "" {
		return []string{}, nil
	}
	// TOML permits a trailing comma in an inline array; JSON does not.
	if strings.HasSuffix(inner, ",") {
		inner = strings.TrimSpace(strings.TrimSuffix(inner, ","))
	}
	items := make([]string, 0)
	for len(inner) > 0 {
		if inner[0] != '"' {
			return nil, errors.New("array item is not a quoted string")
		}
		end := -1
		for i := 1; i < len(inner); i++ {
			if inner[i] != '"' {
				continue
			}
			backslashes := 0
			for j := i - 1; j >= 0 && inner[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, errors.New("unterminated string array item")
		}
		item, err := strconv.Unquote(inner[:end+1])
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		inner = strings.TrimSpace(inner[end+1:])
		if inner == "" {
			break
		}
		if inner[0] != ',' {
			return nil, errors.New("array items must be comma separated")
		}
		inner = strings.TrimSpace(inner[1:])
		if inner == "" {
			break
		}
	}
	return items, nil
}

func replaceCodexTUIValue(line, encoded string) string {
	newline := ""
	if strings.HasSuffix(line, "\r\n") {
		newline = "\r\n"
	} else if strings.HasSuffix(line, "\n") {
		newline = "\n"
	}
	plain := strings.TrimSuffix(strings.TrimSuffix(line, "\r\n"), "\n")
	code, comment := splitCodexTOMLComment(plain)
	eq := strings.IndexByte(code, '=')
	if eq < 0 {
		return line
	}
	result := code[:eq+1] + " " + encoded
	if comment != "" {
		result += " " + comment
	}
	return result + newline
}

func splitCodexTOMLComment(line string) (string, string) {
	quoted := false
	for i, r := range line {
		switch r {
		case '"':
			backslashes := 0
			for j := i - 1; j >= 0 && line[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				quoted = !quoted
			}
		case '#':
			if !quoted {
				return strings.TrimRight(line[:i], " \t"), strings.TrimSpace(line[i:])
			}
		}
	}
	return line, ""
}

func validateCodexTUIItems(items []string) error {
	if len(items) > codexTUIItemMaxCount {
		return errors.New("codex tui.status_line has too many items")
	}
	for _, item := range items {
		if len(item) == 0 || len(item) > codexTUIItemMaxLen {
			return errors.New("codex tui.status_line contains an invalid item")
		}
		for _, r := range item {
			if r < 0x20 || r > 0x7e {
				return errors.New("codex tui.status_line contains unsafe characters")
			}
		}
	}
	return nil
}

func appendUniqueCodexTUIItems(items []string, required ...string) []string {
	result := append([]string(nil), items...)
	for _, wanted := range required {
		seen := false
		for _, item := range result {
			if item == wanted {
				seen = true
				break
			}
		}
		if !seen {
			result = append(result, wanted)
		}
	}
	return result
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func rollbackCodexTUIConfig(path string, prior []byte, mode os.FileMode, priorErr error) error {
	if os.IsNotExist(priorErr) {
		return os.Remove(path)
	}
	return writeFileAtomic(path, prior, mode)
}
