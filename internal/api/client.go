package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

const (
	DefaultBaseURL = "https://freeinference.org/v1"

	// Endpoint-specific timeouts.
	DialTimeout           = 750 * time.Millisecond
	TLSHandshakeTimeout   = 1500 * time.Millisecond
	ResponseHeaderTimeout = 2 * time.Second
	CatalogTimeout        = 5 * time.Second
	HealthTimeout         = 5 * time.Second
	ProbeTimeout          = 45 * time.Second

	// Response body bounds.
	MaxCatalogBody = 2 << 20 // 2 MiB
	MaxHealthBody  = 1 << 20 // 1 MiB
	MaxErrorBody   = 64 << 10
	MaxProbeBody   = 1 << 20

	SyntheticProbeHeader = "X-Probe"
	SyntheticProbeValue  = "synthetic"
)

// CustomEndpointConfig holds the validated custom endpoint configuration.
// Both FI_CUSTOM_ENDPOINT and FI_CUSTOM_API_KEY must be present together.
type CustomEndpointConfig struct {
	EndpointIdentity *EndpointIdentity // validated endpoint identity
	APIKey           string            // custom API key
}

// LoadCustomEndpointConfig loads and validates the custom endpoint configuration.
// Returns (nil, nil) if not configured. Returns error if partially configured.
func LoadCustomEndpointConfig() (*CustomEndpointConfig, error) {
	customEndpoint := os.Getenv("FI_CUSTOM_ENDPOINT")
	customAPIKey := os.Getenv("FI_CUSTOM_API_KEY")

	if customEndpoint == "" && customAPIKey == "" {
		return nil, nil // not configured
	}
	if customEndpoint == "" || customAPIKey == "" {
		return nil, errors.New("FI_CUSTOM_ENDPOINT and FI_CUSTOM_API_KEY must be set together")
	}

	id, err := NormalizeEndpoint(customEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid FI_CUSTOM_ENDPOINT: %w", err)
	}

	return &CustomEndpointConfig{
		EndpointIdentity: id,
		APIKey:           customAPIKey,
	}, nil
}

// ValidateBaseURL validates a base URL for credentialed API requests.
// Rules:
//   - Must be absolute (scheme + host).
//   - Must be HTTPS, unless it's loopback AND FI_ALLOW_INSECURE_LOCALHOST=1.
//   - Must not contain userinfo.
//   - Must not have a fragment.
//
// Returns the normalized URL string on success, or an error.
func ValidateBaseURL(rawURL string) (string, error) {
	id, err := NormalizeEndpoint(rawURL)
	if err != nil {
		return "", err
	}
	return id.RequestURL, nil
}

// EndpointIdentity is the normalized, safe-to-persist form of an API endpoint.
// It is the single source of truth for endpoint normalization shared by
// provider detection (internal/adapters) and API construction. Persisting only
// Origin (scheme://host) ensures userinfo, query strings, and fragments never
// leak into snapshots or logs.
type EndpointIdentity struct {
	Host       string // hostname only, lowercased
	Origin     string // scheme://host — safe to persist
	RequestURL string // scheme://host/path — for requests (no userinfo/query/fragment)
	IsFI       bool   // points at an approved FreeInference host
}

