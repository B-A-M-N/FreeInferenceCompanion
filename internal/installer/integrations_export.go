package installer

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
)

// IntegrationSummary is a sanitized, externally reportable integration record.
type IntegrationSummary struct {
	Client          string    `json:"client"`
	ConfigRoot      string    `json:"config_root"`
	PluginPath      string    `json:"plugin_path"`
	MarketplacePath string    `json:"marketplace_path,omitempty"`
	Version         string    `json:"version"`
	Registered      bool      `json:"registered,omitempty"`
	DiscoverySource string    `json:"discovery_source"`
	InstalledAt     time.Time `json:"installed_at"`
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
	if err := clientenv.ValidateEnvironment(client, root); err != nil {
		return IntegrationSummary{}, err
	}
	paths, err := PathsForHome(home)
	if err != nil {
		return IntegrationSummary{}, err
	}
	environment := clientenv.Environment{Client: client, ConfigRoot: root, Source: clientenv.SourceExplicit}
	results, err := ReconcileClientEnvironments(reconcileOptions{
		home: paths.home(),
		pluginSources: map[clientenv.Client]string{
			clientenv.ClientClaudeCode: paths.claudePluginPath(),
			clientenv.ClientCodex:      paths.codexPluginPath(),
		},
		version:   corePluginVersion(paths),
		discovery: false,
		explicit:  []clientenv.Environment{environment},
		stdout:    stdout,
	})
	if err != nil {
		return IntegrationSummary{}, err
	}
	for _, result := range results {
		if result.Warning != "" {
			return IntegrationSummary{}, errors.New(result.Warning)
		}
		if result.Action == "installed" && result.Client == string(client) && canonical(result.ConfigRoot) == canonical(root) {
			metadata, loadErr := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
			if loadErr != nil {
				return IntegrationSummary{}, loadErr
			}
			if record := findClientIntegration(metadata, environment); record != nil {
				return integrationSummaryFromRecord(*record), nil
			}
		}
	}
	return IntegrationSummary{}, errors.New("explicit client integration did not complete")
}

func corePluginVersion(paths Paths) string {
	metadata, found, err := LoadInstallationMetadata(paths.MetadataPath())
	if err != nil || !found || metadata == nil {
		return ""
	}
	if metadata.CodexPluginVersion != "" {
		return metadata.CodexPluginVersion
	}
	return metadata.ClaudePluginVersion
}

func integrationSummaryFromRecord(record ClientIntegration) IntegrationSummary {
	return IntegrationSummary{
		Client:          record.Client,
		ConfigRoot:      record.ConfigRoot,
		PluginPath:      record.PluginPath,
		MarketplacePath: record.MarketplacePath,
		Version:         record.Version,
		Registered:      record.Registered,
		DiscoverySource: record.DiscoverySource,
		InstalledAt:     record.InstalledAt,
	}
}

// RemoveClientIntegration removes one recorded alternate environment by the
// stable identity (client type, config root). It returns removed paths and
// non-fatal cleanup warnings, and refuses modified or path-drifted targets.
func RemoveClientIntegration(home string, client clientenv.Client, root string, stdout io.Writer) ([]string, error) {
	metadata, err := loadClientEnvironmentMetadata(clientEnvironmentMetadataPath(home))
	if err != nil {
		return nil, err
	}
	selector := &clientenv.Environment{Client: client, ConfigRoot: root}
	removed, warnings := UninstallClientIntegration(home, metadata, selector, stdout)
	if len(warnings) > 0 {
		if len(removed) == 0 {
			return nil, errors.New(warnings[0])
		}
		joined := make([]string, len(warnings))
		copy(joined, warnings)
		return removed, fmt.Errorf("%s", strings.Join(joined, "; "))
	}
	return removed, nil
}
