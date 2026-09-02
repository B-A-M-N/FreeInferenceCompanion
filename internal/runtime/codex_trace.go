package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
)

const (
	codexTraceHeaderEnv  = tracing.TraceSessionEnv
	canonicalTraceHeader = tracing.SessionHeader
)

// CodexTraceMapping describes whether the selected provider's documented
// env_http_headers mapping can carry Companion's bounded request metadata.
type CodexTraceMapping struct {
	Ready     bool
	Existing  bool
	Modified  bool
	Missing   []string
	Conflicts []string
}

// InspectCodexTraceHeaders reports the complete mapping state without
// changing Codex config. It deliberately returns no header values.
func InspectCodexTraceHeaders(path, providerID string) (CodexTraceMapping, error) {
	if !validCodexName(providerID) {
		return CodexTraceMapping{}, errors.New("invalid Codex provider name")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if len(body) > maxCodexConfigBytes {
		return CodexTraceMapping{}, errors.New("Codex config exceeds the supported size limit")
	}
	providerTable := "model_providers." + providerID
	nestedTable := providerTable + ".env_http_headers"
	providerTableQuoted := "model_providers.\"" + providerID + "\""
	nestedTableQuoted := providerTableQuoted + ".env_http_headers"
	mappings := tracing.CodexHeaderMappings()
	values := make(map[string]string)
	present := make(map[string]bool)
	duplicate := ""
	record := func(header, value string) {
		for _, mapping := range mappings {
			if strings.EqualFold(header, mapping.Header) {
				key := strings.ToLower(mapping.Header)
				if present[key] {
					duplicate = mapping.Header
					return
				}
				values[key] = value
				present[key] = true
				return
			}
		}
	}
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
		if table == nestedTable || table == nestedTableQuoted {
			parsed, parsedOK := parseTomlString(strings.TrimSpace(value))
			key = strings.Trim(key, " \"")
			if isKnownCodexMapping(key, mappings) && !parsedOK {
				return CodexTraceMapping{}, fmt.Errorf("Codex %s mapping is malformed", key)
			}
			if parsedOK {
				record(key, parsed)
			}
			continue
		}
		if (table == providerTable || table == providerTableQuoted) && key == "env_http_headers" && strings.HasPrefix(strings.TrimSpace(value), "{") {
			mapping, valid := parseInlineHeaderMap(strings.TrimSpace(value))
			if !valid {
				return CodexTraceMapping{}, errors.New("Codex env_http_headers table is malformed")
			}
			for header, mapped := range mapping {
				record(header, mapped)
			}
		}
	}
	if duplicate != "" {
		return CodexTraceMapping{}, fmt.Errorf("duplicate Codex %s mapping", duplicate)
	}

	result := CodexTraceMapping{Existing: len(present) > 0}
	for _, mapping := range mappings {
		key := strings.ToLower(mapping.Header)
		value, exists := values[key]
		if !exists {
			result.Missing = append(result.Missing, mapping.Header)
		} else if value != mapping.Env {
			result.Conflicts = append(result.Conflicts, mapping.Header)
		}
	}
	result.Ready = len(result.Missing) == 0 && len(result.Conflicts) == 0
	return result, nil
}

// InspectCodexTraceHeader is the legacy session-only view retained for callers
// that only need to inspect the original X-Session-ID mapping.
func InspectCodexTraceHeader(path, providerID string) (configured, conflict bool, err error) {
	mapping, err := InspectCodexTraceHeaders(path, providerID)
	if err != nil {
		return false, false, err
	}
	for _, header := range mapping.Missing {
		if strings.EqualFold(header, canonicalTraceHeader) {
			return false, false, nil
		}
	}
	for _, header := range mapping.Conflicts {
		if strings.EqualFold(header, canonicalTraceHeader) {
			return false, true, nil
		}
	}
	return true, false, nil
}