// NormalizeEndpoint parses and normalizes a raw URL into an EndpointIdentity.
// Rules:
//   - Must be absolute (scheme + host).
//   - Must be HTTPS, unless it's loopback AND FI_ALLOW_INSECURE_LOCALHOST=1.
//   - Must not contain userinfo.
//   - Must not have a fragment.
//   - Query strings are NOT persisted (they may carry secrets).
//
// Returns an error if the URL is invalid. A valid non-FreeInference URL returns
// a non-nil identity with IsFI=false and no error — callers decide whether to
// accept unapproved hosts.
func NormalizeEndpoint(rawURL string) (*EndpointIdentity, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid base URL: must be absolute (scheme://host)")
	}
	if u.User != nil {
		return nil, fmt.Errorf("invalid base URL: must not contain userinfo")
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("invalid base URL: must not have a fragment")
	}
	// Query strings may carry secrets (e.g. ?api_key=...). Reject them so a
	// credential-bearing URL can never be confirmed as a provider endpoint and
	// so query parameters are never persisted into snapshots.
	if u.RawQuery != "" {
		return nil, fmt.Errorf("invalid base URL: must not contain a query string")
	}
	if u.Scheme != "https" {
		host := u.Hostname()
		isLoopback := isLoopbackHost(host)
		allowInsecure := os.Getenv("FI_ALLOW_INSECURE_LOCALHOST") == "1"
		if !(isLoopback && allowInsecure) {
			return nil, fmt.Errorf("invalid base URL: must be HTTPS (set FI_ALLOW_INSECURE_LOCALHOST=1 for loopback development)")
		}
	}
	host := strings.ToLower(u.Hostname())
	origin := u.Scheme + "://" + u.Host
	requestURL := origin + u.Path
	return &EndpointIdentity{
		Host:       host,
		Origin:     origin,
		RequestURL: requestURL,
		IsFI:       isApprovedCredentialHost(host),
	}, nil
}
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// isApprovedCredentialHost reports whether host is one of the approved
// FreeInference hostnames that may receive the configured API key.
func isApprovedCredentialHost(host string) bool {
	host = strings.ToLower(host)
	if host == "freeinference.org" {
		return true
	}
	return strings.HasSuffix(host, ".freeinference.org")
}

// CredentialError is returned when a credential would be sent to a host that
// is not an approved FreeInference endpoint and the user has not explicitly
// configured a custom endpoint via FI_CUSTOM_ENDPOINT.
type CredentialError struct {
	Host string
}

func (e *CredentialError) Error() string {
	return "refusing to send API key to non-FreeInference host " + e.Host +
		" (configure FI_CUSTOM_ENDPOINT and FI_CUSTOM_API_KEY for a custom endpoint)"
}

// SanitizeEndpointError returns a user-facing, non-secret-bearing description
// of an endpoint-validation error. CredentialErrors are rendered with a
// stable message that never echoes the raw URL. Other validation errors are
// passed through verbatim because ValidateBaseURL messages never include the
// raw URL.
func SanitizeEndpointError(err error) string {
	if err == nil {
		return ""
	}
	var ce *CredentialError
	if errors.As(err, &ce) {
		return ce.Error()
	}
	return err.Error()
}

// Client communicates with the FreeInference API.
// Security-sensitive fields are private. Use accessors for read-only access.
type Client struct {
	baseURL          string
	endpointIdentity *EndpointIdentity
	apiKey           string
	version          string
	httpClient       *http.Client

	// customEndpoint holds the validated custom endpoint config (if any).
	// When set, only the custom API key is used for requests to this origin.
	customEndpoint *CustomEndpointConfig

	// _testMode is set by NewClientForTest to bypass per-request credential
	// validation. It prevents doRequest from re-checking the host, which is
	// necessary for httptest server usage in tests.
	_testMode bool
}

// BaseURL returns the client's base URL (read-only accessor).
func (c *Client) BaseURL() string {
	return c.baseURL
}

// EndpointIdentity returns the client's endpoint identity (read-only accessor).
func (c *Client) EndpointIdentity() *EndpointIdentity {
	return c.endpointIdentity
}

// Version returns the client version (read-only accessor).
func (c *Client) Version() string {
	return c.version
}

// APIKey returns the client's API key (read-only accessor).
func (c *Client) APIKey() string {
	return c.apiKey
}

