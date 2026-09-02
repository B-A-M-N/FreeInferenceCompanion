package runtime

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
)

const maxCodexConfigBytes = 1 << 20

type codexProviderConfig struct {
	BaseURL string
	EnvKey  string
}

type codexConfig struct {
	ModelProvider string
	Providers     map[string]codexProviderConfig
}

// ResolveCodexProviderConfiguration reads the selected Codex provider from
// the user's config without executing commands or reading credentials from
// disk. The provider's env_key must name the environment variable Codex uses;
// a coincidental FREEINFERENCE_API_KEY is not enough.
func ResolveCodexProviderConfiguration() (ClientEvidence, error) {
	home, err := codexHome()
	if err != nil {
		return ClientEvidence{}, err
	}
	base, err := loadCodexConfig(filepath.Join(home, "config.toml"))
	if err != nil {
		return ClientEvidence{}, err
	}

	selectionSource := "config.toml:model_provider"
	providerID := strings.TrimSpace(base.ModelProvider)
	if providerID == "" {
		providerID = "openai"
	}

	// Codex profile selection is normally a CLI argument and is not exposed to
	// child commands. If an embedding environment supplies CODEX_PROFILE, honor
	// it; otherwise the top-level model_provider is the only selection we can
	// establish. A profile's provider table is inherited from config.toml.
	if profile := strings.TrimSpace(os.Getenv("CODEX_PROFILE")); profile != "" {
		if !validCodexName(profile) {
			return ClientEvidence{}, errors.New("invalid CODEX_PROFILE name")
		}
		profileConfig, profileErr := loadCodexConfig(filepath.Join(home, profile+".config.toml"))
		if profileErr != nil {
			return ClientEvidence{}, fmt.Errorf("load Codex profile: %w", profileErr)
		}
		if strings.TrimSpace(profileConfig.ModelProvider) != "" {
			providerID = strings.TrimSpace(profileConfig.ModelProvider)
		}
		selectionSource = "CODEX_PROFILE:" + profile
	}

	provider, ok := base.Providers[providerID]
	if !ok {
		return ClientEvidence{
			Client:                    ClientCodex,
			ProviderID:                providerID,
			ProviderSelectionVerified: false,
			ProviderSelectionSource:   selectionSource,
		}, errors.New("selected Codex provider definition is unavailable")
	}
	if strings.TrimSpace(provider.BaseURL) == "" || strings.TrimSpace(provider.EnvKey) == "" {
		return ClientEvidence{
			Client:                    ClientCodex,
			ProviderID:                providerID,
			ProviderSelectionVerified: false,
			ProviderSelectionSource:   selectionSource,
		}, errors.New("selected Codex provider lacks base_url or env_key")
	}
	if !validCodexName(providerID) || !validEnvName(strings.TrimSpace(provider.EnvKey)) {
		return ClientEvidence{
			Client:                    ClientCodex,
			ProviderID:                providerID,
			ProviderSelectionVerified: false,
			ProviderSelectionSource:   selectionSource,
		}, errors.New("selected Codex provider contains an invalid identifier")
	}

	credentialSource := CredentialSource(strings.TrimSpace(provider.EnvKey))
	credentialValue := os.Getenv(string(credentialSource))
	return ClientEvidence{
		Client:                    ClientCodex,
		EndpointSource:            "codex:model_providers." + providerID + ".base_url",
		EndpointURL:               provider.BaseURL,
		CredentialSource:          credentialSource,
		CredentialValue:           credentialValue,
		ProviderID:                providerID,
		ProviderEnvKey:            string(credentialSource),
		ProviderSelectionVerified: true,
		ProviderSelectionSource:   selectionSource,
	}, nil
}

// CodexProviderConfiguration returns the non-secret provider configuration
// summary for diagnostics. It is not itself an activation decision.
func CodexProviderConfiguration() (ProviderConfiguration, error) {
	evidence, err := ResolveCodexProviderConfiguration()
	config := ProviderConfiguration{
		ProviderID:              evidence.ProviderID,
		CredentialSource:        string(evidence.CredentialSource),
		SelectionVerified:       evidence.ProviderSelectionVerified,
		SelectionSource:         evidence.ProviderSelectionSource,
		FreeInferenceConfigured: false,
	}
	if evidence.EndpointURL != "" {
		if id, normalizeErr := api.NormalizeEndpoint(evidence.EndpointURL); normalizeErr == nil {
			config.EndpointURL = id.RequestURL
			config.FreeInferenceConfigured = id.IsFI
		}
	}
	if err != nil {
		return config, err
	}
	return config, nil
}

func codexHome() (string, error) {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("Codex home: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func loadCodexConfig(path string) (codexConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return codexConfig{}, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxCodexConfigBytes+1))
	if err != nil {
		return codexConfig{}, err
	}
	if len(body) > maxCodexConfigBytes {
		return codexConfig{}, errors.New("Codex config exceeds the supported size limit")
	}
	return parseCodexConfig(string(body)), nil
}

// parseCodexConfig intentionally parses only the stable scalar fields needed
// for activation. It is not a general TOML implementation and never attempts
// to interpret auth commands, bearer tokens, or arbitrary config values.
func parseCodexConfig(contents string) codexConfig {
	result := codexConfig{Providers: make(map[string]codexProviderConfig)}
	table := ""
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(stripTomlComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value, ok = parseTomlString(strings.TrimSpace(value))
		if !ok {
			continue
		}
		if table == "" {
			if key == "model_provider" {
				result.ModelProvider = value
			}
			continue
		}
		const prefix = "model_providers."
		if !strings.HasPrefix(table, prefix) {
			continue
		}
		providerID := strings.Trim(strings.TrimPrefix(table, prefix), "\"")
		provider := result.Providers[providerID]
		switch key {
		case "base_url":
			provider.BaseURL = value
		case "env_key":
			provider.EnvKey = value
		default:
			continue
		}
		result.Providers[providerID] = provider
	}
	return result
}

func stripTomlComment(line string) string {
	quoted := false
	escaped := false
	for i, r := range line {
		switch {
		case r == '"' && !escaped:
			quoted = !quoted
		case r == '#' && !quoted:
			return line[:i]
		}
		escaped = r == '\\' && !escaped
		if r != '\\' {
			escaped = false
		}
	}
	return line
}

func parseTomlString(value string) (string, bool) {
	if len(value) < 2 {
		return "", false
	}
	if value[0] == '"' && value[len(value)-1] == '"' {
		parsed, err := strconv.Unquote(value)
		return parsed, err == nil
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1], true
	}
	return "", false
}

func validCodexName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func validEnvName(value string) bool {
	if value == "" || (value[0] != '_' && (value[0] < 'A' || value[0] > 'Z') && (value[0] < 'a' || value[0] > 'z')) {
		return false
	}
	for _, r := range value[1:] {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	// Codex's provider env_key is a credential reference. Reject common
	// process/configuration variables so a malformed config cannot cause the
	// companion to treat unrelated local data as an API credential.
	upper := strings.ToUpper(value)
	for _, nonCredential := range []string{"HOME", "PATH", "PWD", "SHELL", "USER", "CODEX_HOME"} {
		if upper == nonCredential {
			return false
		}
	}
	return true
}