// EnsureCodexTraceHeader adds Companion's complete bounded mapping set. The
// name is retained for source compatibility with the original session-only
// helper.
func EnsureCodexTraceHeader(path, providerID string) (CodexTraceMapping, error) {
	return EnsureCodexTraceHeaders(path, providerID)
}

// EnsureCodexTraceHeaders adds the mappings required by Codex when absent. It
// preserves comments and unrelated TOML text and never replaces an existing
// mapping. Unsupported/ambiguous forms fail open to the caller, which should
// launch Codex without Companion trace injection.
func EnsureCodexTraceHeaders(path, providerID string) (CodexTraceMapping, error) {
	if !validCodexName(providerID) {
		return CodexTraceMapping{}, errors.New("invalid Codex provider name")
	}
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	defer lock.Release()
	return ensureCodexTraceHeaderLocked(path, providerID)
}

func ensureCodexTraceHeaderLocked(path, providerID string) (CodexTraceMapping, error) {
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
	providerTableQuoted := "model_providers.\"" + providerID + "\""
	nestedTableQuoted := providerTableQuoted + ".env_http_headers"
	mappings := tracing.CodexHeaderMappings()
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
			missing, conflicts, existing := classifyCodexMappings(mapping, mappings)
			if len(conflicts) > 0 {
				return CodexTraceMapping{Existing: existing, Conflicts: conflicts}, fmt.Errorf("Codex mapping already points elsewhere: %s", strings.Join(conflicts, ", "))
			}
			if len(missing) == 0 {
				return CodexTraceMapping{Ready: true, Existing: existing}, nil
			}
			updated, ok := addInlineHeaderMappings(lines[j], missing)
			if !ok {
				return CodexTraceMapping{}, errors.New("Codex env_http_headers table cannot be merged safely")
			}
			lines[j] = updated
			if err := atomicRewriteCodex(path, strings.Join(lines, ""), info.Mode().Perm()); err != nil {
				return CodexTraceMapping{}, err
			}
			return CodexTraceMapping{Ready: true, Existing: existing, Modified: true}, nil
		}
	}

	// A nested env_http_headers table is the documented and safest form.
	for i, raw := range lines {
		line := tomlLine(raw)
		if tableName(line) != nestedTable && tableName(line) != nestedTableQuoted {
			continue
		}
		end := len(lines)
		mapping := make(map[string]string)
		for j := i + 1; j < len(lines); j++ {
			candidate := tomlLine(lines[j])
			if isTomlTable(candidate) {
				end = j
				break
			}
			key, value, ok := cutTomlAssignment(candidate)
			if ok {
				key = strings.Trim(key, " \"")
				known := isKnownCodexMapping(key, mappings)
				parsed, parsedOK := parseTomlString(strings.TrimSpace(value))
				if known && !parsedOK {
					return CodexTraceMapping{}, fmt.Errorf("Codex %s mapping is malformed", key)
				}
				if parsedOK {
					if known {
						if _, exists := mappingValue(mapping, key); exists {
							return CodexTraceMapping{}, fmt.Errorf("duplicate Codex %s mapping", key)
						}
					}
					mapping[key] = parsed
				}
			}
		}
		missing, conflicts, existing := classifyCodexMappings(mapping, mappings)
		if len(conflicts) > 0 {
			return CodexTraceMapping{Existing: existing, Conflicts: conflicts}, fmt.Errorf("Codex mapping already points elsewhere: %s", strings.Join(conflicts, ", "))
		}
		if len(missing) == 0 {
			return CodexTraceMapping{Ready: true, Existing: existing}, nil
		}
		insert := ""
		for _, wanted := range missing {
			insert += "\"" + wanted.Header + "\" = \"" + wanted.Env + "\"" + tomlLineEnding(contents)
		}
		lines = append(lines[:end], append([]string{insert}, lines[end:]...)...)
		if err := atomicRewriteCodex(path, strings.Join(lines, ""), info.Mode().Perm()); err != nil {
			return CodexTraceMapping{}, err
		}
		return CodexTraceMapping{Ready: true, Existing: existing, Modified: true}, nil
	}

	// No mapping exists. Appending a new nested table is valid as long as the
	// provider did not already define an inline env_http_headers value.
	if !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	lineEnding := tomlLineEnding(contents)
	contents += lineEnding + "[" + nestedTable + "]" + lineEnding
	for _, mapping := range mappings {
		contents += "\"" + mapping.Header + "\" = \"" + mapping.Env + "\"" + lineEnding
	}
	if err := atomicRewriteCodex(path, contents, info.Mode().Perm()); err != nil {
		return CodexTraceMapping{}, err
	}
	return CodexTraceMapping{Ready: true, Modified: true}, nil
}