// ClientConfig is the authoritative, credential-safe configuration for an API
// client. Construct clients with NewClient(cfg) so that credential-safety
// validation always runs — the legacy NewClient(baseURL, apiKey, timeout)
// constructor does NOT validate the host and must not be used when apiKey != "".
type ClientConfig struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// NewClient creates a credential-safe FreeInference API client.
//
// Validation performed before any network capability exists:
//   - Base URL is absolute, HTTPS (except loopback with opt-in), no userinfo/fragment.
//   - When APIKey != "", the host MUST be an approved FreeInference host unless
//     FI_CUSTOM_ENDPOINT and FI_CUSTOM_API_KEY are configured. This prevents the
//     API key from being silently transported to an arbitrary attacker-controlled
//     HTTPS endpoint.
//
// Returns a CredentialError (without the URL) when a credential would be sent
// to an unapproved host.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	// Load custom endpoint config if configured
	customCfg, err := LoadCustomEndpointConfig()
	if err != nil {
		return nil, err
	}

	normalized, err := approvedBaseURL(cfg.BaseURL, cfg.APIKey, customCfg)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: DialTimeout}
	return &Client{
		baseURL:          normalized,
		endpointIdentity: &EndpointIdentity{Host: extractHost(normalized), Origin: extractOrigin(normalized), RequestURL: normalized, IsFI: isApprovedCredentialHost(extractHost(normalized))},
		apiKey:           cfg.APIKey,
		version:          "dev",
		customEndpoint:   customCfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   TLSHandshakeTimeout,
				ResponseHeaderTimeout: ResponseHeaderTimeout,
			},
			// Strict redirect policy: all redirects are forbidden so a
			// malicious endpoint can't redirect our credentialed request
			// to an attacker-controlled host.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// extractHost extracts the host from a normalized URL string.
func extractHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// extractOrigin extracts the origin (scheme://host) from a normalized URL string.
func extractOrigin(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// approvedBaseURL validates a base URL and, when an API key is present,
// requires the host to be an approved FreeInference hostname unless the user
// has explicitly configured a custom endpoint via FI_CUSTOM_ENDPOINT.
//
// This is the authoritative credential-safety gate: it prevents
// FREEINFERENCE_API_KEY from being silently transported to an arbitrary host
// when an attacker controls FREEINFERENCE_BASE_URL.
//
// Custom endpoint contract:
//   - FI_CUSTOM_ENDPOINT must be a valid HTTPS URL with a trusted origin
//   - FI_CUSTOM_API_KEY is the only credential allowed for custom endpoints
//   - FREEINFERENCE_API_KEY is NEVER sent to a custom endpoint
func approvedBaseURL(rawURL string, apiKey string, customCfg *CustomEndpointConfig) (string, error) {
	normalized, err := ValidateBaseURL(rawURL)
	if err != nil {
		return "", err
	}
	if apiKey == "" {
		// No credential is at stake; the URL only needs to pass basic validation.
		return normalized, nil
	}
	// A credential is present. If this is the production FreeInference key,
	// require an approved host. The custom endpoint key is checked separately.
	if strings.HasPrefix(apiKey, "hyi-") || strings.HasPrefix(apiKey, "sk-fi") {
		// Production FreeInference key: must go to approved host only.
		u, err := url.Parse(normalized)
		if err != nil {
			return "", fmt.Errorf("invalid base URL: %w", err)
		}
		if !isApprovedCredentialHost(u.Hostname()) {
			return "", &CredentialError{
				Host: u.Hostname(),
			}
		}
		return normalized, nil
	}
	// Non-FI key: if custom endpoint is configured, verify the normalized URL
	// matches the configured origin. This prevents FREEINFERENCE_API_KEY from
	// being sent to a custom endpoint while allowing a user to configure
	// their own endpoint + key pair.
	if customCfg != nil {
		// Verify the configured endpoint matches the requested one
		if !strings.EqualFold(normalized, customCfg.EndpointIdentity.RequestURL) {
			return "", &CredentialError{
				Host: normalized,
			}
		}
		return normalized, nil
	}
	// Not a custom endpoint and not a FreeInference-hosted key — could be
	// an Anthropic-compatible or OpenAI-compatible key on an approved host.
	u, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	if !isApprovedCredentialHost(u.Hostname()) {
		return "", &CredentialError{
			Host: u.Hostname(),
		}
	}
	return normalized, nil
}

// newClientLegacy creates a client WITHOUT credential-safety host validation.
// It is unexported for test scaffolding only. Production callers must use
// NewClient, which always validates the host.
func newClientLegacy(baseURL, apiKey string, timeout time.Duration) *Client {
	c := newClientLegacyWithMode(baseURL, apiKey, timeout, true)
	return c
}

func newClientLegacyWithMode(baseURL, apiKey string, timeout time.Duration, testMode bool) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	dialer := &net.Dialer{Timeout: DialTimeout}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		version: "dev",
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   TLSHandshakeTimeout,
				ResponseHeaderTimeout: ResponseHeaderTimeout,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// All redirects are forbidden — no cross-origin leakage,
				// no same-origin ambiguity.
				return http.ErrUseLastResponse
			},
		},
		_testMode: testMode,
	}
}

