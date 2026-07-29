package adapters

import (
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

// clearProviderEnv removes every provider-relevant variable so each test
// controls the full detection environment.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"FI_PROVIDER", "FI_DISABLED", "FI_UNSAFE_FORCE_ACTIVATION",
		"FREEINFERENCE_BASE_URL", "ANTHROPIC_BASE_URL", "OPENAI_BASE_URL",
		"FREEINFERENCE_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"FI_ALLOW_CUSTOM_API_ENDPOINT", "FI_ALLOW_INSECURE_LOCALHOST",
	} {
		t.Setenv(env, "")
	}
	runtime.ResetSaltCache()
}

// P0-1 regression: the legacy permissive rules (endpoint-only, key-only,
// FI_PROVIDER-only) MUST NOT confirm activation. Only endpoint+key on an
// approved FreeInference host confirms.
func TestDetectProvider_NothingConfigured_Inactive(t *testing.T) {
	clearProviderEnv(t)
	d := DetectProvider()
	if d.Confirmed || d.Name != "unknown" {
		t.Errorf("expected unresolved unknown, got %+v", d)
	}
}

func TestDetectProvider_FIProviderOnly_Inactive(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FI_PROVIDER", "freeinference")
	d := DetectProvider()
	if d.Confirmed {
		t.Errorf("FI_PROVIDER alone must not confirm: %+v", d)
	}
}

func TestDetectProvider_EndpointOnly_Inactive(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	d := DetectProvider()
	if d.Confirmed {
		t.Errorf("endpoint alone must not confirm: %+v", d)
	}
}

func TestDetectProvider_KeyOnly_Inactive(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	d := DetectProvider()
	if d.Confirmed {
		t.Errorf("key alone must not confirm: %+v", d)
	}
}

func TestDetectProvider_FreeInferenceEndpointAndKey_Active(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	d := DetectProvider()
	if !d.Confirmed || d.Name != "freeinference" {
		t.Errorf("endpoint+key on FI host must confirm: %+v", d)
	}
	if d.BaseURL != "https://freeinference.org" {
		t.Errorf("BaseURL = %q, want sanitized origin", d.BaseURL)
	}
}

func TestDetectProvider_AnthropicCompatibleEndpointAndKey_Active(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	d := DetectProvider()
	if !d.Confirmed || d.Source != "ANTHROPIC_BASE_URL" {
		t.Errorf("anthropic-compat FI host+key must confirm: %+v", d)
	}
}

func TestDetectProvider_OpenAICompatibleEndpointAndKey_Active(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://api.freeinference.org/v1")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	d := DetectProvider()
	if !d.Confirmed || d.Source != "OPENAI_BASE_URL" {
		t.Errorf("openai-compat FI host+key must confirm: %+v", d)
	}
}

func TestDetectProvider_NonFreeInferenceSession_Inactive(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	d := DetectProvider()
	if d.Confirmed || d.Name != "unknown" {
		t.Errorf("non-FI session must stay unknown: %+v", d)
	}
}

func TestDetectProvider_APIKeyWithConflictingProvider_Inactive(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	d := DetectProvider()
	if d.Confirmed {
		t.Errorf("conflicting provider config must not confirm: %+v", d)
	}
}

func TestIsFreeInferenceURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://freeinference.org/v1", true},
		{"https://api.freeinference.org/v1", true},
		{"https://freeinference.org.evil.com/v1", false},
		{"https://api.anthropic.com", false},
		{"freeinference.org/v1", false}, // no scheme → no hostname
		{"", false},
		{"https://", false},
	}
	for _, c := range cases {
		if got := IsFreeInferenceURL(c.url); got != c.want {
			t.Errorf("IsFreeInferenceURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// TestIsFreeInferenceURL_RejectsCredentialBearingURLs verifies that URLs with
// userinfo, query strings, or fragments are never treated as FreeInference —
// even when the hostname is correct.
func TestIsFreeInferenceURL_RejectsCredentialBearingURLs(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"userinfo", "https://token123@freeinference.org/v1"},
		{"query secret", "https://freeinference.org/v1?api_key=querysecret"},
		{"fragment", "https://freeinference.org/v1#section"},
		{"userinfo+query", "https://user:pass@freeinference.org/v1?token=x"},
		{"lookalike host", "https://evilfreeinference.org/v1"},
		{"http remote", "http://freeinference.org/v1"},
	}
	for _, c := range cases {
		if got := IsFreeInferenceURL(c.url); got {
			t.Errorf("IsFreeInferenceURL(%q) = true, want false (%s)", c.url, c.name)
		}
	}
}

// TestDetectProvider_DoesNotPersistRawURL verifies that provider detection
// persists only the sanitized origin (scheme://host), never the raw environment
// value that may carry userinfo or query secrets.
func TestDetectProvider_DoesNotPersistRawURL(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://token123@freeinference.org/v1?api_key=querysecret")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	d := DetectProvider()
	if d.BaseURL != "" {
		t.Errorf("persisted base_url = %q, want empty (credential-bearing URL must not be persisted)", d.BaseURL)
	}
	if d.Confirmed {
		t.Errorf("credential-bearing URL must not be confirmed: %+v", d)
	}
}

// TestDetectProvider_PersistsSanitizedOrigin verifies that a valid FreeInference
// URL persists only the sanitized origin, not the full path.
func TestDetectProvider_PersistsSanitizedOrigin(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1/some/path")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	d := DetectProvider()
	if !d.Confirmed || d.Source != "FREEINFERENCE_BASE_URL" {
		t.Errorf("unexpected detection: %+v", d)
	}
	if d.BaseURL != "https://freeinference.org" {
		t.Errorf("persisted base_url = %q, want sanitized origin %q", d.BaseURL, "https://freeinference.org")
	}
}

// TestDetectProviderForModel_VendorPrefixModel_StaysActive covers the
// regression: a FreeInference deployment may legitimately serve a model with
// a deepseek-/llama-/mistral- prefix. The previous blacklist mis-classified
// these as non-FreeInference and stripped confirmation. The model ID is now
// recorded but never affects activation.
func TestDetectProviderForModel_VendorPrefixModel_StaysActive(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	for _, model := range []string{"deepseek-r1", "llama-3.1-70b", "mistral-large"} {
		d := DetectProviderForModel(model)
		if !d.Confirmed {
			t.Errorf("model %q on FI host must stay confirmed: %+v", model, d)
		}
	}
}
