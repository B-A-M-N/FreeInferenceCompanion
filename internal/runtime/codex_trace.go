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
// env_http_headers mapping can carry the documented Companion session header.
type CodexTraceMapping struct {
	Ready        bool
	Existing     bool
	Modified     bool
	Missing      []string
	Conflicts    []string
	Added        []string
	Inline       bool
	CreatedTable bool
}

// InspectCodexTraceHeaders reports the complete mapping state without
// changing Codex config. It deliberately returns no header values.
func InspectCodexTraceHeaders(path, providerID string) (CodexTraceMapping, error) {
	if !validCodexName(providerID) {
		return CodexTraceMapping{}, errors.New("invalid codex provider name")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return CodexTraceMapping{}, errors.New("codex config is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if len(body) > maxCodexConfigBytes {
		return CodexTraceMapping{}, errors.New("codex config exceeds the supported size limit")
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
				return CodexTraceMapping{}, fmt.Errorf("codex %s mapping is malformed", key)
			}
			if parsedOK {
				record(key, parsed)
			}
			continue
		}
		if (table == providerTable || table == providerTableQuoted) && key == "env_http_headers" && strings.HasPrefix(strings.TrimSpace(value), "{") {
			mapping, valid := parseInlineHeaderMap(strings.TrimSpace(value))
			if !valid {
				return CodexTraceMapping{}, errors.New("codex env_http_headers table is malformed")
			}
			for header, mapped := range mapping {
				record(header, mapped)
			}
		}
	}
	if duplicate != "" {
		return CodexTraceMapping{}, fmt.Errorf("duplicate codex %s mapping", duplicate)
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
		return CodexTraceMapping{}, errors.New("invalid codex provider name")
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
		return CodexTraceMapping{}, errors.New("codex config is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if len(body) > maxCodexConfigBytes {
		return CodexTraceMapping{}, errors.New("codex config exceeds the supported size limit")
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
				return CodexTraceMapping{}, errors.New("codex env_http_headers format is unsupported")
			}
			mapping, valid := parseInlineHeaderMap(strings.TrimSpace(value))
			if !valid {
				return CodexTraceMapping{}, errors.New("codex env_http_headers table is malformed")
			}
			missing, conflicts, existing := classifyCodexMappings(mapping, mappings)
			if len(conflicts) > 0 {
				return CodexTraceMapping{Existing: existing, Conflicts: conflicts}, fmt.Errorf("codex mapping already points elsewhere: %s", strings.Join(conflicts, ", "))
			}
			if len(missing) == 0 {
				return CodexTraceMapping{Ready: true, Existing: existing, Inline: true}, nil
			}
			updated, ok := addInlineHeaderMappings(lines[j], missing)
			if !ok {
				return CodexTraceMapping{}, errors.New("codex env_http_headers table cannot be merged safely")
			}
			lines[j] = updated
			if err := atomicRewriteCodex(path, strings.Join(lines, ""), info.Mode().Perm()); err != nil {
				return CodexTraceMapping{}, err
			}
			return CodexTraceMapping{Ready: true, Existing: existing, Modified: true, Added: mappingHeaders(missing), Inline: true}, nil
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
					return CodexTraceMapping{}, fmt.Errorf("codex %s mapping is malformed", key)
				}
				if parsedOK {
					if known {
						if _, exists := mappingValue(mapping, key); exists {
							return CodexTraceMapping{}, fmt.Errorf("duplicate codex %s mapping", key)
						}
					}
					mapping[key] = parsed
				}
			}
		}
		missing, conflicts, existing := classifyCodexMappings(mapping, mappings)
		if len(conflicts) > 0 {
			return CodexTraceMapping{Existing: existing, Conflicts: conflicts}, fmt.Errorf("codex mapping already points elsewhere: %s", strings.Join(conflicts, ", "))
		}
		if len(missing) == 0 {
			return CodexTraceMapping{Ready: true, Existing: existing}, nil
		}
		insert := ""
		for _, wanted := range missing {
			insert += "\"" + wanted.Header + "\" = \"" + wanted.Env + "\"" + tomlLineEnding(contents)
		}
		// SplitAfter leaves an unterminated final line without a separator.
		// Add that separator before inserting a child assignment so the new
		// mapping cannot be glued to the existing TOML value.
		if end == len(lines) && end > 0 && lines[end-1] != "" && !strings.HasSuffix(lines[end-1], "\n") {
			lines[end-1] += tomlLineEnding(contents)
		}
		lines = append(lines[:end], append([]string{insert}, lines[end:]...)...)
		if err := atomicRewriteCodex(path, strings.Join(lines, ""), info.Mode().Perm()); err != nil {
			return CodexTraceMapping{}, err
		}
		return CodexTraceMapping{Ready: true, Existing: existing, Modified: true, Added: mappingHeaders(missing)}, nil
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
	return CodexTraceMapping{Ready: true, Modified: true, Added: mappingHeaders(mappings), CreatedTable: true}, nil
}