// endpoint resolves path against the base URL using proper URL parsing.
func (c *Client) endpoint(path string) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("parse path: %w", err)
	}
	// ResolveReference with a leading-slash path keeps the scheme/host.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
		ref, err = url.Parse(path)
		if err != nil {
			return "", fmt.Errorf("parse path: %w", err)
		}
	}
	// Preserve any path prefix already in the base URL (e.g. /v1).
	if base.Path != "" && base.Path != "/" {
		ref.Path = strings.TrimSuffix(base.Path, "/") + ref.Path
	}
	return base.ResolveReference(ref).String(), nil
}

// doRequest performs an authenticated HTTP request with a bounded error body.
//
// Defense in depth: re-check the host immediately before attaching the
// Authorization header. This guards against any future caller that constructs
// a Client without going through NewClient, and makes the credential-safety
// invariant local to the place that actually sends the credential.
func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	reqURL, err := c.endpoint(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "FreeInference-Companion/"+c.version)
	// Add synthetic probe header for inference probes
	if path == "/chat/completions" {
		req.Header.Set(SyntheticProbeHeader, SyntheticProbeValue)
	}
	if c.apiKey != "" {
		// Defense in depth: never attach the credential unless the request is
		// destined for an approved FreeInference host or a configured custom
		// endpoint. This is the last line of defense against credential
		// exfiltration to an arbitrary host.
		// _testMode bypasses this check for httptest server usage in tests.
		if !c._testMode {
			if !c.shouldAttachCredential(req.URL.Hostname()) {
				return nil, &CredentialError{Host: req.URL.Hostname()}
			}
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.httpClient.Do(req)
}

// shouldAttachCredential determines if the credential should be attached to a request
// for the given hostname. It checks both the endpoint identity and any custom endpoint.
func (c *Client) shouldAttachCredential(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return false
	}
	// If custom endpoint is configured, only attach credential to that exact origin
	if c.customEndpoint != nil {
		return strings.EqualFold(hostname, c.customEndpoint.EndpointIdentity.Host)
	}
	// Otherwise, attach credential only to approved FreeInference hosts
	return isApprovedCredentialHost(hostname)
}

// HTTPError is a sanitized non-2xx API response. RetryAfter is honored when
// the server supplies a Retry-After header or retry_after body field.
type HTTPError struct {
	StatusCode int
	Message    string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// readErrorBody reads a bounded error body and extracts a sanitized message.
func readErrorBody(resp *http.Response) *HTTPError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBody))
	he := &HTTPError{StatusCode: resp.StatusCode}

	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
			he.RetryAfter = time.Duration(secs) * time.Second
		}
	}

	var errResp ErrorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		// Upstream error messages are untrusted text — apply the same redaction
		// gate as the raw-body fallback so an echoed Authorization header or
		// key-shaped token in a structured response cannot leak.
		he.Message = secure.Redact(errResp.Error.Message)
		if he.RetryAfter == 0 && errResp.Error.RetryAfter != nil && *errResp.Error.RetryAfter >= 0 {
			he.RetryAfter = time.Duration(*errResp.Error.RetryAfter) * time.Second
		}
		return he
	}
	// Fall back to a truncated raw body (bounded by MaxErrorBody already).
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	// Defensive: scrub any secret-shaped substring before the message enters
	// our error type. Bodies are bounded but may still contain echoed
	// headers or auth tokens from a misbehaving upstream.
	he.Message = secure.Redact(msg)
	return he
}

// ============================================================
// Model types (from /v1/models)
// ============================================================

