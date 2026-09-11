package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
)

const (
	clientEnvironmentMetadataSchema = 1
	clientEnvironmentDigestFormat   = "framed-v2"
	maxClientEnvironmentMetadata    = 128 << 10
)

// ClientEnvironmentMetadata owns only additional client environments. Core
// canonical ownership remains in core.json; this document allows fan-out and
// reconciliation without turning every hardened core field into an array.
type ClientEnvironmentMetadata struct {
	SchemaVersion int                 `json:"schema_version"`
	Integrations  []ClientIntegration `json:"integrations"`
}

type ClientIntegration struct {
	Client            string    `json:"client"`
	ConfigRoot        string    `json:"config_root"`
	PluginPath        string    `json:"plugin_path"`
	PluginSHA256      string    `json:"plugin_sha256"`
	MarketplacePath   string    `json:"marketplace_path,omitempty"`
	MarketplaceSHA256 string    `json:"marketplace_sha256,omitempty"`
	Version           string    `json:"version"`
	Registered        bool      `json:"registered,omitempty"`
	DiscoverySource   string    `json:"discovery_source"`
	InstalledAt       time.Time `json:"installed_at"`
}

// EnvironmentIntegrationResult reports one non-canonical reconciliation.
type EnvironmentIntegrationResult struct {
	Client     string `json:"client"`
	ConfigRoot string `json:"config_root"`
	Action     string `json:"action"`
	Warning    string `json:"warning,omitempty"`
}

// ReconcileOptions are inputs owned by installOrUpdate.
type reconcileOptions struct {
	home          string
	pluginSources map[clientenv.Client]string
	version       string
	dryRun        bool
	discovery     bool
	explicit      []clientenv.Environment
	stdout        io.Writer
}

func (o reconcileOptions) sourceFor(client clientenv.Client) (string, error) {
	source := o.pluginSources[client]
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("verified %s plugin source unavailable: %w", client, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("verified %s plugin source is not a directory", client)
	}
	return source, nil
}

func clientEnvironmentMetadataPath(home string) string {
	return filepath.Join(home, ".config", "freeinference-companion", "installations", "client-environments.json")
}