const codexTraceBackupSuffix = ".freeinference-trace.backup"

// SetupCodexTraceConfig performs the complete explicit setup lifecycle under
// one lock: inspect, backup, install, and record surgical ownership metadata.
func SetupCodexTraceConfig(path, providerID string) (CodexTraceMapping, error) {
	if !validCodexName(providerID) {
		return CodexTraceMapping{}, errors.New("invalid codex provider name")
	}
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	defer lock.Release()

	before, err := os.ReadFile(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	mapping, err := InspectCodexTraceHeaders(path, providerID)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	if len(mapping.Conflicts) > 0 {
		return mapping, fmt.Errorf("codex mapping already points elsewhere: %s", strings.Join(mapping.Conflicts, ", "))
	}
	ownershipPath := codexTraceOwnershipPath(path)
	if existing, found, err := loadCodexTraceOwnership(ownershipPath); err != nil {
		return CodexTraceMapping{}, err
	} else if found {
		canonical, canonicalErr := canonicalExistingPath(path)
		if canonicalErr != nil || existing.ConfigPath != canonical || existing.ProviderID != providerID {
			return CodexTraceMapping{}, errors.New("codex trace ownership belongs to a different configuration")
		}
		if mapping.Ready {
			return mapping, nil
		}
		return CodexTraceMapping{}, errors.New("codex trace mapping drifted after installation; uninstall or reconcile it first")
	}
	if mapping.Ready {
		return mapping, nil
	}
	if err := backupCodexTraceConfigLocked(path); err != nil {
		return CodexTraceMapping{}, err
	}
	installed, err := ensureCodexTraceHeaderLocked(path, providerID)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	after, err := os.ReadFile(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return CodexTraceMapping{}, err
	}
	ownership := ownershipForTrace(canonical, providerID, string(before), string(after), installed)
	if err := saveCodexTraceOwnership(ownershipPath, ownership); err != nil {
		info, statErr := os.Stat(path)
		if statErr == nil {
			_ = atomicRewriteCodex(path, string(before), info.Mode().Perm())
		}
		return CodexTraceMapping{}, fmt.Errorf("write codex trace ownership: %w", err)
	}
	return installed, nil
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
		return errors.New("codex config is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(body) > maxCodexConfigBytes {
		return errors.New("codex config exceeds the supported size limit")
	}
	backup := path + codexTraceBackupSuffix
	if existing, statErr := os.Lstat(backup); statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			return errors.New("codex trace backup is not a regular file")
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

// RestoreCodexTraceConfig removes only the mappings recorded as Companion-owned
// by explicit setup. The full backup is never used as the restore source, so
// unrelated edits made after setup remain intact.
func RestoreCodexTraceConfig(path, providerID string) error {
	if providerID != "" && !validCodexName(providerID) {
		return errors.New("invalid codex provider name")
	}
	lock, err := acquireCodexTraceLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return err
	}
	ownershipPath := codexTraceOwnershipPath(path)
	ownership, found, err := loadCodexTraceOwnership(ownershipPath)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("no FreeInference codex trace ownership metadata exists")
	}
	if providerID == "" {
		providerID = ownership.ProviderID
	}
	if ownership.ConfigPath != canonical || ownership.ProviderID != providerID {
		return errors.New("codex trace ownership belongs to a different configuration")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("codex config is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(body) > maxCodexConfigBytes {
		return errors.New("codex config exceeds the supported size limit")
	}
	updated, err := removeOwnedCodexMappings(string(body), providerID, *ownership)
	if err != nil {
		return err
	}
	if err := atomicRewriteCodex(path, updated, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Remove(ownershipPath); err != nil {
		rollbackErr := atomicRewriteCodex(path, string(body), info.Mode().Perm())
		if rollbackErr != nil {
			return fmt.Errorf("remove codex trace ownership metadata: %w (rollback failed: %v)", err, rollbackErr)
		}
		return fmt.Errorf("remove codex trace ownership metadata: %w", err)
	}
	backup := path + codexTraceBackupSuffix
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove codex trace backup: %w", err)
	}
	return nil
}

func removeOwnedCodexMappings(contents, providerID string, ownership codexTraceOwnership) (string, error) {
	providerTable := "model_providers." + providerID
	providerTableQuoted := "model_providers.\"" + providerID + "\""
	nestedTable := providerTable + ".env_http_headers"
	nestedTableQuoted := providerTableQuoted + ".env_http_headers"
	owned := make(map[string]string, len(ownership.AddedMappings))
	for _, header := range ownership.AddedMappings {
		env, err := ownership.expectedMapping(header)
		if err != nil {
			return "", err
		}
		owned[strings.ToLower(header)] = env
	}
	lines := strings.SplitAfter(contents, "\n")
	removed := make(map[string]bool)
	table := ""
	for i, raw := range lines {
		line := tomlLine(raw)
		if isTomlTable(line) {
			table = tableName(line)
		}
		if ownership.Inline && (table == providerTable || table == providerTableQuoted) {
			for j := i + 1; j < len(lines); j++ {
				candidate := tomlLine(lines[j])
				if isTomlTable(candidate) {
					break
				}
				key, _, ok := cutTomlAssignment(candidate)
				if !ok || key != "env_http_headers" {
					continue
				}
				updated, found, err := removeInlineOwnedMappings(lines[j], owned, removed)
				if err != nil {
					return "", err
				}
				if !found {
					return "", errors.New("codex companion inline trace mapping changed; refusing to remove user changes")
				}
				lines[j] = updated
				break
			}
		}
		if ownership.Inline || (table != nestedTable && table != nestedTableQuoted) {
			continue
		}
		key, value, ok := cutTomlAssignment(line)
		if !ok {
			continue
		}
		key = strings.Trim(key, " \"")
		env, isOwned := owned[strings.ToLower(key)]
		if !isOwned {
			continue
		}
		parsed, parsedOK := parseTomlString(strings.TrimSpace(value))
		if !parsedOK || parsed != env {
			return "", fmt.Errorf("codex companion mapping %s changed; refusing to remove user changes", key)
		}
		removed[strings.ToLower(key)] = true
		lines[i] = ""
	}
	for header := range owned {
		if !removed[header] {
			return "", errors.New("codex companion trace mapping is missing; refusing to remove user changes")
		}
	}
	if ownership.CreatedNestedTable {
		removeEmptyCodexNestedTable(lines, nestedTable, nestedTableQuoted)
	}
	updated := strings.Join(lines, "")
	if !ownership.OriginalTrailingNewline {
		updated = strings.TrimSuffix(updated, "\n")
		updated = strings.TrimSuffix(updated, "\r")
	}
	return updated, nil
}

func removeInlineOwnedMappings(raw string, owned map[string]string, removed map[string]bool) (string, bool, error) {
	withoutComment := stripTomlComment(raw)
	open := strings.Index(withoutComment, "{")
	close := strings.LastIndex(withoutComment, "}")
	if open < 0 || close < open {
		return "", false, errors.New("codex env_http_headers table is malformed")
	}
	parts := splitInlineTable(withoutComment[open+1 : close])
	remaining := make([]string, 0, len(parts))
	found := false
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return "", false, errors.New("codex env_http_headers table is malformed")
		}
		key = strings.Trim(strings.TrimSpace(key), "\"")
		parsed, parsedOK := parseTomlString(strings.TrimSpace(value))
		if !parsedOK {
			return "", false, errors.New("codex env_http_headers table is malformed")
		}
		if expected, isOwned := owned[strings.ToLower(key)]; isOwned {
			if parsed != expected {
				return "", false, fmt.Errorf("codex companion mapping %s changed; refusing to remove user changes", key)
			}
			removed[strings.ToLower(key)] = true
			found = true
			continue
		}
		remaining = append(remaining, part)
	}
	updated := withoutComment[:open+1] + strings.Join(remaining, ",") + withoutComment[close:]
	if len(withoutComment) < len(raw) {
		updated += raw[len(withoutComment):]
	}
	return updated, found, nil
}

