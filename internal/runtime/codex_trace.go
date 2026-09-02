package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/state"
)

const codexTraceHeaderEnv = "FI_TRACE_SESSION_ID"

// CodexTraceMapping describes whether the selected provider's documented
// env_http_headers mapping can carry the Companion trace ID.
type CodexTraceMapping struct {
	Ready    bool
	Existing bool
	Modified bool
}

// InspectCodexTraceHeader reports mapping state without changing the Codex
// config. It is used by doctor and deliberately returns no header value.
func InspectCodexTraceHeader(path, providerID string) (configured, conflict bool, err error) {
	if !validCodexName(providerID) {
		return false, false, errors.New("invalid Codex provider name")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false, false, err
	}
	if len(body) > maxCodexConfigBytes {
		return false, false, errors.New("Codex config exceeds the supported size limit")
	}
	providerTable := "model_providers." + providerID
	nestedTable := providerTable + ".env_http_headers"
	providerTableQuoted := `model_providers."` + providerID + `"`
	nestedTableQuoted := providerTableQuoted + ".env_http_headers"
	table := ""
	for _, raw := range strings.Split(string(body), "\n") {
		line := tomlLine(raw)
		if isTomlTable(line) {
			table = tableName(line)
			continue
		}
		key, value, ok := cutTomlAssignment(line)
		if !ok {
			continue
		}
		if (table == nestedTable || table == nestedTableQuoted) && strings.EqualFold(strings.Trim(key, " \""), canonicalTraceHeader) {
			parsed, parsedOK := parseTomlString(strings.TrimSpace(value))
			return parsedOK && parsed == codexTraceHeaderEnv, parsedOK && parsed != codexTraceHeaderEnv, nil
		}
		if (table == providerTable || table == providerTableQuoted) && key == "env_http_headers" && strings.HasPrefix(strings.TrimSpace(value), "{") {
			mapping, valid := parseInlineHeaderMap(strings.TrimSpace(value))
			if !valid {
				return false, false, errors.New("Codex env_http_headers table is malformed")
			}
			if mapped, exists := mappingValue(mapping, canonicalTraceHeader); exists {
				return mapped == codexTraceHeaderEnv, mapped != codexTraceHeaderEnv, nil
			}
			return false, false, nil
		}
	}
	return false, false, nil
}