func loadClientEnvironmentMetadata(path string) (*ClientEnvironmentMetadata, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return &ClientEnvironmentMetadata{SchemaVersion: clientEnvironmentMetadataSchema}, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("client environment metadata is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxClientEnvironmentMetadata {
		return nil, errors.New("client environment metadata exceeds the supported size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var metadata ClientEnvironmentMetadata
	if err := dec.Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decode client environment metadata: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("client environment metadata contains multiple JSON values")
		}
		return nil, fmt.Errorf("read client environment metadata: %w", err)
	}
	if err := validateClientEnvironmentMetadata(&metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func validateClientEnvironmentMetadata(metadata *ClientEnvironmentMetadata) error {
	if metadata.SchemaVersion != clientEnvironmentMetadataSchema {
		return fmt.Errorf("unsupported client environment metadata schema %d", metadata.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(metadata.Integrations))
	for i := range metadata.Integrations {
		record := &metadata.Integrations[i]
		if err := validateClientIntegration(record); err != nil {
			return fmt.Errorf("integration %d: %w", i, err)
		}
		id := clientIntegrationID(record.Client, record.ConfigRoot)
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate integration identity %s", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateClientIntegration(record *ClientIntegration) error {
	if !validClientName(record.Client) {
		return fmt.Errorf("invalid client %q", record.Client)
	}
	if !semverPattern.MatchString(record.Version) {
		return fmt.Errorf("invalid version %q", record.Version)
	}
	if record.InstalledAt.IsZero() || record.PluginSHA256 == "" || len(record.PluginSHA256) != 64 || !isHex(record.PluginSHA256) {
		return errors.New("invalid ownership evidence")
	}
	expectedPlugin, err := expectedClientPluginPath(record.Client, record.ConfigRoot)
	if err != nil {
		return err
	}
	recorded, err := canonicalPath(record.PluginPath)
	if err != nil {
		return errors.New("invalid plugin path")
	}
	expected, err := canonicalPath(expectedPlugin)
	if err != nil {
		return errors.New("invalid expected plugin path")
	}
	if recorded != expected {
		return errors.New("plugin path is not derivable from client identity")
	}
	if record.MarketplacePath != "" || record.MarketplaceSHA256 != "" {
		if record.Client != string(clientenv.ClientCodex) || record.MarketplacePath == "" || record.MarketplaceSHA256 == "" {
			return errors.New("invalid marketplace ownership")
		}
		expectedMarketplace := filepath.Join(record.ConfigRoot, "plugins", "freeinference-companion-marketplace")
		marketplaceRecorded, err := canonicalPath(record.MarketplacePath)
		if err != nil {
			return errors.New("invalid marketplace path")
		}
		marketplaceExpected, err := canonicalPath(expectedMarketplace)
		if err != nil {
			return errors.New("invalid expected marketplace path")
		}
		if marketplaceRecorded != marketplaceExpected || len(record.MarketplaceSHA256) != 64 || !isHex(record.MarketplaceSHA256) {
			return errors.New("marketplace path is not derivable from client identity")
		}
	}
	return nil
}

func validClientName(client string) bool {
	return client == string(clientenv.ClientClaudeCode) || client == string(clientenv.ClientCodex)
}

func clientIntegrationID(client, root string) string {
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		canonicalRoot = filepath.Clean(root)
	}
	return client + ":" + canonicalRoot
}

func expectedClientPluginPath(client, root string) (string, error) {
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		return "", err
	}
	if !validClientName(client) {
		return "", fmt.Errorf("invalid client %q", client)
	}
	return filepath.Join(canonicalRoot, "plugins", "freeinference-companion"), nil
}

func saveClientEnvironmentMetadata(path string, metadata *ClientEnvironmentMetadata) error {
	if err := validateClientEnvironmentMetadata(metadata); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".client-environments-*")
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

// ReconcileClientEnvironments fans the verified core plugin artifact into
// canonical-adjacent Claude/Codex environments. Failures warn and continue;
// they never invalidate a healthy core installation.
func ReconcileClientEnvironments(opts reconcileOptions) ([]EnvironmentIntegrationResult, error) {
	metadataPath := clientEnvironmentMetadataPath(opts.home)
	prior, err := loadClientEnvironmentMetadata(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("read client environment metadata: %w", err)
	}
	environments, discoveryWarnings := clientenv.Discover(opts.home)
	var results []EnvironmentIntegrationResult
	for _, warning := range discoveryWarnings {
		results = append(results, EnvironmentIntegrationResult{Action: "warning", Warning: warning.Error()})
	}
	if !opts.discovery {
		environments = filterCanonical(environments)
	}
	environments = append(environments, opts.explicit...)
	// Previously owned roots survive removal from discovery; upgrades must not
	// strand an old Companion copy in them.
	for _, record := range prior.Integrations {
		// A root that no longer exists has nothing to reconcile or own. Drop its
		// stale record; never recreate an abandoned client profile.
		if info, statErr := os.Lstat(record.ConfigRoot); statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		environments = append(environments, clientenv.Environment{
			Client:     clientenv.Client(record.Client),
			ConfigRoot: record.ConfigRoot,
			Source:     clientenv.SourceRecorded,
		})
	}
	environments = dedupeClientEnvironments(environments)
	records := make([]ClientIntegration, 0, len(environments))
	changed := false
	now := time.Now().UTC()
	for _, environment := range environments {
		if isCanonicalRoot(opts.home, environment) {
			continue
		}
		result, record, warning := reconcileOneClientEnvironment(environment, prior, opts, now)
		results = append(results, result)
		if warning != "" {
			// A failed reconciliation must not erase proof that an existing old
			// copy is Companion-owned. Preserve its record until a later safe
			// update succeeds or explicit uninstall removes it.
			if previous := findClientIntegration(prior, environment); previous != nil {
				records = append(records, *previous)
			}
			continue
		}
		changed = true
		records = append(records, record)
	}
	if opts.dryRun {
		return results, nil
	}
	if changed || len(records) != len(prior.Integrations) {
		next := &ClientEnvironmentMetadata{SchemaVersion: clientEnvironmentMetadataSchema, Integrations: records}
		if err := saveClientEnvironmentMetadata(metadataPath, next); err != nil {
			return results, fmt.Errorf("save client environment metadata: %w", err)
		}
	}
	return results, nil
}

func filterCanonical(environments []clientenv.Environment) []clientenv.Environment {
	result := make([]clientenv.Environment, 0, len(environments))
	for _, environment := range environments {
		if environment.Source == clientenv.SourceCanonical {
			result = append(result, environment)
		}
	}
	return result
}

func dedupeClientEnvironments(environments []clientenv.Environment) []clientenv.Environment {
	type identity struct {
		client clientenv.Client
		root   string
	}
	rank := map[clientenv.DiscoverySource]int{
		clientenv.SourceCanonical:   0,
		clientenv.SourceEnvironment: 1,
		clientenv.SourceExplicit:    2,
		clientenv.SourceXDG:         3,
		clientenv.SourceHome:        4,
		clientenv.SourceRecorded:    5,
	}
	best := make(map[identity]clientenv.Environment)
	for _, environment := range environments {
		root, err := canonicalPath(environment.ConfigRoot)
		if err != nil {
			continue
		}
		id := identity{environment.Client, root}
		previous, exists := best[id]
		if !exists || rank[environment.Source] < rank[previous.Source] {
			best[id] = environment
		}
	}
	result := make([]clientenv.Environment, 0, len(best))
	for _, environment := range best {
		result = append(result, environment)
	}
	return result
}

func isCanonicalRoot(home string, environment clientenv.Environment) bool {
	root, err := canonicalPath(environment.ConfigRoot)
	if err != nil {
		return true
	}
	switch environment.Client {
	case clientenv.ClientClaudeCode:
		canonical, _ := canonicalPath(clientenv.CanonicalRoot(home, clientenv.ClientClaudeCode))
		return root == canonical
	case clientenv.ClientCodex:
		canonical, _ := canonicalPath(clientenv.CanonicalRoot(home, clientenv.ClientCodex))
		return root == canonical
	default:
		return true
	}
}

func reconcileOneClientEnvironment(environment clientenv.Environment, prior *ClientEnvironmentMetadata, opts reconcileOptions, now time.Time) (EnvironmentIntegrationResult, ClientIntegration, string) {
	result := EnvironmentIntegrationResult{Client: string(environment.Client), ConfigRoot: environment.ConfigRoot}
	pluginSource, sourceErr := opts.sourceFor(environment.Client)
	if sourceErr != nil {
		result.Action, result.Warning = "warning", sourceErr.Error()
		return result, ClientIntegration{}, sourceErr.Error()
	}
	if environment.Source != clientenv.SourceRecorded {
		if warning := validateClientConfigRoot(environment.ConfigRoot); warning != nil {
			result.Action, result.Warning = "warning", warning.Error()
			return result, ClientIntegration{}, warning.Error()
		}
	}
	expectedPlugin, err := expectedClientPluginPath(string(environment.Client), environment.ConfigRoot)
	if err != nil {
		result.Action, result.Warning = "warning", err.Error()
		return result, ClientIntegration{}, err.Error()
	}
	previous := findClientIntegration(prior, environment)
	if err := validateAdditionalOwnedDirectory(expectedPlugin, previous); err != nil {
		result.Action, result.Warning = "warning", err.Error()
		return result, ClientIntegration{}, err.Error()
	}
	record := ClientIntegration{
		Client:          string(environment.Client),
		ConfigRoot:      environment.ConfigRoot,
		PluginPath:      expectedPlugin,
		Version:         opts.version,
		DiscoverySource: string(environment.Source),
		InstalledAt:     now,
	}
	if environment.Client == clientenv.ClientCodex {
		marketplacePath := filepath.Join(environment.ConfigRoot, "plugins", "freeinference-companion-marketplace")
		if err := validateAdditionalOwnedDirectory(marketplacePath, previous); err != nil {
			result.Action, result.Warning = "warning", err.Error()
			return result, ClientIntegration{}, err.Error()
		}
		record.MarketplacePath = marketplacePath
	}
	if opts.dryRun {
		result.Action = "planned"
		return result, record, ""
	}
	tx := &installTransaction{}
	stage, err := stageDirectory(pluginSource, expectedPlugin)
	if err == nil {
		err = tx.replace(expectedPlugin, stage)
	}
	if err != nil {
		tx.rollback()
		result.Action, result.Warning = "warning", fmt.Sprintf("install plugin: %v", err)
		return result, ClientIntegration{}, result.Warning
	}
	if environment.Client == clientenv.ClientCodex {
		marketplaceStage, marketErr := stageCodexMarketplace(Paths{CodexMarketplaceDir: record.MarketplacePath}, pluginSource)
		if marketErr == nil {
			marketErr = tx.replace(record.MarketplacePath, marketplaceStage)
		}
		if marketErr != nil {
			tx.rollback()
			result.Action, result.Warning = "warning", fmt.Sprintf("install marketplace: %v", marketErr)
			return result, ClientIntegration{}, result.Warning
		}
	}
	if err := tx.finalize(); err != nil {
		result.Action, result.Warning = "warning", fmt.Sprintf("cleanup: %v", err)
		return result, ClientIntegration{}, result.Warning
	}
	record.PluginSHA256, _ = pathDigest(expectedPlugin)
	if record.MarketplacePath != "" {
		record.MarketplaceSHA256, _ = pathDigest(record.MarketplacePath)
	}
	if environment.Client == clientenv.ClientCodex {
		registered, warnings := registerCodexMarketplaceForHome(record.ConfigRoot, record.MarketplacePath, opts.stdout)
		record.Registered = registered
		if len(warnings) > 0 {
			result.Warning = warnings[0]
		}
	}
	result.Action = "installed"
	return result, record, ""
}

func validateClientConfigRoot(root string) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("configuration root is not a directory")
	}
	return nil
}

