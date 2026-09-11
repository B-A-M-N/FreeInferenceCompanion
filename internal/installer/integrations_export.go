package installer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
)

// IntegrationSummary is a sanitized, externally reportable integration record.
type IntegrationSummary struct {
	Client           string    `json:"client"`
	ConfigRoot       string    `json:"config_root"`
	PluginPath       string    `json:"plugin_path"`
	MarketplacePath  string    `json:"marketplace_path,omitempty"`
	Version          string    `json:"version"`
	Registered       bool      `json:"registered,omitempty"`
	MarketplaceAdded bool      `json:"marketplace_added,omitempty"`
	DiscoverySource  string    `json:"discovery_source"`
	InstalledAt      time.Time `json:"installed_at"`
}

// CodexInstallationSummary separates local payload state from native Codex
// registration. A route can be verified even when the plugin is absent, and a
// payload can be present while native registration is incomplete.
type CodexInstallationSummary struct {
	PayloadInstalled      bool `json:"payload_installed"`
	MarketplaceInstalled  bool `json:"marketplace_installed"`
	MarketplaceRegistered bool `json:"marketplace_registered"`
	PluginRegistered      bool `json:"plugin_registered"`
	NativeStatusKnown     bool `json:"native_status_known"`
}

// InspectCodexInstallation reports canonical or alternate Codex ownership and
// payload state without mutating either the client configuration or metadata.
func InspectCodexInstallation(home, root string) (CodexInstallationSummary, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return CodexInstallationSummary{}, err
	}
	root = filepath.Clean(root)
	canonicalRoot, err := filepath.Abs(clientenv.CanonicalRoot(home, clientenv.ClientCodex))
	if err != nil {
		return CodexInstallationSummary{}, err
	}
	pluginPath := filepath.Join(root, "plugins", "freeinference-companion")
	marketplacePath := filepath.Join(root, "plugins", "freeinference-companion-marketplace")
	result := CodexInstallationSummary{
		PayloadInstalled:     pathIsDirectory(pluginPath),
		MarketplaceInstalled: pathIsDirectory(marketplacePath),
	}
	if root == canonicalRoot {
		paths, err := PathsForHome(home)
		if err != nil {
			return result, err
		}
		metadata, found, err := LoadInstallationMetadata(paths.MetadataPath())
		if err != nil {
			return result, err
		}
		if found && metadata != nil {
			resolved := pathsForRecordedCodex(paths, metadata)
			result.PayloadInstalled = pathIsDirectory(resolved.codexPluginPath())
			result.MarketplaceInstalled = pathIsDirectory(resolved.CodexMarketplaceDir)
			result.MarketplaceRegistered = metadata.CodexMarketplaceAdded
			result.PluginRegistered = metadata.CodexPluginRegistered
			result.NativeStatusKnown = metadata.CodexNativeRegistrationKnown
		}
		return result, nil
	}
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		return result, err
	}
	for _, record := range metadata.Integrations {
		if record.Client == string(clientenv.ClientCodex) && canonical(record.ConfigRoot) == root {
			result.MarketplaceRegistered = record.MarketplaceAdded
			result.PluginRegistered = record.Registered
			result.NativeStatusKnown = true
			break
		}
	}
	return result, nil
}

func pathIsDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// ClientEnvironmentMetadataPath exposes the stable ownership-document path
// without exposing private installer transaction details.
func ClientEnvironmentMetadataPath(home string) string {
	return clientEnvironmentMetadataPath(home)
}

// ListClientIntegrations returns sanitized alternate-environment ownership
// records. Canonical core ownership remains represented by core metadata.
func ListClientIntegrations(home string) ([]IntegrationSummary, error) {
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		return nil, err
	}
	result := make([]IntegrationSummary, 0, len(metadata.Integrations))
	for _, record := range metadata.Integrations {
		result = append(result, integrationSummaryFromRecord(record))
	}
	return result, nil
}

// AddClientIntegration explicitly registers one arbitrary client
// configuration root and immediately installs from the verified core plugin.
// The route must independently prove FreeInference; launcher names are never
// accepted as evidence.
func AddClientIntegration(home string, client clientenv.Client, root string, stdout io.Writer) (IntegrationSummary, error) {
	if strings.TrimSpace(root) == "" {
		return IntegrationSummary{}, errors.New("configuration root is empty")
	}
	paths, err := PathsForHome(home)
	if err != nil {
		return IntegrationSummary{}, err
	}
	var summary IntegrationSummary
	err = withInstallerLock(paths, func() error {
		// Metadata read, legacy normalization, validation, version selection, and
		// target mutation must observe one coherent core installation under the
		// same installer mutation lock.
		normalizedPaths, normalizeErr := paths.normalizeLegacyPaths()
		if normalizeErr != nil {
			return normalizeErr
		}
		metadata, found, loadErr := LoadInstallationMetadata(normalizedPaths.MetadataPath())
		if loadErr != nil || !found || metadata == nil {
			return errors.New("core installation metadata is unavailable")
		}
		if normalizedPaths, normalizeErr = validateAndNormalizeMetadataPaths(metadata, normalizedPaths); normalizeErr != nil {
			return fmt.Errorf("core installation does not match this home: %w", normalizeErr)
		}
		version := corePluginVersionForClient(metadata, client)
		if version == "" {
			return errors.New("verified core plugin is unavailable for the requested client")
		}
		if err := verifyCorePluginSource(normalizedPaths, metadata, client); err != nil {
			return err
		}
		summary, err = addClientIntegrationLocked(home, normalizedPaths, metadata, client, root, version, stdout)
		return err
	})
	return summary, err
}