func removeEmptyCodexNestedTable(lines []string, nestedTable, nestedTableQuoted string) {
	for i, raw := range lines {
		line := tomlLine(raw)
		if tableName(line) != nestedTable && tableName(line) != nestedTableQuoted {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if isTomlTable(tomlLine(lines[j])) {
				end = j
				break
			}
		}
		for j := i + 1; j < end; j++ {
			trimmed := strings.TrimSpace(stripTomlComment(lines[j]))
			if trimmed != "" {
				return
			}
		}
		lines[i] = ""
		// ensureCodexTraceHeaderLocked inserts one separator before a newly
		// appended table. Remove that separator with the owned table so a
		// setup/restore round trip leaves the original bytes unchanged.
		if i > 0 && strings.TrimSpace(lines[i-1]) == "" {
			lines[i-1] = ""
		}
		return
	}
}

func acquireCodexTraceLock(path string) (*state.FileLock, error) {
	lock := state.NewFileLock(path + ".freeinference-trace.lock")
	if err := lock.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			return nil, errors.New("another codex trace setup operation is in progress")
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

func mappingHeaders(mappings []tracing.HeaderMapping) []string {
	result := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		result = append(result, mapping.Header)
	}
	return result
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
		return fmt.Errorf("create codex config temp: %w", err)
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
		return fmt.Errorf("replace codex config: %w", err)
	}
	return nil
}
