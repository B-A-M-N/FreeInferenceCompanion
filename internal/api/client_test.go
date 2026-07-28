package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpointJoinPreservesBasePath(t *testing.T) {
	c := NewClient("https://freeinference.org/v1", "", time.Second)
	got, err := c.endpoint("/models")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://freeinference.org/v1/models" {
		t.Errorf("endpoint = %s", got)
	}
}

func TestListModelsParsesCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("request path = %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		json.NewEncoder(w).Encode(ModelsResponse{
			Object: "list",
			Data:   []Model{{ID: "glm-5.1", ContextLength: 200000}},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL+"/v1", "", 5*time.Second)
	models, err := c.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "glm-5.1" {
		t.Errorf("models = %+v", models)
	}
}

func TestListModelsErrorIsSanitizedAndBounded(t *testing.T) {
	huge := strings.Repeat("x", 300000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(huge))
	}))
	defer server.Close()

	c := NewClient(server.URL, "", 5*time.Second)
	_, err := c.ListModels()
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if he.StatusCode != 500 {
		t.Errorf("status = %d", he.StatusCode)
	}
	if len(he.Message) > 250 {
		t.Errorf("error message not bounded: %d chars", len(he.Message))
	}
}

func TestRetryAfterParsed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit","message":"slow","code":429}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "", 5*time.Second)
	_, err := c.ListModels()
	he, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if he.RetryAfter != 45*time.Second {
		t.Errorf("retry after = %v", he.RetryAfter)
	}
}

func TestProbeDoesNotClaimAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ModelsResponse{Data: []Model{{ID: "m1"}}})
	}))
	defer server.Close()

	c := NewClient(server.URL, "hyi-test-key-12345", 5*time.Second)
	res := c.Probe()
	if res.Endpoint.State != CheckPass {
		t.Errorf("endpoint = %+v", res.Endpoint)
	}
	if res.Authentication.State != CheckUnknown {
		t.Errorf("auth must be unknown without a real authenticated op: %+v", res.Authentication)
	}
	if res.ModelAccess.State != CheckUnknown {
		t.Errorf("model access must be unknown from catalog alone: %+v", res.ModelAccess)
	}
}

func TestProbeUnreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "", 2*time.Second)
	res := c.Probe()
	if res.Endpoint.State != CheckFail {
		t.Errorf("endpoint = %+v", res.Endpoint)
	}
}

func TestProbeInferenceSendsStructuredJSON(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		if r.Header.Get(SyntheticProbeHeader) != SyntheticProbeValue {
			t.Error("missing synthetic probe header")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "pong"}}},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "hyi-test-key-12345", 5*time.Second)
	res := c.ProbeInference("glm-5.1")
	if res.ModelAccess.State != CheckPass {
		t.Errorf("probe = %+v", res.ModelAccess)
	}
	if res.Authentication.State != CheckPass {
		t.Errorf("auth = %+v", res.Authentication)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("probe body is not structured JSON: %v", err)
	}
	if parsed["model"] != "glm-5.1" {
		t.Errorf("model = %v", parsed["model"])
	}
	if _, ok := parsed["messages"].([]any); !ok {
		t.Error("messages missing")
	}
}

func TestProbeInferenceUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"type":"auth","message":"bad key","code":401}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "bad-key", 5*time.Second)
	res := c.ProbeInference("glm-5.1")
	if res.Authentication.State != CheckFail {
		t.Errorf("auth = %+v", res.Authentication)
	}
}

func TestProbeInferenceRequiresModel(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "", time.Second)
	res := c.ProbeInference("")
	if res.Endpoint.State != CheckFail {
		t.Errorf("empty model must fail fast: %+v", res.Endpoint)
	}
}

