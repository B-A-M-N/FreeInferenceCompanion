package runtime

import (
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

	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
)

const (
	codexTraceOwnershipSchema = 1
	maxCodexTraceOwnership    = 32 << 10
)

type codexTraceOwnership struct {
	SchemaVersion      int       `json:"schema_version"`
	ConfigPath         string    `json:"config_path"`
	ProviderID         string    `json:"provider_id"`
	AddedMappings      []string  `json:"added_mappings"`
	CreatedNestedTable bool      `json:"created_nested_table"`
	Inline             bool      `json:"inline"`
	BeforeFingerprint  string    `json:"before_fingerprint"`
	AfterFingerprint   string    `json:"after_fingerprint"`
	InstalledAt        time.Time `json:"installed_at"`
}

func codexTraceOwnershipPath(configPath string) string {
	return configPath + ".freeinference-trace.metadata"
}

func (m codexTraceOwnership) validate() error {
	if m.SchemaVersion != codexTraceOwnershipSchema || !validCodexName(m.ProviderID) || m.ConfigPath == "" || len(m.AddedMappings) == 0 || len(m.AddedMappings) > 8 || m.InstalledAt.IsZero() {
		return errors.New("invalid codex trace ownership metadata")
	}
	if m.BeforeFingerprint == "" || m.AfterFingerprint == "" || len(m.BeforeFingerprint) != 64 || len(m.AfterFingerprint) != 64 {
		return errors.New("invalid codex trace ownership fingerprint")
	}
	seen := make(map[string]bool)
	for _, header := range m.AddedMappings {
		if seen[strings.ToLower(header)] || !isKnownTraceHeader(header) {
			return errors.New("invalid codex trace ownership mapping")
		}
		seen[strings.ToLower(header)] = true
	}
	return nil
}

func isKnownTraceHeader(header string) bool {
	for _, mapping := range tracing.CodexHeaderMappings() {
		if strings.EqualFold(header, mapping.Header) {
			return true
		}
	}
	return false
}

func traceHeaderEnv(header string) (string, bool) {
	for _, mapping := range tracing.CodexHeaderMappings() {
		if strings.EqualFold(header, mapping.Header) {
			return mapping.Env, true
		}
	}
	return "", false
}

func fingerprintCodexConfig(contents string) string {
	sum := sha256.Sum256([]byte(contents))
	return hex.EncodeToString(sum[:])
}

func loadCodexTraceOwnership(path string) (*codexTraceOwnership, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, errors.New("codex trace ownership metadata is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxCodexTraceOwnership {
		return nil, false, errors.New("codex trace ownership metadata is too large")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	var metadata codexTraceOwnership
	if err := dec.Decode(&metadata); err != nil {
		return nil, false, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, false, errors.New("codex trace ownership metadata contains multiple JSON values")
		}
		return nil, false, err
	}
	if err := metadata.validate(); err != nil {
		return nil, false, err
	}
	return &metadata, true, nil
}

func saveCodexTraceOwnership(path string, metadata codexTraceOwnership) error {
	if err := metadata.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".freeinference-trace-metadata-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func ownershipForTrace(path, providerID string, before, after string, mapping CodexTraceMapping) codexTraceOwnership {
	return codexTraceOwnership{
		SchemaVersion:      codexTraceOwnershipSchema,
		ConfigPath:         path,
		ProviderID:         providerID,
		AddedMappings:      append([]string(nil), mapping.Added...),
		CreatedNestedTable: mapping.CreatedTable,
		Inline:             mapping.Inline,
		BeforeFingerprint:  fingerprintCodexConfig(before),
		AfterFingerprint:   fingerprintCodexConfig(after),
		InstalledAt:        time.Now().UTC(),
	}
}

func (m codexTraceOwnership) expectedMapping(header string) (string, error) {
	for _, owned := range m.AddedMappings {
		if strings.EqualFold(owned, header) {
			if env, ok := traceHeaderEnv(header); ok {
				return env, nil
			}
			break
		}
	}
	return "", fmt.Errorf("codex trace mapping %s is not companion-owned", header)
}