func validateAdditionalOwnedDirectory(path string, previous *ClientIntegration) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing unsafe target path %s", path)
	}
	if previous == nil {
		return fmt.Errorf("refusing to replace unowned Companion directory %s", path)
	}
	expectedDigest := previous.PluginSHA256
	if path == previous.MarketplacePath && previous.MarketplaceSHA256 != "" {
		expectedDigest = previous.MarketplaceSHA256
	}
	if expectedDigest == "" {
		return fmt.Errorf("owned Companion directory has no checksum %s", path)
	}
	matched, err := pathDigestMatches(path, expectedDigest)
	if err != nil || !matched {
		return fmt.Errorf("Companion directory changed after installation: %s", path)
	}
	return nil
}

func findClientIntegration(metadata *ClientEnvironmentMetadata, environment clientenv.Environment) *ClientIntegration {
	if metadata == nil {
		return nil
	}
	for i := range metadata.Integrations {
		record := &metadata.Integrations[i]
		if record.Client == string(environment.Client) && clientIntegrationID(record.Client, record.ConfigRoot) == clientIntegrationID(string(environment.Client), environment.ConfigRoot) {
			return record
		}
	}
	return nil
}

// UninstallClientEnvironments removes only paths derivable from recorded
// client/config-root identities. It never trusts plugin_path as an arbitrary
// deletion target and never removes an owned directory changed after install.
func UninstallClientEnvironments(home string, stdout io.Writer) ([]string, []string) {
	path := clientEnvironmentMetadataPath(home)
	metadata, err := loadClientEnvironmentMetadata(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("read client environment ownership: %v", err)}
	}
	removed, warnings := UninstallClientIntegration(home, metadata, nil, stdout)
	if len(warnings) > 0 {
		return removed, warnings
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return removed, []string{fmt.Sprintf("remove client environment metadata: %v", err)}
	}
	return removed, nil
}