// TestGetHealthNeverSendsAPIKey is the regression test for P0-2: regardless of
// how the client is configured, the health request must NOT carry an
// Authorization header. The API key is scoped to the FreeInference inference
// endpoint; a separately configured FI_HEALTH_URL must never receive it.
func TestGetHealthNeverSendsAPIKey(t *testing.T) {
	var seenAuth string
	var seenHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenHost = r.Host
		json.NewEncoder(w).Encode(HealthResponse{Status: "healthy", Healthy: 1, Total: 1})
	}))
	defer server.Close()

	// httptest.Server.URL is http://127.0.0.1:port — not HTTPS. Use the host
	// portion directly so we can build an https URL against the same listener
	// by replacing the scheme in the request only via TLS-free normalization.
	// NormalizeHealthURL requires HTTPS, so we craft an https URL whose Host
	// matches a server we DO control via Host header rewriting. Simpler: drive
	// the request through GetHealthFromTrusted with a synthetic server and a
	// custom allowlist entry, swapping the dialer. Instead, the cleanest path
	// is to assert behavior at the URL-validation layer (TestNormalizeHealthURL)
	// AND assert via a testClient that the request really left without auth.
	_ = seenAuth
	_ = seenHost
	_ = server

	// Direct assertion: a client with a configured API key still produces a
	// health request with no Authorization header. We reuse the public
	// NormalizeHealthURL to build a valid https request, then swap the
	// transport to point at our test server.
	c := NewClient("https://freeinference.org/v1", "hyi-test-key-12345", 5*time.Second)

	// Capture whether the request would have carried auth by intercepting via
	// a server bound to the loopback that mimics freeinference.org's health
	// endpoint. The trick: we dial 127.0.0.1 but set the URL host to
	// 127.0.0.1 (localhost is the test "trusted" host via the allowlist).
	authSeen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen <- r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(HealthResponse{Status: "healthy", Healthy: 1, Total: 1})
	}))
	defer srv.Close()

	// Swap the client transport to use our test server regardless of scheme.
	// We bypass TLS by pointing GetHealth at the http URL directly; to do that
	// we have to relax the https requirement — but the contract is "the API
	// key must not be sent". The simplest faithful test: use NormalizeHealthURL
	// to confirm https URLs validate, then assert on the constructed request
	// that Authorization is unset. Do that via a request-observing transport.
	c.HTTPClient = &http.Client{Transport: roundTripRecorder(func(req *http.Request) (*http.Response, error) {
		authSeen <- req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"status":"healthy","healthy":1,"total":1}`)),
			Header:     make(http.Header),
		}, nil
	})}

	if _, err := c.GetHealth("https://freeinference.org/health"); err != nil {
		t.Fatalf("GetHealth returned error: %v", err)
	}
	got := <-authSeen
	if got != "" {
		t.Fatalf("GetHealth sent Authorization header %q; API key must never be on health requests", got)
	}
}

type roundTripRecorder func(*http.Request) (*http.Response, error)

func (f roundTripRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestNormalizeHealthURL covers the validation gates that keep the API key
// (and userinfo / query-string secrets) off health requests and out of
// persisted state.
func TestNormalizeHealthURL(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantErr     bool
		wantOrigin  string
		wantRequest string
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "relative", input: "/health", wantErr: true},
		{name: "plain http rejected", input: "http://freeinference.org/health", wantErr: true},
		{name: "attacker https", input: "https://evil.example.com/health", wantErr: false, wantOrigin: "https://evil.example.com", wantRequest: "https://evil.example.com/health"},
		{name: "userinfo rejected", input: "https://token@freeinference.org/health", wantErr: true},
		{name: "fragment rejected", input: "https://freeinference.org/health#frag", wantErr: true},
		{name: "query preserved on request, not origin", input: "https://status.freeinference.org/health?token=secret", wantErr: false, wantOrigin: "https://status.freeinference.org", wantRequest: "https://status.freeinference.org/health?token=secret"},
		{name: "valid https", input: "https://freeinference.org/health", wantErr: false, wantOrigin: "https://freeinference.org", wantRequest: "https://freeinference.org/health"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeHealthURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got.Origin != tc.wantOrigin {
				t.Errorf("origin = %q, want %q", got.Origin, tc.wantOrigin)
			}
			if got.RequestURL != tc.wantRequest {
				t.Errorf("request URL = %q, want %q", got.RequestURL, tc.wantRequest)
			}
		})
	}
}

// TestIsAllowedHealthOrigin verifies the allowlist matching used to gate
// FI_HEALTH_URL before any network call.
func TestIsAllowedHealthOrigin(t *testing.T) {
	allow := []string{"freeinference.org", "status.freeinference.org"}
	cases := []struct {
		host string
		want bool
	}{
		{"freeinference.org", true},
		{"status.freeinference.org", true},
		{"FreeInference.org", true}, // case-insensitive
		// Subdomain lookalikes must be rejected.
		{"evilfreeinference.org", false},
		{"freeinference.org.evil.com", false},
		// Strangers rejected.
		{"evil.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsAllowedHealthOrigin(tc.host, allow); got != tc.want {
			t.Errorf("IsAllowedHealthOrigin(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// TestGetHealthFromTrustedRejectsUnknownHost ensures that FI_HEALTH_URL values
// pointing at a non-FreeInference host are refused before any network call.
// This is the primary exfiltration defense.
func TestGetHealthFromTrustedRejectsUnknownHost(t *testing.T) {
	c := NewClient("https://freeinference.org/v1", "hyi-test-key-12345", 5*time.Second)
	called := false
	c.HTTPClient = &http.Client{Transport: roundTripRecorder(func(req *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})}

	_, err := c.GetHealthFromTrusted("https://evil.example.com/health", HealthURLOrigins)
	if err == nil {
		t.Fatal("expected rejection of unknown host")
	}
	if called {
		t.Fatal("transport was invoked for a non-allowlisted host; allowlist gate did not fire")
	}
}

// TestStructuredErrorRedacted ensures an upstream error message echoed in
// structured JSON is scrubbed before it can reach reports or status output.
// Regression for the P0-6 partial: previously only the raw-body fallback ran
// through secure.Redact.
func TestStructuredErrorRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"type":"auth","message":"Bearer hyi-test-key-12345 rejected","code":401}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "hyi-test-key-12345", 5*time.Second)
	_, err := c.ListModels()
	he, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if strings.Contains(he.Message, "hyi-test-key-12345") {
		t.Fatalf("structured error message leaked a token-shaped value: %q", he.Message)
	}
}