const codexTraceBackupSuffix = ".freeinference-trace.backup"

// SetupCodexTraceConfig performs the complete explicit setup lifecycle under
// one lock: inspect, backup, and (only when needed) install the mapping. This
// closes the race window between separate backup and rewrite operations.
func SetupCodexTraceConfig(path, providerID string) (CodexTraceMapping, error) {
	if !validCodexName(providerID) {
		return CodexTraceMapping{}, errors.New("invalid Codex provider name")
	}
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	defer lock.Release()

	mapping, err := InspectCodexTraceHeaders(path, providerID)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if len(mapping.Conflicts) > 0 {
		return mapping, fmt.Errorf("Codex mapping already points elsewhere: %s", strings.Join(mapping.Conflicts, ", "))
	}
	if mapping.Ready {
		return mapping, nil
	}
	if err := backupCodexTraceConfigLocked(path); err != nil {
		return CodexTraceMapping{}, err
	}
	return ensureCodexTraceHeaderLocked(path, providerID)
}

// BackupCodexTraceConfig makes a one-time, mode-preserving backup before the
// explicit trace setup command mutates Codex configuration. Existing backups
// are never overwritten, so uninstall remains reversible to the first state.
func BackupCodexTraceConfig(path string) error {
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()
	return backupCodexTraceConfigLocked(path)
}

func backupCodexTraceConfigLocked(path string) error {
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
	mapping, err := InspectCodexTraceHeaders(path, providerID)
	if err != nil {
		return err
	}
	if len(mapping.Conflicts) > 0 || len(mapping.Missing) > 0 {
		return errors.New("Codex Companion mapping changed; refusing to restore over user changes")
	}
	if !mapping.Ready {
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
		for existingKey := range result {
			if strings.EqualFold(existingKey, key) {
				return nil, false
			}
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

func addInlineHeaderMappings(raw string, mappings []tracing.HeaderMapping) (string, bool) {
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
	additions := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		additions = append(additions, "\""+mapping.Header+"\" = \""+mapping.Env+"\"")
	}
	return raw[:close] + separator + strings.Join(additions, ", ") + raw[close:] + comment + newline, true
}

func addInlineHeaderMapping(raw string) (string, bool) {
	return addInlineHeaderMappings(raw, []tracing.HeaderMapping{
		{Header: canonicalTraceHeader, Env: codexTraceHeaderEnv},
	})
}

func classifyCodexMappings(values map[string]string, mappings []tracing.HeaderMapping) (missing []tracing.HeaderMapping, conflicts []string, existing bool) {
	for _, mapping := range mappings {
		value, found := mappingValue(values, mapping.Header)
		if !found {
			missing = append(missing, mapping)
			continue
		}
		existing = true
		if value != mapping.Env {
			conflicts = append(conflicts, mapping.Header)
		}
	}
	return missing, conflicts, existing
}

func isKnownCodexMapping(header string, mappings []tracing.HeaderMapping) bool {
	for _, mapping := range mappings {
		if strings.EqualFold(header, mapping.Header) {
			return true
		}
	}
	return false
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
