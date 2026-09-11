// Package clientenv discovers Claude Code and Codex client configuration
// roots. Discovery is bounded and structural: it never crawls a home
// recursively and never identifies environments by launcher or profile names.
package clientenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/config"
)

// Client identifies a supported client type.
type Client string

const (
	ClientClaudeCode Client = "claude-code"
	ClientCodex      Client = "codex"
)

// DiscoverySource explains why an environment was selected.
type DiscoverySource string

const (
	SourceCanonical   DiscoverySource = "canonical"
	SourceEnvironment DiscoverySource = "environment"
	SourceXDG         DiscoverySource = "xdg-discovery"
	SourceHome        DiscoverySource = "home-discovery"
	SourceRecorded    DiscoverySource = "recorded"
	SourceExplicit    DiscoverySource = "explicit"
)

// Environment is one client configuration root, not a model profile.
type Environment struct {
	Client     Client
	ConfigRoot string
	Source     DiscoverySource
}

const (
	maxConfigBytes = 1 << 20
	maxScanDirs    = 4096
	maxHomeEntries = 4096
	xdgScanDepth   = 2
)

// CanonicalRoot returns the client's documented default configuration root.
// It does not identify alternate environments or launcher-specific profiles.
func CanonicalRoot(home string, client Client) string {
	switch client {
	case ClientClaudeCode:
		return filepath.Join(home, ".claude")
	case ClientCodex:
		return filepath.Join(home, ".codex")
	default:
		return ""
	}
}

// Discover returns canonical roots, roots exported in the current process
// environment, and bounded structural discoveries, deduplicated by canonical
// absolute root. Warnings describe invalid explicit roots and scan failures;
// they do not prevent healthy environments from being returned.
func Discover(home string) ([]Environment, []error) {
	return DiscoverWithEnv(home, os.Environ())
}

// DiscoverWithEnv is the injectable form used by tests and callers that need
// an explicit environment snapshot.
func DiscoverWithEnv(home string, env []string) ([]Environment, []error) {
	if strings.TrimSpace(home) == "" {
		return nil, []error{errors.New("home directory is empty")}
	}
	homeAbs, err := filepath.Abs(home)
	if err != nil {
		return nil, []error{err}
	}

	lookup := func(key string) (string, bool) {
		for _, item := range env {
			k, value, ok := strings.Cut(item, "=")
			if ok && k == key {
				return value, true
			}
		}
		return "", false
	}

	var (
		environments []Environment
		warnings     []error
	)
	add := func(environment Environment) {
		if warning := validateRoot(environment.ConfigRoot); warning != nil {
			warnings = append(warnings, fmt.Errorf("%s %s: %w", environment.Client, environment.ConfigRoot, warning))
			return
		}
		environments = append(environments, environment)
	}

	add(Environment{Client: ClientClaudeCode, ConfigRoot: CanonicalRoot(homeAbs, ClientClaudeCode), Source: SourceCanonical})
	if root, ok := lookup("CLAUDE_CONFIG_DIR"); ok && strings.TrimSpace(root) != "" {
		add(Environment{Client: ClientClaudeCode, ConfigRoot: strings.TrimSpace(root), Source: SourceEnvironment})
	}
	add(Environment{Client: ClientCodex, ConfigRoot: CanonicalRoot(homeAbs, ClientCodex), Source: SourceCanonical})
	if root, ok := lookup("CODEX_HOME"); ok && strings.TrimSpace(root) != "" {
		add(Environment{Client: ClientCodex, ConfigRoot: strings.TrimSpace(root), Source: SourceEnvironment})
	}

	xdgRoot := strings.TrimSpace(lookupOrDefault(lookup, "XDG_CONFIG_HOME", filepath.Join(homeAbs, ".config")))
	claudeFound, claudeWarnings := scanDirectories(xdgRoot, xdgScanDepth, func(dir string) bool {
		return looksLikeClaudeSettings(filepath.Join(dir, "settings.json"))
	})
	warnings = append(warnings, claudeWarnings...)
	for _, root := range claudeFound {
		environments = append(environments, Environment{Client: ClientClaudeCode, ConfigRoot: root, Source: SourceXDG})
	}
	codexFound, codexWarnings := scanDirectories(xdgRoot, xdgScanDepth, func(dir string) bool {
		return looksLikeCodexConfig(filepath.Join(dir, "config.toml"))
	})
	warnings = append(warnings, codexWarnings...)
	for _, root := range codexFound {
		environments = append(environments, Environment{Client: ClientCodex, ConfigRoot: root, Source: SourceXDG})
	}
	codexHomeFound, homeWarnings := scanHiddenCodexHome(homeAbs)
	warnings = append(warnings, homeWarnings...)
	for _, root := range codexHomeFound {
		environments = append(environments, Environment{Client: ClientCodex, ConfigRoot: root, Source: SourceHome})
	}
	return dedupe(environments), warnings
}