// EnsureCodexTraceHeader adds the narrow mapping required by Codex when it is
// absent. It preserves comments and unrelated TOML text and never replaces an
// existing X-Session-ID mapping. Unsupported/ambiguous forms fail open to the
// caller, which should launch Codex without Companion trace injection.
func EnsureCodexTraceHeader(path, providerID string) (CodexTraceMapping, error) {
	if !validCodexName(providerID) {
		return CodexTraceMapping{}, errors.New("invalid Codex provider name")
	}
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	defer lock.Release()
	info, err := os.Lstat(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return CodexTraceMapping{}, errors.New("Codex config is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if len(body) > maxCodexConfigBytes {
		return CodexTraceMapping{}, errors.New("Codex config exceeds the supported size limit")
	}
	contents := string(body)
	providerTable := "model_providers." + providerID
	nestedTable := providerTable + ".env_http_headers"
	providerTableQuoted := `model_providers."` + providerID + `"`
	nestedTableQuoted := providerTableQuoted + ".env_http_headers"
	lines := strings.SplitAfter(contents, "\n")

	// Inline env_http_headers tables cannot safely be converted to a nested
	// table. They can still be merged when the one-line inline table is valid.
	for i, raw := range lines {
		line := tomlLine(raw)
		if tableName(line) != providerTable && tableName(line) != providerTableQuoted {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			candidate := tomlLine(lines[j])
			if isTomlTable(candidate) {
				break
			}
			key, value, ok := cutTomlAssignment(candidate)
			if !ok || key != "env_http_headers" {
				continue
			}
			if !strings.HasPrefix(strings.TrimSpace(value), "{") {
				return CodexTraceMapping{}, errors.New("Codex env_http_headers format is unsupported")
			}
			mapping, valid := parseInlineHeaderMap(strings.TrimSpace(value))
			if !valid {
				return CodexTraceMapping{}, errors.New("Codex env_http_headers table is malformed")
			}
			if value, exists := mappingValue(mapping, canonicalTraceHeader); exists {
				if value == codexTraceHeaderEnv {
					return CodexTraceMapping{Ready: true, Existing: true}, nil
				}
				return CodexTraceMapping{Existing: true}, errors.New("Codex X-Session-ID mapping already points elsewhere")
			}
			updated, ok := addInlineHeaderMapping(lines[j])
			if !ok {
				return CodexTraceMapping{}, errors.New("Codex env_http_headers table cannot be merged safely")
			}
			lines[j] = updated
			if err := atomicRewriteCodex(path, strings.Join(lines, ""), info.Mode().Perm()); err != nil {
				return CodexTraceMapping{}, err
			}
			return CodexTraceMapping{Ready: true, Modified: true}, nil
		}
	}

	// A nested env_http_headers table is the documented and safest form.
	for i, raw := range lines {
		line := tomlLine(raw)
		if tableName(line) != nestedTable && tableName(line) != nestedTableQuoted {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			candidate := tomlLine(lines[j])
			if isTomlTable(candidate) {
				end = j
				break
			}
			key, value, ok := cutTomlAssignment(candidate)
			if ok && strings.EqualFold(strings.Trim(key, " \""), canonicalTraceHeader) {
				parsed, parsedOK := parseTomlString(strings.TrimSpace(value))
				if parsedOK && parsed == codexTraceHeaderEnv {
					return CodexTraceMapping{Ready: true, Existing: true}, nil
				}
				return CodexTraceMapping{Existing: true}, errors.New("Codex X-Session-ID mapping already points elsewhere")
			}
		}
		insert := "\"" + canonicalTraceHeader + "\" = \"" + codexTraceHeaderEnv + "\"" + tomlLineEnding(contents)
		lines = append(lines[:end], append([]string{insert}, lines[end:]...)...)
		if err := atomicRewriteCodex(path, strings.Join(lines, ""), info.Mode().Perm()); err != nil {
			return CodexTraceMapping{}, err
		}
		return CodexTraceMapping{Ready: true, Modified: true}, nil
	}

	// No mapping exists. Appending a new nested table is valid as long as the
	// provider did not already define an inline env_http_headers value.
	if !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	lineEnding := tomlLineEnding(contents)
	contents += lineEnding + "[" + nestedTable + "]" + lineEnding + "\"" + canonicalTraceHeader + "\" = \"" + codexTraceHeaderEnv + "\"" + lineEnding
	if err := atomicRewriteCodex(path, contents, info.Mode().Perm()); err != nil {
		return CodexTraceMapping{}, err
	}
	return CodexTraceMapping{Ready: true, Modified: true}, nil
}

const codexTraceBackupSuffix = ".freeinference-trace.backup"

// BackupCodexTraceConfig makes a one-time, mode-preserving backup before the
// explicit trace setup command mutates Codex configuration. Existing backups
// are never overwritten, so uninstall remains reversible to the first state.
func BackupCodexTraceConfig(path string) error {
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("Codex config is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(body) > maxCodexConfigBytes {
		return errors.New("Codex config exceeds the supported size limit")
	}
	backup := path + codexTraceBackupSuffix
	if existing, statErr := os.Lstat(backup); statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			return errors.New("Codex trace backup is not a regular file")
		}
		return nil
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(backup), ".freeinference-trace-backup-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, backup)
}

