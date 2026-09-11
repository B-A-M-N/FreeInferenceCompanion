package clientenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/config"
)

// CodexProxyAttestation is an explicit declaration that a loopback Codex
// provider forwards to an approved FreeInference /v1 endpoint. It is stored in
// FIC-owned metadata, never written into the client's config.toml.
type CodexProxyAttestation struct {
	UpstreamURL string `json:"upstream_url"`
}

func codexProxyPath(home, root string) string {
	id, err := canonicalRootPath(root)
	if err != nil {
		id = filepath.Clean(root)
	}
	name := strings.ReplaceAll(strings.TrimLeft(id, "/"), "/", "_")
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
	endpoint, err := api.NormalizeEndpoint(upstream)
	if err != nil {
		return fmt.Errorf("upstream endpoint: %w", err)
	}
	if !endpoint.IsFI || endpoint.RequestURL != endpoint.Origin+"/v1" {
		return errors.New("upstream endpoint is not an approved FreeInference /v1 route")
	}
	path := codexProxyPath(home, root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(CodexProxyAttestation{UpstreamURL: upstream}, "", "  ")
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
	endpoint, endpointErr := api.NormalizeEndpoint(baseURL)
	if endpointErr == nil && endpoint.IsFI && endpoint.RequestURL == endpoint.Origin+"/v1" {
		return CodexRouteVerifiedDirect, baseURL, nil
	}
	if !isLoopbackURL(baseURL) {
		return CodexRouteUnrelated, baseURL, nil
	}
	attestation, attestationErr := loadCodexProxyAttestation(home, root)
	if attestationErr != nil || attestation == nil {
		return CodexRouteCandidate, baseURL, nil
	}
	upstream, upstreamErr := api.NormalizeEndpoint(attestation.UpstreamURL)
	if upstreamErr != nil || !upstream.IsFI || upstream.RequestURL != upstream.Origin+"/v1" {
		return CodexRouteCandidate, baseURL, nil
	}
	return CodexRouteVerifiedProxy, baseURL, nil
}

func loadCodexProxyAttestation(home, root string) (*CodexProxyAttestation, error) {
	data, err := os.ReadFile(codexProxyPath(home, root))
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

func isLoopbackURL(raw string) bool {
	host := strings.TrimSpace(strings.ToLower(hostOfURL(raw)))
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}

func hostOfURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if after, ok := strings.CutPrefix(trimmed, "http://"); ok {
		trimmed = after
	} else if after, ok := strings.CutPrefix(trimmed, "https://"); ok {
		trimmed = after
	}
	if slash := strings.IndexByte(trimmed, '/'); slash >= 0 {
		trimmed = trimmed[:slash]
	}
	// Strip port. IPv6 hosts in this comparison are either bare ::1 or
	// bracketed; a simple suffix cut is sufficient for loopback detection.
	if colon := strings.LastIndexByte(trimmed, ':'); colon >= 0 && !strings.Contains(trimmed, "]") {
		trimmed = trimmed[:colon]
	}
	return strings.Trim(trimmed, "[]")
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
	table := ""
	selected := ""
	providers := map[string]string{}
	for _, line := range strings.Split(contents, "\n") {
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
			selected = value
		}
		if strings.HasPrefix(table, "model_providers.") && key == "base_url" {
			providers[strings.TrimPrefix(table, "model_providers.")] = value
		}
	}
	if selected == "" {
		selected = "openai"
	}
	base, ok := providers[selected]
	return selected, base, ok
}