// UninstallClientIntegration removes one recorded identity. A nil selector
// means all alternate integrations. The transaction spans selected records and
// metadata removal; drift/digest failures retain ownership for later repair.
func UninstallClientIntegration(home string, metadata *ClientEnvironmentMetadata, selector *clientenv.Environment, stdout io.Writer) ([]string, []string) {
	if metadata == nil {
		return nil, []string{"client environment ownership metadata is unavailable"}
	}
	path := clientEnvironmentMetadataPath(home)
	var removed, warnings []string
	tx := &installTransaction{}
	for i := range metadata.Integrations {
		record := metadata.Integrations[i]
		if selector != nil && clientIntegrationID(record.Client, record.ConfigRoot) != clientIntegrationID(string(selector.Client), selector.ConfigRoot) {
			continue
		}
		recordRemoved, recordWarnings := uninstallOneClientIntegration(tx, record)
		removed = append(removed, recordRemoved...)
		warnings = append(warnings, recordWarnings...)
		if record.Registered {
			warnings = append(warnings, unregisterCodexClientMarketplace(record.ConfigRoot)...)
		}
	}
	if selector != nil && len(removed) == 0 {
		return removed, append(warnings, "no matching client integration was found")
	}
	if len(warnings) > 0 && selector != nil {
		tx.rollback()
		return nil, warnings
	}
	remaining := make([]ClientIntegration, 0, len(metadata.Integrations))
	for _, record := range metadata.Integrations {
		if selector == nil || clientIntegrationID(record.Client, record.ConfigRoot) != clientIntegrationID(string(selector.Client), selector.ConfigRoot) {
			remaining = append(remaining, record)
		}
	}
	if len(remaining) > 0 {
		if err := saveClientEnvironmentMetadata(path, &ClientEnvironmentMetadata{SchemaVersion: clientEnvironmentMetadataSchema, Integrations: remaining}); err != nil {
			tx.rollback()
			return nil, append(warnings, fmt.Sprintf("save client environment metadata: %v", err))
		}
	} else if err := tx.remove(path); err != nil {
		tx.rollback()
		return nil, append(warnings, fmt.Sprintf("remove client environment metadata: %v", err))
	}
	if err := tx.finalize(); err != nil {
		warnings = append(warnings, fmt.Sprintf("cleanup client environment rollback files: %v", err))
	}
	return removed, warnings
}

