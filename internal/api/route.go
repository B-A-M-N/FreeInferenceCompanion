package api

import "strings"

// Route constants identify the supported client-facing runtime paths. The
// management catalog remains /v1 for both clients, while Claude Code uses the
// Anthropic-compatible runtime path.
const (
	CodexRoutePath  = "/v1"
	ClaudeRoutePath = "/anthropic"
)

// NormalizeRoute returns the canonical route for a validated endpoint by
// removing every trailing slash. The implementation is the single source of
// truth used by discovery, explicit integration, runtime activation, proxy
// attestation, doctor, and diagnose.
func NormalizeRoute(rawURL string) (string, *EndpointIdentity, error) {
	endpoint, err := NormalizeEndpoint(rawURL)
	if err != nil {
		return "", nil, err
	}
	path := strings.TrimPrefix(endpoint.RequestURL, endpoint.Origin)
	path = strings.TrimRight(path, "/")
	return endpoint.Origin + path, endpoint, nil
}

// IsFIRoute reports whether rawURL resolves to an approved FreeInference host
// and exactly matches the expected canonical route.
func IsFIRoute(rawURL, expectedPath string) bool {
	route, endpoint, err := NormalizeRoute(rawURL)
	return err == nil && endpoint.IsFI && route == endpoint.Origin+expectedPath
}
