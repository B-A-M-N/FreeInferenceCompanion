package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
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

// Client communicates with the FreeInference API.
type Client struct {
	BaseURL    string
	APIKey     string
	Version    string
	HTTPClient *http.Client
}

// NewClient creates a new FreeInference API client.
// If apiKey is empty, requests are made without authentication.
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	dialer := &net.Dialer{Timeout: DialTimeout}
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Version: "dev",
		HTTPClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   TLSHandshakeTimeout,
				ResponseHeaderTimeout: ResponseHeaderTimeout,
			},
		},
	}
}

// endpoint resolves path against the base URL using proper URL parsing.
func (c *Client) endpoint(path string) (string, error) {
	base, err := url.Parse(c.BaseURL)
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
	req.Header.Set("User-Agent", "FreeInference-Companion/"+c.Version)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	return c.HTTPClient.Do(req)
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
	req.Header.Set("User-Agent", "FreeInference-Companion/"+c.Version)
	// Intentionally no Authorization header. The API key is scoped to the
	// configured FreeInference endpoint; sending it to a separately configured
	// health URL would risk exfiltration to an unrelated host.

	resp, err := c.HTTPClient.Do(req)
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

	reqURL, err := c.endpoint("/chat/completions")
	if err != nil {
		result.Endpoint = CheckResult{State: CheckFail, Detail: err.Error()}
		return result
	}
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(payload))
	if err != nil {
		result.Endpoint = CheckResult{State: CheckFail, Detail: "create probe request"}
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "FreeInference-Companion/"+c.Version)
	req.Header.Set(SyntheticProbeHeader, SyntheticProbeValue)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
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