func uninstallOneClientIntegration(tx *installTransaction, record ClientIntegration) ([]string, []string) {
	var removed []string
	pluginPath, err := expectedClientPluginPath(record.Client, record.ConfigRoot)
	if err != nil {
		return nil, []string{fmt.Sprintf("invalid client environment %s: %v", record.ConfigRoot, err)}
	}
	if canonical(pluginRecordPath(record)) != canonical(pluginPath) {
		return nil, []string{fmt.Sprintf("client environment path drift refused for %s", record.ConfigRoot)}
	}
	if err := validateOwnedDirectoryForRemoval(pluginPath, record.PluginSHA256); err != nil {
		return nil, []string{err.Error()}
	}
	if err := tx.remove(pluginPath); err != nil {
		return nil, []string{fmt.Sprintf("remove %s: %v", pluginPath, err)}
	}
	removed = append(removed, pluginPath)
	if record.MarketplacePath == "" {
		return removed, nil
	}
	expectedMarketplace := filepath.Join(record.ConfigRoot, "plugins", "freeinference-companion-marketplace")
	if canonical(record.MarketplacePath) != canonical(expectedMarketplace) {
		return removed, []string{fmt.Sprintf("client marketplace path drift refused for %s", record.ConfigRoot)}
	}
	if err := validateOwnedDirectoryForRemoval(expectedMarketplace, record.MarketplaceSHA256); err != nil {
		return removed, []string{err.Error()}
	}
	if err := tx.remove(expectedMarketplace); err != nil {
		return removed, []string{fmt.Sprintf("remove %s: %v", expectedMarketplace, err)}
	}
	return append(removed, expectedMarketplace), nil
}

func pluginRecordPath(record ClientIntegration) string { return record.PluginPath }

func canonical(path string) string {
	canonical, err := canonicalPath(path)
	if err != nil {
		return ""
	}
	return canonical
}

func underlyingPathError(err error) error { return err }

func unregisterCodexClientMarketplace(codexHome string) []string {
	codex, err := exec.LookPath("codex")
	if err != nil {
		return []string{"Codex CLI was not found; alternate-profile registration may remain"}
	}
	var warnings []string
	if err := runCodexPluginCommandForHome(codex, codexHome, "plugin", "remove", "freeinference-companion@freeinference-companion-local", "--json"); err != nil {
		warnings = append(warnings, fmt.Sprintf("Codex plugin registration cleanup failed for %s", codexHome))
	}
	if err := runCodexPluginCommandForHome(codex, codexHome, "plugin", "marketplace", "remove", "freeinference-companion-local", "--json"); err != nil {
		warnings = append(warnings, fmt.Sprintf("Codex marketplace cleanup failed for %s", codexHome))
	}
	return warnings
}