// ValidateEnvironment reports whether one explicit configuration root is a
// directory containing the client's expected FreeInference route. It performs
// no discovery and never accepts launcher names as identity.
func ValidateEnvironment(client Client, root string) error {
	switch client {
	case ClientClaudeCode:
		if !looksLikeClaudeSettings(filepath.Join(root, "settings.json")) {
			return errors.New("Claude settings.json does not select an approved FreeInference /v1 endpoint")
		}
	case ClientCodex:
		if !looksLikeCodexConfig(filepath.Join(root, "config.toml")) {
			return errors.New("Codex config.toml does not select an approved FreeInference /v1 endpoint")
		}
	default:
		return fmt.Errorf("unsupported client %q", client)
	}
	return validateRoot(root)
}

func lookupOrDefault(lookup func(string) (string, bool), key, fallback string) string {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func validateRoot(root string) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("configuration root is a symlink")
	}
	if !info.IsDir() {
		return errors.New("configuration root is not a directory")
	}
	return nil
}

func scanHiddenCodexHome(home string) ([]string, []error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, []error{fmt.Errorf("scan home directory: %w", err)}
	}
	if len(entries) > maxHomeEntries {
		return nil, []error{fmt.Errorf("home directory exceeds %d entries", maxHomeEntries)}
	}
	var found []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
			continue
		}
		root := filepath.Join(home, entry.Name())
		if info, err := os.Lstat(root); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		if looksLikeCodexConfig(filepath.Join(root, "config.toml")) {
			found = append(found, root)
		}
	}
	return found, nil
}

func scanDirectories(root string, maxDepth int, accept func(string) bool) ([]string, []error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, []error{err}
	}
	info, err := os.Lstat(rootAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{err}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil
	}
	var (
		found    []string
		warnings []error
		visited  int
	)
	var visit func(dir string, depth int)
	visit = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		visited++ // root is depth 0; first candidates are depth 1

		if visited > maxScanDirs {
			if visited == maxScanDirs+1 {
				warnings = append(warnings, fmt.Errorf("bounded scan exceeded %d directories under %s", maxScanDirs, rootAbs))
			}
			return
		}
		if accept(dir) {
			found = append(found, dir)
		}
		if depth == maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("scan %s: %w", dir, err))
			return
		}
		for _, entry := range entries {
			// Hidden and symlinked directories are outside the normal
			// user-level discovery namespace and can make traversal unbounded.
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			visit(filepath.Join(dir, entry.Name()), depth+1)
		}
	}
	visit(rootAbs, 0)
	sort.Strings(found)
	return found, warnings
}

func looksLikeClaudeSettings(path string) bool {
	data, err := readBoundedNoFollow(path)
	if err != nil {
		return false
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	for key, value := range settings.Env {
		if key != "ANTHROPIC_BASE_URL" {
			continue
		}
		endpoint, err := api.NormalizeEndpoint(value)
		if err == nil && endpoint.IsFI && endpoint.RequestURL == endpoint.Origin+"/v1" {
			return true
		}
	}
	return false
}

func looksLikeCodexConfig(path string) bool {
	data, err := readBoundedNoFollow(path)
	if err != nil {
		return false
	}
	table := ""
	selectedProvider := ""
	providers := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if hash := strings.IndexByte(line, '#'); hash >= 0 {
			line = line[:hash]
		}
		line = strings.TrimSpace(line)
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
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		value, ok = parseConfigString(value)
		if !ok {
			continue
		}
		if table == "" && key == "model_provider" {
			selectedProvider = value
		}
		if strings.HasPrefix(table, "model_providers.") && key == "base_url" {
			providers[strings.TrimPrefix(table, "model_providers.")] = value
		}
	}
	if selectedProvider == "" {
		selectedProvider = "openai"
	}
	baseURL := providers[selectedProvider]
	if baseURL == "" {
		return false
	}
	endpoint, err := api.NormalizeEndpoint(baseURL)
	return err == nil && endpoint.IsFI && endpoint.RequestURL == endpoint.Origin+"/v1"
}

func parseConfigString(value string) (string, bool) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", false
	}
	return strings.ReplaceAll(value[1:len(value)-1], `\\"`, `"`), true
}

func readBoundedNoFollow(path string) ([]byte, error) {
	f, err := config.OpenNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigBytes {
		return nil, errors.New("file exceeds the supported size limit")
	}
	return data, nil
}

func dedupe(environments []Environment) []Environment {
	canonicalRoot := func(path string) string {
		abs, err := filepath.Abs(path)
		if err != nil {
			return filepath.Clean(path)
		}
		return filepath.Clean(abs)
	}
	type identity struct {
		client Client
		root   string
	}
	rank := map[DiscoverySource]int{
		SourceCanonical:   0,
		SourceEnvironment: 1,
		SourceExplicit:    2,
		SourceXDG:         3,
		SourceHome:        4,
		SourceRecorded:    5,
	}
	best := make(map[identity]Environment)
	for _, environment := range environments {
		id := identity{environment.Client, canonicalRoot(environment.ConfigRoot)}
		previous, exists := best[id]
		if !exists || rank[environment.Source] < rank[previous.Source] {
			best[id] = environment
		}
	}
	identities := make([]identity, 0, len(best))
	for id := range best {
		identities = append(identities, id)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].root != identities[j].root {
			return identities[i].root < identities[j].root
		}
		return identities[i].client < identities[j].client
	})
	result := make([]Environment, 0, len(identities))
	for _, id := range identities {
		result = append(result, best[id])
	}
	return result
}