// RestoreCodexTraceConfig restores the backup made by explicit setup. It
// refuses to overwrite a user conflict and removes the backup only after a
// successful atomic restore.
func RestoreCodexTraceConfig(path, providerID string) error {
	if !validCodexName(providerID) {
		return errors.New("invalid Codex provider name")
	}
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()
	configured, conflict, err := InspectCodexTraceHeader(path, providerID)
	if err != nil {
		return err
	}
	if conflict {
		return errors.New("Codex X-Session-ID mapping changed; refusing to restore over user changes")
	}
	if !configured {
		return errors.New("FreeInference Codex trace mapping is not installed")
	}
	backup := path + codexTraceBackupSuffix
	backupInfo, err := os.Lstat(backup)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("no FreeInference Codex trace backup exists")
		}
		return err
	}
	if backupInfo.Mode()&os.ModeSymlink != 0 || !backupInfo.Mode().IsRegular() {
		return errors.New("Codex trace backup is not a regular file")
	}
	body, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	if len(body) > maxCodexConfigBytes {
		return errors.New("Codex trace backup exceeds the supported size limit")
	}
	mode := backupInfo.Mode().Perm()
	if err := atomicRewriteCodex(path, string(body), mode); err != nil {
		return err
	}
	return os.Remove(backup)
}

func acquireCodexTraceLock(path string) (*state.FileLock, error) {
	lock := state.NewFileLock(path + ".freeinference-trace.lock")
	if err := lock.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			return nil, errors.New("another Codex trace setup operation is in progress")
		}
		return nil, err
	}
	return lock, nil
}

const canonicalTraceHeader = "X-Session-ID"

func isTomlTable(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

func tomlLine(raw string) string {
	raw = strings.TrimSuffix(raw, "\n")
	raw = strings.TrimSuffix(raw, "\r")
	return strings.TrimSpace(stripTomlComment(raw))
}

func tomlLineEnding(contents string) string {
	if strings.Contains(contents, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func tableName(line string) string {
	if !isTomlTable(line) {
		return ""
	}
	return strings.TrimSpace(line[1 : len(line)-1])
}

func cutTomlAssignment(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(key), strings.TrimSpace(value), true
}

func parseInlineHeaderMap(value string) (map[string]string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return nil, false
	}
	body := strings.TrimSpace(value[1 : len(value)-1])
	result := make(map[string]string)
	if body == "" {
		return result, true
	}
	for _, part := range splitInlineTable(body) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, false
		}
		key = strings.Trim(strings.TrimSpace(key), "\"")
		parsed, parsedOK := parseTomlString(strings.TrimSpace(value))
		if key == "" || !parsedOK {
			return nil, false
		}
		result[key] = parsed
	}
	return result, true
}

func splitInlineTable(value string) []string {
	var result []string
	start := 0
	quoted := false
	escaped := false
	for i, r := range value {
		if r == '"' && !escaped {
			quoted = !quoted
		}
		if r == ',' && !quoted {
			result = append(result, value[start:i])
			start = i + 1
		}
		escaped = r == '\\' && !escaped
		if r != '\\' {
			escaped = false
		}
	}
	result = append(result, value[start:])
	return result
}

func addInlineHeaderMapping(raw string) (string, bool) {
	newline := ""
	if strings.HasSuffix(raw, "\n") {
		newline = "\n"
		raw = strings.TrimSuffix(raw, "\n")
	}
	comment := ""
	withoutComment := stripTomlComment(raw)
	if len(withoutComment) < len(raw) {
		comment = raw[len(withoutComment):]
		raw = withoutComment
	}
	close := strings.LastIndex(raw, "}")
	open := strings.Index(raw, "{")
	if close < 0 || open < 0 || open > close {
		return "", false
	}
	inside := strings.TrimSpace(raw[open+1 : close])
	separator := ""
	if inside != "" {
		separator = ", "
	}
	return raw[:close] + separator + "\"" + canonicalTraceHeader + "\" = \"" + codexTraceHeaderEnv + "\"" + raw[close:] + comment + newline, true
}

func mappingValue(mapping map[string]string, wanted string) (string, bool) {
	for key, value := range mapping {
		if strings.EqualFold(key, wanted) {
			return value, true
		}
	}
	return "", false
}

func atomicRewriteCodex(path, contents string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".freeinference-trace-*")
	if err != nil {
		return fmt.Errorf("create Codex config temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode & 0777); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace Codex config: %w", err)
	}
	return nil
}