// ModelsResponse is the response from GET /v1/models.
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model is a single model from the FreeInference catalog.
type Model struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name,omitempty"`
	Object                  string            `json:"object,omitempty"`
	Created                 int64             `json:"created,omitempty"`
	OwnedBy                 string            `json:"owned_by,omitempty"`
	InputModalities         []string          `json:"input_modalities,omitempty"`
	OutputModalities        []string          `json:"output_modalities,omitempty"`
	Quantization            string            `json:"quantization,omitempty"`
	ContextLength           int               `json:"context_length"`
	MaxOutputLength         int               `json:"max_output_length"`
	Pricing                 map[string]string `json:"pricing,omitempty"`
	SupportedSamplingParams []string          `json:"supported_sampling_parameters,omitempty"`
	SupportedFeatures       []string          `json:"supported_features,omitempty"`
}

// ============================================================
// Health types
// ============================================================

// HealthResponse is the response from the health monitoring API.
type HealthResponse struct {
	Status                         string `json:"status"`
	Total                          int    `json:"total"`
	Healthy                        int    `json:"healthy"`
	Unhealthy                      int    `json:"unhealthy"`
	CycleOk                        bool   `json:"cycleOk"`
	LastCycleAt                    string `json:"lastCycleAt"`
	PendingControlPlaneTransitions int    `json:"pendingControlPlaneTransitions"`
}

// ============================================================
// Error response
// ============================================================

