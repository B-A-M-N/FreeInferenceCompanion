// Package clientenv discovers Claude Code and Codex client configuration
// roots. Discovery is bounded and structural: it never crawls a home
// recursively and never identifies environments by launcher or profile names.
package clientenv

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

	canonicalClaude := CanonicalRoot(homeAbs, ClientClaudeCode)
	add(Environment{Client: ClientClaudeCode, ConfigRoot: canonicalClaude, Source: SourceCanonical})
	if validateRoot(canonicalClaude) == nil && IsLegacyFreeInferenceClaudeSettings(filepath.Join(canonicalClaude, "settings.json")) {
		warnings = append(warnings, legacyClaudeRouteWarning(canonicalClaude))
	}
	if root, ok := lookup("CLAUDE_CONFIG_DIR"); ok && strings.TrimSpace(root) != "" {
		root = strings.TrimSpace(root)
		if warning := validateRoot(root); warning != nil {
			warnings = append(warnings, fmt.Errorf("%s %s: %w", ClientClaudeCode, root, warning))
		} else if IsFreeInferenceClaudeSettings(filepath.Join(root, "settings.json")) {
			add(Environment{Client: ClientClaudeCode, ConfigRoot: root, Source: SourceEnvironment})
		} else if IsLegacyFreeInferenceClaudeSettings(filepath.Join(root, "settings.json")) {
			warnings = append(warnings, legacyClaudeRouteWarning(root))
		}
	}
	add(Environment{Client: ClientCodex, ConfigRoot: CanonicalRoot(homeAbs, ClientCodex), Source: SourceCanonical})
	if root, ok := lookup("CODEX_HOME"); ok && strings.TrimSpace(root) != "" {
		root = strings.TrimSpace(root)
		if warning := validateRoot(root); warning != nil {
			warnings = append(warnings, fmt.Errorf("%s %s: %w", ClientCodex, root, warning))
		} else if IsFreeInferenceCodexConfig(filepath.Join(root, "config.toml")) {
			add(Environment{Client: ClientCodex, ConfigRoot: root, Source: SourceEnvironment})
		}
	}

	xdgRoot := strings.TrimSpace(lookupOrDefault(lookup, "XDG_CONFIG_HOME", filepath.Join(homeAbs, ".config")))
	var legacyClaudeFound []string
	claudeFound, claudeWarnings := scanDirectories(xdgRoot, xdgScanDepth, func(dir string) bool {
		settingsPath := filepath.Join(dir, "settings.json")
		if looksLikeClaudeSettings(settingsPath) {
			return true
		}
		if looksLikeLegacyClaudeSettings(settingsPath) {
			legacyClaudeFound = append(legacyClaudeFound, dir)
		}
		return false
	})
	warnings = append(warnings, claudeWarnings...)
	for _, root := range claudeFound {
		environments = append(environments, Environment{Client: ClientClaudeCode, ConfigRoot: root, Source: SourceXDG})
	}
	sort.Strings(legacyClaudeFound)
	for _, root := range legacyClaudeFound {
		warnings = append(warnings, legacyClaudeRouteWarning(root))
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
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	return ValidateEnvironmentWithHome(client, root, home)
}

// ValidateEnvironmentWithHome verifies an explicit root using the supplied
// trusted home for proxy-attestation lookup. It exists so CLI callers cannot
// accidentally validate under a different home than they will mutate.
func ValidateEnvironmentWithHome(client Client, root, home string) error {
	switch client {
	case ClientClaudeCode:
		if !looksLikeClaudeSettings(filepath.Join(root, "settings.json")) {
			if IsLegacyFreeInferenceClaudeSettings(filepath.Join(root, "settings.json")) {
				return errors.New("claude settings.json uses the legacy FreeInference /v1 route; set ANTHROPIC_BASE_URL to https://freeinference.org/anthropic")
			}
			return errors.New("claude settings.json does not select an approved FreeInference client route")
		}
	case ClientCodex:
		state, _, err := VerifyCodexConfigRoute(home, root)
		if err != nil || (state != CodexRouteVerifiedDirect && state != CodexRouteVerifiedProxy) {
			return errors.New("codex config.toml does not select an approved FreeInference route")
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
	return IsFreeInferenceClaudeSettings(path)
}

func looksLikeLegacyClaudeSettings(path string) bool {
	return IsLegacyFreeInferenceClaudeSettings(path)
}

func legacyClaudeRouteWarning(root string) error {
	return fmt.Errorf("claude configuration %s uses the legacy FreeInference /v1 route; update ANTHROPIC_BASE_URL to https://freeinference.org/anthropic before integration discovery can manage it", root)
}

func looksLikeCodexConfig(path string) bool {
	return IsFreeInferenceCodexConfig(path)
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
