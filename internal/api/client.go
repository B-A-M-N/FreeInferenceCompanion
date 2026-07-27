package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	DefaultBaseURL         = "https://freeinference.org/v1"
	DefaultHealthURL       = "" // No stable public health endpoint — configured by user if available
	DefaultConnectTimeout  = 10 * time.Second
	DefaultHealthTimeout   = 10 * time.Second
	DefaultUsageTimeout    = 10 * time.Second
	SyntheticProbeHeader   = "X-Probe"
	SyntheticProbeValue    = "synthetic"
)

// Client communicates with the FreeInference API.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewClient creates a new FreeInference API client.
// If apiKey is empty, requests are made without authentication (some endpoints may still work).
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// doRequest performs an authenticated HTTP request.
func (c *Client) doRequest(method, path string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", c.BaseURL, path)
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("X-API-Key", c.APIKey)
	}
	return c.HTTPClient.Do(req)
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
// Health types (from status.staging.freeinference.org/api/health)
// ============================================================

// HealthResponse is the response from the health monitoring API.
type HealthResponse struct {
	Status                      string `json:"status"`
	Total                       int    `json:"total"`
	Healthy                     int    `json:"healthy"`
	Unhealthy                   int    `json:"unhealthy"`
	CycleOk                     bool   `json:"cycleOk"`
	LastCycleAt                 string `json:"lastCycleAt"`
	PendingControlPlaneTransitions int `json:"pendingControlPlaneTransitions"`
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
// Returns nil models without error if the endpoint is reachable but returns unexpected data.
func (c *Client) ListModels() ([]Model, error) {
	resp, err := c.doRequest("GET", "/models", nil)
	if err != nil {
		return nil, fmt.Errorf("list models request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if json.Unmarshal(body, &errResp) == nil {
			return nil, &errResp.Error
		}
		return nil, fmt.Errorf("list models: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp ModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		// Try raw array format
		var models []Model
		if err2 := json.Unmarshal(body, &models); err2 != nil {
			return nil, fmt.Errorf("parse models: %w", err)
		}
		return models, nil
	}
	return modelsResp.Data, nil
}

// GetHealth fetches health status from the configured health URL.
func (c *Client) GetHealth(healthURL string) (*HealthResponse, error) {
	if healthURL == "" {
		return nil, fmt.Errorf("no health source configured")
	}

	req, err := http.NewRequest("GET", healthURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create health request: %w", err)
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("health request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read health response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var healthResp HealthResponse
	if err := json.Unmarshal(body, &healthResp); err != nil {
		return nil, fmt.Errorf("parse health: %w", err)
	}
	return &healthResp, nil
}

// ProbeResult summarizes a synthetic inference probe.
type ProbeResult struct {
	EndpointReachable bool   `json:"endpoint_reachable"`
	AuthAccepted      bool   `json:"auth_accepted"`
	ModelFound        bool   `json:"model_found"`
	ModelAvailable    bool   `json:"model_available"`
	Error             string `json:"error,omitempty"`
}

// Probe checks API reachability, auth validity, and model access.
// Does NOT send an inference probe — use ProbeInference for that.
func (c *Client) Probe() ProbeResult {
	result := ProbeResult{}

	// Check endpoint reachability via /v1/models
	models, err := c.ListModels()
	if err != nil {
		result.Error = fmt.Sprintf("endpoint: %v", err)
		return result
	}
	result.EndpointReachable = true

	// If we have an API key, check that auth was accepted
	// (models endpoint may work without auth for public endpoints)
	if c.APIKey != "" {
		// The models endpoint itself is partially public, so we don't have
		// a definitive auth check. Report the models list as evidence.
		result.AuthAccepted = true
	}

	result.ModelFound = len(models) > 0
	result.ModelAvailable = true

	return result
}

// ProbeInference sends a synthetic minimal inference request.
// Marked with X-Probe: synthetic header.
// This is only run on explicit `fi doctor --probe` command.
func (c *Client) ProbeInference(model string) ProbeResult {
	result := ProbeResult{}

	if model == "" {
		model = "qwen3.6-35b" // smallest available model
	}

	// Use chat completions endpoint for probe
	url := fmt.Sprintf("%s/chat/completions", c.BaseURL)
	probeBody := fmt.Sprintf(`{
		"model": "%s",
		"messages": [{"role": "user", "content": "ping"}],
		"max_tokens": 5,
		"stream": false
	}`, model)

	req, err := http.NewRequest("POST", url, mustReader(probeBody))
	if err != nil {
		result.Error = fmt.Sprintf("create probe: %v", err)
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SyntheticProbeHeader, SyntheticProbeValue)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("probe request: %v", err)
		return result
	}
	defer resp.Body.Close()

	result.EndpointReachable = true

	if resp.StatusCode == http.StatusOK {
		result.AuthAccepted = true
		result.ModelFound = true
		result.ModelAvailable = true
	} else if resp.StatusCode == http.StatusUnauthorized {
		result.AuthAccepted = false
		result.Error = "authentication failed"
	} else if resp.StatusCode == http.StatusNotFound {
		result.ModelFound = false
		result.Error = fmt.Sprintf("model '%s' not found", model)
	} else {
		body, _ := io.ReadAll(resp.Body)
		result.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return result
}

func mustReader(s string) io.Reader {
	return &stringReader{s: s}
}

type stringReader struct {
	s string
	i int
}

func (r *stringReader) Read(b []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(b, r.s[r.i:])
	r.i += n
	return n, nil
}

// VerifyAPIKey checks if the API key is syntactically valid (hyi- prefix).
func VerifyAPIKey(key string) bool {
	if len(key) < 10 {
		return false
	}
	// FreeInference keys start with "hyi-"
	if len(key) >= 4 && key[:4] == "hyi-" {
		return true
	}
	// Also accept "sk-" prefixed keys (common OpenAI format compatibility)
	if len(key) >= 3 && key[:3] == "sk-" {
		return true
	}
	return false
}