package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	installationMetadataSchema = 3
	maxInstallationMetadata    = 32 << 10
)

// InstallationMetadata records only the files owned by the Companion
// installer. It is deliberately separate from runtime configuration so the
// executable used to invoke install/update cannot masquerade as an installed
// version.
type InstallationMetadata struct {
	SchemaVersion           int       `json:"schema_version"`
	InstalledVersion        string    `json:"installed_version"`
	BinaryVersion           string    `json:"binary_version,omitempty"`
	ClaudePluginVersion     string    `json:"claude_plugin_version,omitempty"`
	CodexPluginVersion      string    `json:"codex_plugin_version,omitempty"`
	CodexMarketplaceVersion string    `json:"codex_marketplace_version,omitempty"`
	ManagedBinaryPath       string    `json:"managed_binary_path"`
	ManagedBinarySHA256     string    `json:"managed_binary_sha256,omitempty"`
	ShimPath                string    `json:"shim_path"`
	ClaudePluginPath        string    `json:"claude_plugin_path"`
	ClaudePluginSHA256      string    `json:"claude_plugin_sha256,omitempty"`
	CodexPluginPath         string    `json:"codex_plugin_path"`
	CodexPluginSHA256       string    `json:"codex_plugin_sha256,omitempty"`
	CodexMarketplacePath    string    `json:"codex_marketplace_path"`
	CodexMarketplaceSHA256  string    `json:"codex_marketplace_sha256,omitempty"`
	InstalledAt             time.Time `json:"installed_at"`
	InstallerVersion        string    `json:"installer_version"`
	ManifestOrigin          string    `json:"manifest_origin"`
	ArtifactSHA256          string    `json:"artifact_sha256"`
	ShimBackupPath          string    `json:"shim_backup_path,omitempty"`
	ManagedBinaryOwned      bool      `json:"managed_binary_owned"`
	ShimOwned               bool      `json:"shim_owned"`
	ClaudePluginOwned       bool      `json:"claude_plugin_owned"`
	CodexPluginOwned        bool      `json:"codex_plugin_owned"`
	CodexMarketplaceOwned   bool      `json:"codex_marketplace_owned"`
}

func installationMetadataPath(home string) string {
	return filepath.Join(home, ".config", "freeinference-companion", "installations", "core.json")
}

// MetadataPath returns the persistent metadata location for these paths.
func (p Paths) MetadataPath() string {
	if p.metadataPath != "" {
		return p.metadataPath
	}
	if p.InstallDir == "" {
		return ""
	}
	home := filepath.Dir(filepath.Dir(p.InstallDir))
	return installationMetadataPath(home)
}

func (p Paths) lockPath() string {
	return filepath.Join(filepath.Dir(p.MetadataPath()), "installer.lock")
}

func (p Paths) shimPath() string {
	if p.ShimPath != "" {
		return p.ShimPath
	}
	return filepath.Join(p.LocalBin, "freeinference")
}

func (p Paths) claudePluginPath() string {
	if p.ClaudePluginPath != "" {
		return p.ClaudePluginPath
	}
	return filepath.Join(p.ClaudePluginDir, "freeinference-companion")
}

func (p Paths) codexPluginPath() string {
	if p.CodexPluginPath != "" {
		return p.CodexPluginPath
	}
	return filepath.Join(p.CodexPluginDir, "freeinference-companion")
}

func (m InstallationMetadata) validate() error {
	if m.SchemaVersion != installationMetadataSchema {
		return fmt.Errorf("unsupported installation metadata schema %d", m.SchemaVersion)
	}
	if strings.TrimSpace(m.InstalledVersion) == "" || !semverPattern.MatchString(m.InstalledVersion) {
		return errors.New("installation metadata has an invalid installed version")
	}
	for name, value := range map[string]string{
		"managed_binary_path":    m.ManagedBinaryPath,
		"shim_path":              m.ShimPath,
		"claude_plugin_path":     m.ClaudePluginPath,
		"codex_plugin_path":      m.CodexPluginPath,
		"codex_marketplace_path": m.CodexMarketplacePath,
		"manifest_origin":        m.ManifestOrigin,
		"artifact_sha256":        m.ArtifactSHA256,
	} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("installation metadata has an invalid %s", name)
		}
	}
	for name, owned := range map[string]bool{
		"managed_binary":    m.ManagedBinaryOwned,
		"claude_plugin":     m.ClaudePluginOwned,
		"codex_plugin":      m.CodexPluginOwned,
		"codex_marketplace": m.CodexMarketplaceOwned,
	} {
		if owned {
			var checksum string
			switch name {
			case "managed_binary":
				checksum = m.ManagedBinarySHA256
			case "claude_plugin":
				checksum = m.ClaudePluginSHA256
			case "codex_plugin":
				checksum = m.CodexPluginSHA256
			case "codex_marketplace":
				checksum = m.CodexMarketplaceSHA256
			}
			if checksum == "" {
				return fmt.Errorf("installation metadata has no checksum for owned %s", name)
			}
			var componentVersion string
			switch name {
			case "managed_binary":
				componentVersion = m.BinaryVersion
			case "claude_plugin":
				componentVersion = m.ClaudePluginVersion
			case "codex_plugin":
				componentVersion = m.CodexPluginVersion
			case "codex_marketplace":
				componentVersion = m.CodexMarketplaceVersion
			}
			if !semverPattern.MatchString(componentVersion) {
				return fmt.Errorf("installation metadata has an invalid version for owned %s", name)
			}
		}
	}
	for name, componentVersion := range map[string]string{
		"binary":            m.BinaryVersion,
		"Claude plugin":     m.ClaudePluginVersion,
		"Codex plugin":      m.CodexPluginVersion,
		"Codex marketplace": m.CodexMarketplaceVersion,
	} {
		if componentVersion != "" && !semverPattern.MatchString(componentVersion) {
			return fmt.Errorf("installation metadata has an invalid %s version", name)
		}
	}
	if strings.TrimSpace(m.InstallerVersion) == "" || len(m.InstallerVersion) > 128 || strings.ContainsAny(m.InstallerVersion, "\x00\r\n") {
		return errors.New("installation metadata has an invalid installer version")
	}
	if m.ShimBackupPath != "" && strings.ContainsAny(m.ShimBackupPath, "\x00\r\n") {
		return errors.New("installation metadata has an invalid shim backup path")
	}
	if len(m.ArtifactSHA256) != 64 || !isHex(m.ArtifactSHA256) {
		return errors.New("installation metadata has an invalid artifact checksum")
	}
	for name, value := range map[string]string{
		"managed_binary_sha256":    m.ManagedBinarySHA256,
		"claude_plugin_sha256":     m.ClaudePluginSHA256,
		"codex_plugin_sha256":      m.CodexPluginSHA256,
		"codex_marketplace_sha256": m.CodexMarketplaceSHA256,
	} {
		if value != "" && (len(value) != 64 || !isHex(value)) {
			return fmt.Errorf("installation metadata has an invalid %s", name)
		}
	}
	if m.InstalledAt.IsZero() {
		return errors.New("installation metadata has no installation time")
	}
	return nil
}

func isHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// LoadInstallationMetadata reads metadata without creating its parent
// directory. A missing file is an explicit uninstalled/unknown state.
func LoadInstallationMetadata(path string) (*InstallationMetadata, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, errors.New("installation metadata is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxInstallationMetadata {
		return nil, false, errors.New("installation metadata exceeds the supported size limit")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	var metadata InstallationMetadata
	if err := dec.Decode(&metadata); err != nil {
		return nil, false, fmt.Errorf("decode installation metadata: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, false, errors.New("installation metadata contains multiple JSON values")
		}
		return nil, false, fmt.Errorf("read installation metadata: %w", err)
	}
	if err := metadata.validate(); err != nil {
		return nil, false, err
	}
	return &metadata, true, nil
}

// SaveInstallationMetadata atomically writes private installer ownership
// metadata. The caller must validate the metadata before calling it.
func SaveInstallationMetadata(path string, metadata InstallationMetadata) error {
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
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".core-metadata-*")
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

func stageInstallationMetadata(path string, metadata InstallationMetadata) (string, error) {
	if err := metadata.validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".core-metadata-stage-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0600); err != nil {
		_ = os.Remove(tmpPath)
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = os.Remove(tmpPath)
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func metadataForPaths(paths Paths, version, manifestURL, artifactSHA string, installerVersion string) InstallationMetadata {
	metadata := InstallationMetadata{
		SchemaVersion:        installationMetadataSchema,
		InstalledVersion:     version,
		ManagedBinaryPath:    paths.BinaryPath,
		ShimPath:             paths.shimPath(),
		ClaudePluginPath:     paths.claudePluginPath(),
		CodexPluginPath:      paths.codexPluginPath(),
		CodexMarketplacePath: paths.CodexMarketplaceDir,
		InstalledAt:          time.Now().UTC(),
		InstallerVersion:     installerVersion,
		ManifestOrigin:       manifestURL,
		ArtifactSHA256:       artifactSHA,
		ManagedBinaryOwned:   true,
		ShimOwned:            true,
		ClaudePluginOwned:    true,
		CodexPluginOwned:     true,
	}
	metadata.ManagedBinarySHA256, _ = pathDigest(paths.BinaryPath)
	metadata.ClaudePluginSHA256, _ = pathDigest(paths.claudePluginPath())
	metadata.CodexPluginSHA256, _ = pathDigest(paths.codexPluginPath())
	metadata.CodexMarketplaceSHA256, _ = pathDigest(paths.CodexMarketplaceDir)
	metadata.CodexMarketplaceOwned = metadata.CodexMarketplaceSHA256 != ""
	metadata.ClaudePluginOwned = metadata.ClaudePluginSHA256 != ""
	metadata.CodexPluginOwned = metadata.CodexPluginSHA256 != ""
	metadata.ManagedBinaryOwned = metadata.ManagedBinarySHA256 != ""
	metadata.ShimOwned = metadata.ManagedBinaryOwned
	if metadata.ManagedBinaryOwned {
		metadata.BinaryVersion = version
	}
	if metadata.ClaudePluginOwned {
		metadata.ClaudePluginVersion = version
	}
	if metadata.CodexPluginOwned {
		metadata.CodexPluginVersion = version
	}
	if metadata.CodexMarketplaceOwned {
		metadata.CodexMarketplaceVersion = version
	}
	return metadata
}