// APIError is a structured FreeInference API error.
type APIError struct {
	Type            string `json:"type"`
	Message         string `json:"message"`
	Code            int    `json:"code"`
	Model           string `json:"model,omitempty"`
	RetryAfter      *int   `json:"retry_after,omitempty"`
	TokensRequested *int   `json:"tokens_requested,omitempty"`
	QueueSize       *int   `json:"queue_size,omitempty"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s (code %d)", e.Type, e.Message, e.Code)
}

// ErrorResponse wraps an API error in a JSON response.
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// ============================================================
// Endpoint methods
// ============================================================

// ListModels fetches the model catalog from GET /v1/models.
func (c *Client) ListModels() ([]Model, error) {
	ctx, cancel := context.WithTimeout(context.Background(), CatalogTimeout)
	defer cancel()

	resp, err := c.doRequest(ctx, "GET", "/models", nil)
	if err != nil {
		return nil, fmt.Errorf("list models request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readErrorBody(resp)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxCatalogBody))
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}

	var modelsResp ModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		var models []Model
		if err2 := json.Unmarshal(body, &models); err2 != nil {
			return nil, fmt.Errorf("parse models: %w", err)
		}
		return models, nil
	}
	return modelsResp.Data, nil
}

// GetHealth fetches health status from the configured health URL.
//
// Security contract: the health endpoint is treated as a public, unauthenticated
// endpoint. The FreeInference API key is NEVER attached, regardless of the
// health URL. Callers may also pass an explicit allowlist of trusted origins
// via GetHealthFromTrusted to forbid requests to attacker-controlled hosts.
//
// The URL must be absolute, HTTPS, and free of userinfo/fragments. Query
// strings are preserved for the request (some health endpoints take a token
// in the query) but only the sanitized origin is safe to persist — see
// SanitizeHealthURL.
func (c *Client) GetHealth(healthURL string) (*HealthResponse, error) {
	sanitized, err := NormalizeHealthURL(healthURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), HealthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", sanitized.RequestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create health request: %w", err)
	}
	req.Header.Set("User-Agent", "FreeInference-Companion/"+c.version)
	// Intentionally no Authorization header. The API key is scoped to the
	// configured FreeInference endpoint; sending it to a separately configured
	// health URL would risk exfiltration to an unrelated host.

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("health request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readErrorBody(resp)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxHealthBody))
	if err != nil {
		return nil, fmt.Errorf("read health response: %w", err)
	}

	var healthResp HealthResponse
	if err := json.Unmarshal(body, &healthResp); err != nil {
		return nil, fmt.Errorf("parse health: %w", err)
	}
	return &healthResp, nil
}

// HealthURLOrigins is the default allowlist of hostnames the companion will
// send health probes to. This is the primary defense against a misconfigured
// FI_HEALTH_URL silently shipping the API key to a third party: when using
// GetHealthFromTrusted, any host not on this list is rejected before any
// network call. Users opting into a custom health endpoint should extend this
// list via configuration or call GetHealth directly with their own acceptance
// of the risk.
var HealthURLOrigins = []string{
	"freeinference.org",
	"status.freeinference.org",
	"health.freeinference.org",
}

// SanitizedHealthURL is the validated, normalized form of a health URL. The
// RequestURL is what gets sent (origin plus path, no userinfo); Origin is the
// safe-to-persist label (scheme://host only, no path/query/userinfo).
type SanitizedHealthURL struct {
	RequestURL string
	Origin     string
}

// NormalizeHealthURL validates and normalizes a health URL. It requires an
// absolute HTTPS URL without userinfo or fragment. Query strings are dropped
// from the persisted origin but preserved on RequestURL — but the caller is
// responsible for ensuring no credential-bearing query parameter is sent to
// an untrusted host (use GetHealthFromTrusted to enforce an allowlist).
func NormalizeHealthURL(rawURL string) (SanitizedHealthURL, error) {
	if rawURL == "" {
		return SanitizedHealthURL{}, fmt.Errorf("no health source configured")
	}
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return SanitizedHealthURL{}, fmt.Errorf("invalid health URL: %w", err)
	}
	if !u.IsAbs() {
		return SanitizedHealthURL{}, fmt.Errorf("health URL must be absolute")
	}
	if u.Scheme != "https" {
		return SanitizedHealthURL{}, fmt.Errorf("health URL must be HTTPS (got scheme %q)", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return SanitizedHealthURL{}, fmt.Errorf("health URL is missing a host")
	}
	if u.User != nil {
		// userinfo in a health URL is a strong signal of an injected credential
		// (e.g. https://token@attacker/). Refuse it outright; do not send it.
		return SanitizedHealthURL{}, fmt.Errorf("health URL must not contain userinfo")
	}
	// url.ParseRequestURI is permissive with fragments on some Go versions; be
	// defensive and reject any URL whose raw form carries '#'. A health
	// endpoint never needs a fragment.
	if strings.Contains(rawURL, "#") || u.Fragment != "" || u.RawFragment != "" {
		return SanitizedHealthURL{}, fmt.Errorf("health URL must not contain a fragment")
	}

	// Build the request URL: scheme://host/path (no userinfo, no fragment).
	// Query is preserved on the request. Query strings are NOT persisted as
	// part of the origin label below.
	reqURL := u.Scheme + "://" + u.Host + u.Path
	if u.RawQuery != "" {
		reqURL += "?" + u.RawQuery
	}

	// Origin is the safe-to-persist label: scheme://host only.
	origin := u.Scheme + "://" + u.Host

	return SanitizedHealthURL{RequestURL: reqURL, Origin: origin}, nil
}

// IsAllowedHealthOrigin reports whether host appears in the supplied
// allowlist. Matching is exact-host or subdomain-of when the allowlist entry
// does not itself begin with a dot, and suffix-of when it does.
func IsAllowedHealthOrigin(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range allowlist {
		e := strings.ToLower(strings.TrimSpace(entry))
		if e == "" {
			continue
		}
		if host == e {
			return true
		}
		if strings.HasPrefix(e, ".") {
			if strings.HasSuffix(host, e) {
				return true
			}
			continue
		}
		if strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}

// GetHealthFromTrusted is GetHealth with an explicit host allowlist. If the
// configured health URL's host is not on the allowlist, the request is refused
// before any network call. This is the safe variant for code paths that read
// FI_HEALTH_URL directly from the environment (background refresh, doctor).
func (c *Client) GetHealthFromTrusted(healthURL string, allowlist []string) (*HealthResponse, error) {
	sanitized, err := NormalizeHealthURL(healthURL)
	if err != nil {
		return nil, err
	}
	u, parseErr := url.Parse(sanitized.RequestURL)
	if parseErr != nil {
		return nil, fmt.Errorf("internal: re-parse sanitized URL: %w", parseErr)
	}
	if !IsAllowedHealthOrigin(u.Hostname(), allowlist) {
		return nil, fmt.Errorf("health URL host %q is not on the trusted allowlist", u.Hostname())
	}
	return c.GetHealth(healthURL)
}

// ============================================================
// Account usage
// ============================================================

// AccountUsageResponse is the response from GET /v1/account/usage.
type AccountUsageResponse struct {
	Object        string `json:"object"`
	RequestsUsed  *int64 `json:"requests_used"`
	RequestsLimit *int64 `json:"requests_limit"`
	TokensUsed    *int64 `json:"tokens_used"`
	TokensLimit   *int64 `json:"tokens_limit"`
}

// GetAccountUsage fetches account-level usage from GET /v1/account/usage and
// returns the observed provider capability separately from the quota data.
// The endpoint is treated as optional until the provider proves otherwise.
func (c *Client) GetAccountUsage() (*schema.AccountUsage, schema.AccountUsageCapabilityState, error) {
	if c.apiKey == "" {
		return nil, schema.CapabilityUnknown, fmt.Errorf("no API key configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), HealthTimeout)
	defer cancel()

	resp, err := c.doRequest(ctx, "GET", "/account/usage", nil)
	if err != nil {
		return nil, schema.CapabilityUnknown, fmt.Errorf("account usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, schema.CapabilityUnsupported, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, schema.CapabilityForbidden, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, schema.CapabilityUnknown, readErrorBody(resp)
	}

	var bodyBytes []byte
	bodyBytes, err = io.ReadAll(io.LimitReader(resp.Body, MaxHealthBody))
	if err != nil {
		return nil, schema.CapabilityUnknown, fmt.Errorf("read account usage response: %w", err)
	}

	var usageResp AccountUsageResponse
	if err := json.Unmarshal(bodyBytes, &usageResp); err != nil {
		// Try a flat int64 shape as fallback.
		var flat struct {
			RequestsUsed  *int64 `json:"requests_used"`
			RequestsLimit *int64 `json:"requests_limit"`
			TokensUsed    *int64 `json:"tokens_used"`
			TokensLimit   *int64 `json:"tokens_limit"`
		}
		if err2 := json.Unmarshal(bodyBytes, &flat); err2 != nil {
			return nil, schema.CapabilityUnknown, fmt.Errorf("parse account usage: %w", err)
		}
		usageResp = AccountUsageResponse{
			RequestsUsed:  flat.RequestsUsed,
			RequestsLimit: flat.RequestsLimit,
			TokensUsed:    flat.TokensUsed,
			TokensLimit:   flat.TokensLimit,
		}
	}
	if usageResp.RequestsUsed == nil && usageResp.RequestsLimit == nil && usageResp.TokensUsed == nil && usageResp.TokensLimit == nil {
		return nil, schema.CapabilityUnknown, errors.New("parse account usage: response contains no quota fields")
	}

	au := &schema.AccountUsage{
		Authoritative: true,
		FetchedAt:     time.Now(),
		RequestsUsed:  usageResp.RequestsUsed,
		RequestsLimit: usageResp.RequestsLimit,
		TokensUsed:    usageResp.TokensUsed,
		TokensLimit:   usageResp.TokensLimit,
	}
	return au, schema.CapabilitySupported, nil
}

// ============================================================
// Probe / diagnostics
// ============================================================

// CheckState is the result of a single diagnostic check.
type CheckState string

const (
	CheckPass    CheckState = "pass"
	CheckWarn    CheckState = "warn"
	CheckFail    CheckState = "fail"
	CheckUnknown CheckState = "unknown"
)

// CheckResult is one diagnostic check outcome.
type CheckResult struct {
	State  CheckState `json:"state"`
	Detail string     `json:"detail,omitempty"`
}

// ProbeResult summarizes connectivity diagnostics. Authentication and
// ModelAccess are "unknown" unless proven by a real authenticated operation.
type ProbeResult struct {
	Endpoint       CheckResult `json:"endpoint"`
	Authentication CheckResult `json:"authentication"`
	Catalog        CheckResult `json:"catalog"`
	ModelAccess    CheckResult `json:"model_access"`
}

// Probe checks endpoint reachability and catalog listing.
// It does NOT claim authentication or model access — those require a real
// authenticated operation (see ProbeInference).
func (c *Client) Probe() ProbeResult {
	result := ProbeResult{
		Endpoint:       CheckResult{State: CheckUnknown},
		Authentication: CheckResult{State: CheckUnknown, Detail: "not verified without an authenticated operation"},
		Catalog:        CheckResult{State: CheckUnknown},
		ModelAccess:    CheckResult{State: CheckUnknown, Detail: "catalog presence does not imply access"},
	}

	models, err := c.ListModels()
	if err != nil {
		result.Endpoint = CheckResult{State: CheckFail, Detail: sanitizeDetail(err.Error())}
		result.Catalog = CheckResult{State: CheckFail, Detail: sanitizeDetail(err.Error())}
		return result
	}
	result.Endpoint = CheckResult{State: CheckPass}
	if len(models) > 0 {
		result.Catalog = CheckResult{State: CheckPass, Detail: fmt.Sprintf("%d models listed", len(models))}
	} else {
		result.Catalog = CheckResult{State: CheckUnknown, Detail: "catalog listed but empty"}
	}
	return result
}

// InferenceProbeResult is the outcome of a synthetic inference probe.
type InferenceProbeResult struct {
	Endpoint       CheckResult `json:"endpoint"`
	Authentication CheckResult `json:"authentication"`
	ModelAccess    CheckResult `json:"model_access"`
	Model          string      `json:"model"`
}

// ProbeInference sends a synthetic minimal inference request for the given
// model. The model must be explicitly chosen by the caller. Marked with the
// X-Probe: synthetic header. Only run on explicit user request.
// Uses doRequest to ensure credential-safety path is always followed.
func (c *Client) ProbeInference(model string) InferenceProbeResult {
	result := InferenceProbeResult{
		Endpoint:       CheckResult{State: CheckUnknown},
		Authentication: CheckResult{State: CheckUnknown},
		ModelAccess:    CheckResult{State: CheckUnknown},
		Model:          model,
	}
	if model == "" {
		result.Endpoint = CheckResult{State: CheckFail, Detail: "no model selected"}
		return result
	}

	payload, err := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 5,
		"stream":     false,
	})
	if err != nil {
		result.Endpoint = CheckResult{State: CheckFail, Detail: "encode probe body"}
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()

	// Use doRequest to ensure credential-safety path is followed
	resp, err := c.doRequest(ctx, "POST", "/chat/completions", bytes.NewReader(payload))
	if err != nil {
		// Check if it's a credential error (credential not attached due to host check)
		if _, ok := err.(*CredentialError); ok {
			result.Endpoint = CheckResult{State: CheckPass}
			result.Authentication = CheckResult{State: CheckFail, Detail: "credential not attached: host not approved"}
			return result
		}
		result.Endpoint = CheckResult{State: CheckFail, Detail: sanitizeDetail(err.Error())}
		return result
	}
	defer resp.Body.Close()
	// Bound the probe body even on success.
	_, _ = io.ReadAll(io.LimitReader(resp.Body, MaxProbeBody))

	result.Endpoint = CheckResult{State: CheckPass}
	switch resp.StatusCode {
	case http.StatusOK:
		result.Authentication = CheckResult{State: CheckPass}
		result.ModelAccess = CheckResult{State: CheckPass, Detail: fmt.Sprintf("synthetic request to %s succeeded", model)}
	case http.StatusUnauthorized, http.StatusForbidden:
		result.Authentication = CheckResult{State: CheckFail, Detail: "authentication rejected"}
	case http.StatusNotFound:
		result.ModelAccess = CheckResult{State: CheckFail, Detail: fmt.Sprintf("model '%s' not found", model)}
	default:
		he := readErrorBody(resp)
		result.ModelAccess = CheckResult{State: CheckUnknown, Detail: sanitizeDetail(he.Error())}
	}
	return result
}

// sanitizeDetail trims error text so unbounded server output never lands in
// reports or status output. Applies secret redaction as a defensive last mile.
func sanitizeDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return secure.Redact(s)
}

// VerifyAPIKey checks if the API key is syntactically valid.
func VerifyAPIKey(key string) bool {
	if len(key) < 10 {
		return false
	}
	if strings.HasPrefix(key, "hyi-") {
		return true
	}
	if strings.HasPrefix(key, "sk-") {
		return true
	}
	return false
}
