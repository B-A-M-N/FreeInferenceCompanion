package adapters

import (
	"testing"
)

// clearProviderEnv removes every provider-relevant variable so each test
// controls the full detection environment.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"FI_PROVIDER", "FREEINFERENCE_BASE_URL", "ANTHROPIC_BASE_URL",
		"OPENAI_BASE_URL", "FREEINFERENCE_API_KEY",
	} {
		t.Setenv(env, "")
	}
}

func TestDetectProvider_ExplicitOverride(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FI_PROVIDER", "freeinference")

	d := DetectProvider()
	if !d.Confirmed || d.Name != "freeinference" || d.Source != "FI_PROVIDER" {
		t.Errorf("unexpected detection: %+v", d)
	}
}

func TestDetectProvider_FreeInferenceBaseURL(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")

	d := DetectProvider()
	if !d.Confirmed || d.Source != "FREEINFERENCE_BASE_URL" {
		t.Errorf("unexpected detection: %+v", d)
	}
	if d.BaseURL != "https://freeinference.org/v1" {
		t.Errorf("expected base URL recorded, got %q", d.BaseURL)
	}
}

func TestDetectProvider_AnthropicCompatibleEndpoint(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")

	d := DetectProvider()
	if !d.Confirmed || d.Source != "ANTHROPIC_BASE_URL" {
		t.Errorf("unexpected detection: %+v", d)
	}
}

func TestDetectProvider_OpenAICompatibleEndpoint(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://api.freeinference.org/v1")

	d := DetectProvider()
	if !d.Confirmed || d.Source != "OPENAI_BASE_URL" {
		t.Errorf("unexpected detection: %+v", d)
	}
}

func TestDetectProvider_APIKeyAlone(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")

	d := DetectProvider()
	if !d.Confirmed || d.Source != "FREEINFERENCE_API_KEY" {
		t.Errorf("unexpected detection: %+v", d)
	}
}

func TestDetectProvider_APIKeyWithConflictingProvider(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")

	d := DetectProvider()
	if d.Confirmed {
		t.Errorf("conflicting provider config must not confirm FreeInference: %+v", d)
	}
}

func TestDetectProvider_NonFreeInferenceSession(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")

	d := DetectProvider()
	if d.Confirmed || d.Name != "unknown" {
		t.Errorf("non-FreeInference session must stay unknown: %+v", d)
	}
}

func TestDetectProvider_NothingConfigured(t *testing.T) {
	clearProviderEnv(t)

	d := DetectProvider()
	if d.Confirmed || d.Name != "unknown" || d.Source != "unresolved" {
		t.Errorf("expected unresolved unknown, got %+v", d)
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
