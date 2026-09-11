package clientenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/config"
)

// CodexProxyAttestation is an explicit declaration that a loopback Codex
// provider forwards to an approved FreeInference /v1 endpoint. It is stored in
// FIC-owned metadata, never written into the client's config.toml.
type CodexProxyAttestation struct {
	UpstreamURL string `json:"upstream_url"`
	ProxyURL    string `json:"proxy_url"`
}

// CodexProxyAttestationPath exposes the injective attestation location for
// callers that need explicit cleanup or diagnostics.
func CodexProxyAttestationPath(home, root string) string { return codexProxyPath(home, root) }

// LoadCodexProxyAttestation returns the explicit upstream declaration for a
// loopback Codex environment. Callers must still validate the selected route
// with VerifyCodexConfigRoute before using the declaration for activation.
func LoadCodexProxyAttestation(home, root string) (*CodexProxyAttestation, error) {
	return loadCodexProxyAttestation(home, root)
}

func codexProxyPath(home, root string) string {
	id, err := canonicalRootPath(root)
	if err != nil {
		id = filepath.Clean(root)
	}
	// SHA-256 is injective enough for configuration-root filenames and avoids
	// separator/underscore collisions in the legacy encoder.
	digest := sha256.Sum256([]byte(id))
	name := hex.EncodeToString(digest[:])
	return filepath.Join(home, ".config", "freeinference-companion", "clientenv", name+".codex-proxy.json")
}

func canonicalRootPath(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// SetCodexProxyAttestation validates and persists an explicit upstream route.
func SetCodexProxyAttestation(home, root, upstream string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("codex configuration root is empty")
	}
	_, proxyURL, routeErr := VerifyCodexConfigRoute(home, root)
	if routeErr != nil {
		return fmt.Errorf("proxy configuration: %w", routeErr)
	}
	proxyRoute, proxyErr := normalizeLoopbackRoute(proxyURL)
	if proxyErr != nil {
		return fmt.Errorf("proxy configuration: %w", proxyErr)
	}
	route, endpoint, err := api.NormalizeRoute(upstream)
	if err != nil {
		return fmt.Errorf("upstream endpoint: %w", err)
	}
	if !endpoint.IsFI || route != endpoint.Origin+api.CodexRoutePath {
		return errors.New("upstream endpoint is not an approved FreeInference Codex /v1 route")
	}
	path := codexProxyPath(home, root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(CodexProxyAttestation{UpstreamURL: upstream, ProxyURL: proxyRoute}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".codex-proxy-*")
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

// VerifyCodexConfigRoute classifies a Codex configuration route.
type CodexRouteState string

const (
	CodexRouteVerifiedDirect CodexRouteState = "verified-direct"
	CodexRouteVerifiedProxy  CodexRouteState = "verified-proxy"
	CodexRouteCandidate      CodexRouteState = "candidate-unverified"
	CodexRouteUnrelated      CodexRouteState = "unrelated"
)

// VerifyCodexConfigRoute reads config.toml and returns the selected provider
// route state. Loopback routes never auto-verify without an explicit FIC-owned
// upstream attestation.
func VerifyCodexConfigRoute(home, root string) (CodexRouteState, string, error) {
	path := filepath.Join(root, "config.toml")
	f, err := config.OpenNoFollow(path)
	if err != nil {
		return CodexRouteUnrelated, "", err
	}
	defer f.Close()
	data, err := readAllBounded(f)
	if err != nil {
		return CodexRouteUnrelated, "", err
	}
	_, baseURL, ok := parseSelectedCodexProvider(string(data))
	if !ok {
		return CodexRouteUnrelated, "", nil
	}
	route, endpoint, endpointErr := api.NormalizeRoute(baseURL)
	if endpointErr == nil && endpoint.IsFI && route == endpoint.Origin+api.CodexRoutePath {
		return CodexRouteVerifiedDirect, baseURL, nil
	}
	if !isLoopbackURL(baseURL) {
		return CodexRouteUnrelated, baseURL, nil
	}
	attestation, attestationErr := loadCodexProxyAttestation(home, root)
	if attestationErr != nil || attestation == nil {
		return CodexRouteCandidate, baseURL, nil
	}
	proxyRoute, proxyErr := normalizeLoopbackRoute(baseURL)
	if proxyErr != nil || attestation.ProxyURL != proxyRoute {
		return CodexRouteCandidate, baseURL, nil
	}
	upstreamRoute, upstream, upstreamErr := api.NormalizeRoute(attestation.UpstreamURL)
	if upstreamErr != nil || !upstream.IsFI || upstreamRoute != upstream.Origin+api.CodexRoutePath {
		return CodexRouteCandidate, baseURL, nil
	}
	return CodexRouteVerifiedProxy, baseURL, nil
}

func normalizeLoopbackRoute(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isLoopbackURL(raw) {
		return "", errors.New("selected route is not a valid loopback HTTP endpoint")
	}
	if u.RawPath != "" || strings.TrimRight(u.Path, "/") != api.CodexRoutePath {
		return "", errors.New("selected route must use the loopback /v1 path")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + api.CodexRoutePath, nil
}

func loadCodexProxyAttestation(home, root string) (*CodexProxyAttestation, error) {
	data, err := readBoundedNoFollow(codexProxyPath(home, root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var attestation CodexProxyAttestation
	if err := json.Unmarshal(data, &attestation); err != nil {
		return nil, err
	}
	return &attestation, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.HasSuffix(u.Host, ":") {
		return false
	}
	if u.Hostname() == "" || !isLoopbackHost(u.Hostname()) {
		return false
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return false
		}
	}
	return true
}

func readAllBounded(f *os.File) ([]byte, error) {
	const max = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, errors.New("config exceeds the supported size limit")
	}
	return data, nil
}

func parseSelectedCodexProvider(contents string) (string, string, bool) {
	var document struct {
		ModelProvider string `toml:"model_provider"`
		Providers     map[string]struct {
			BaseURL string `toml:"base_url"`
		} `toml:"model_providers"`
	}
	if err := toml.Unmarshal([]byte(contents), &document); err != nil {
		return "", "", false
	}
	selected := document.ModelProvider
	if strings.TrimSpace(selected) == "" {
		selected = "openai"
	}
	provider, ok := document.Providers[selected]
	return selected, provider.BaseURL, ok && strings.TrimSpace(provider.BaseURL) != ""
}