func addClientIntegrationLocked(home string, paths Paths, metadata *InstallationMetadata, client clientenv.Client, root, version string, stdout io.Writer) (IntegrationSummary, error) {
	if err := clientenv.ValidateEnvironmentWithHome(client, root, home); err != nil {
		return IntegrationSummary{}, err
	}
	coreOwnedDirectories := make(map[string]string)
	if client == clientenv.ClientCodex && metadata != nil {
		for _, evidence := range [][2]string{
			{metadata.CodexPluginPath, metadata.CodexPluginSHA256},
			{metadata.CodexMarketplacePath, metadata.CodexMarketplaceSHA256},
		} {
			if evidence[0] == "" || evidence[1] == "" {
				continue
			}
			coreOwnedDirectories[canonical(evidence[0])] = evidence[1]
		}
	}
	environment := clientenv.Environment{Client: client, ConfigRoot: root, Source: clientenv.SourceExplicit}
	results, reconcileErr := ReconcileClientEnvironments(reconcileOptions{
		home: paths.home(),
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: paths.CoreClaudePluginPath,
			clientenv.ClientCodex:      paths.CoreCodexPluginPath,
		},
		coreOwnedDirectories: coreOwnedDirectories,
		version:              version,
		discovery:            false,
		explicit:             []clientenv.Environment{environment},
		stdout:               stdout,
	})
	if reconcileErr != nil {
		return IntegrationSummary{}, reconcileErr
	}
	var warning string
	for _, result := range results {
		if result.Warning != "" {
			warning = result.Warning
		}
		if result.Action == "installed" && result.Client == string(client) && canonical(result.ConfigRoot) == canonical(root) {
			persisted, loadErr := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
			if loadErr != nil {
				return IntegrationSummary{}, loadErr
			}
			if record := findClientIntegration(persisted, environment); record != nil {
				summary := integrationSummaryFromRecord(*record)
				// A committed install remains successful even when optional Codex
				// native registration warns; the ownership record is durable.
				if warning != "" {
					stdoutSafeWrite(stdout, "Warning: "+warning+"\n")
				}
				return summary, nil
			}
		}
	}
	if warning != "" {
		return IntegrationSummary{}, errors.New(warning)
	}
	return IntegrationSummary{}, errors.New("explicit client integration did not complete")
}

func stdoutSafeWrite(stdout io.Writer, text string) {
	if stdout != nil {
		_, _ = io.WriteString(stdout, text)
	}
}

func corePluginVersionForClient(metadata *InstallationMetadata, client clientenv.Client) string {
	if metadata == nil {
		return ""
	}
	switch client {
	case clientenv.ClientCodex:
		return metadata.CodexPluginVersion
	case clientenv.ClientClaudeCode:
		return metadata.ClaudePluginVersion
	default:
		return ""
	}
}

func verifyCorePluginSource(paths Paths, metadata *InstallationMetadata, client clientenv.Client) error {
	var source string
	var digest string
	switch client {
	case clientenv.ClientClaudeCode:
		source = paths.CoreClaudePluginPath
		digest = metadata.CoreClaudePluginSHA256
	case clientenv.ClientCodex:
		source = paths.CoreCodexPluginPath
		digest = metadata.CoreCodexPluginSHA256
	default:
		return fmt.Errorf("unsupported integration client %q", client)
	}
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return fmt.Errorf("verified %s plugin source is unavailable", client)
	}
	if err != nil {
		return fmt.Errorf("inspect verified %s plugin source: %w", client, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("verified %s plugin source is not a directory", client)
	}
	if metadata == nil || !coreDirectoryComponentReady(source, digest, source) {
		return fmt.Errorf("verified %s plugin source ownership evidence is missing or changed", client)
	}
	return nil
}

func integrationSummaryFromRecord(record ClientIntegration) IntegrationSummary {
	return IntegrationSummary{
		Client:           record.Client,
		ConfigRoot:       record.ConfigRoot,
		PluginPath:       record.PluginPath,
		MarketplacePath:  record.MarketplacePath,
		Version:          record.Version,
		Registered:       record.Registered,
		MarketplaceAdded: record.MarketplaceAdded,
		DiscoverySource:  record.DiscoverySource,
		InstalledAt:      record.InstalledAt,
	}
}

// RemoveClientIntegration removes one recorded alternate environment by the
// stable identity (client type, config root). It returns removed paths and
// non-fatal cleanup warnings, and refuses modified or path-drifted targets.
func RemoveClientIntegration(home string, client clientenv.Client, root string, stdout io.Writer) ([]string, error) {
	paths, err := PathsForHome(home)
	if err != nil {
		return nil, err
	}
	var removed []string
	var removeErr error
	err = withInstallerLock(paths, func() error {
		metadata, loadErr := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
		if loadErr != nil {
			return loadErr
		}
		selector := &clientenv.Environment{Client: client, ConfigRoot: root}
		var warnings []string
		removed, warnings = UninstallClientIntegration(home, metadata, selector, stdout)
		if len(warnings) > 0 {
			if len(removed) == 0 {
				removeErr = errors.New(warnings[0])
			} else {
				removeErr = fmt.Errorf("%s", strings.Join(warnings, "; "))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, removeErr
}
