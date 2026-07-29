package runtime

import (
	"testing"
)

// clearActivationEnv removes every activation-relevant variable so each test
// controls the full activation environment.
func clearActivationEnv(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"FI_PROVIDER", "FI_DISABLED", "FI_UNSAFE_FORCE_ACTIVATION",
		"FREEINFERENCE_BASE_URL", "ANTHROPIC_BASE_URL", "OPENAI_BASE_URL",
		"FREEINFERENCE_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"FI_ALLOW_CUSTOM_API_ENDPOINT", "FI_ALLOW_INSECURE_LOCALHOST",
	} {
		t.Setenv(env, "")
	}
}

func TestActivation_NoEndpointNoKey_Inactive(t *testing.T) {
	clearActivationEnv(t)
	a := Evaluate()
	if a.Active {
		t.Fatalf("expected inactive, got %+v", a)
	}
	if a.InactiveReason != ReasonEndpointAndKeyRequired {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonEndpointAndKeyRequired)
	}
}

func TestActivation_EndpointOnly_Inactive(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	a := Evaluate()
	if a.Active {
		t.Fatalf("endpoint alone must not activate: %+v", a)
	}
	if a.InactiveReason != ReasonEndpointOnly {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonEndpointOnly)
	}
}

func TestActivation_KeyOnly_Inactive(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a := Evaluate()
	if a.Active {
		t.Fatalf("key alone must not activate: %+v", a)
	}
	if a.InactiveReason != ReasonKeyOnly {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonKeyOnly)
	}
}

func TestActivation_ProviderFlagOnly_Inactive(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FI_PROVIDER", "freeinference")
	a := Evaluate()
	if a.Active {
		t.Fatalf("FI_PROVIDER alone must not activate: %+v", a)
	}
	if a.InactiveReason != ReasonEndpointAndKeyRequired {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonEndpointAndKeyRequired)
	}
}

func TestActivation_FreeInferenceEndpointAndKey_Active(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a := Evaluate()
	if !a.Active {
		t.Fatalf("expected active, got %+v", a)
	}
	if a.Origin != "https://freeinference.org" {
		t.Errorf("origin = %q, want https://freeinference.org", a.Origin)
	}
	if a.EndpointSource != "FREEINFERENCE_BASE_URL" {
		t.Errorf("endpoint source = %q", a.EndpointSource)
	}
	if a.CredentialSource != CredFreeInferenceAPIKey {
		t.Errorf("credential source = %q", a.CredentialSource)
	}
}

func TestActivation_NonFreeInferenceEndpointAndKey_Inactive(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a := Evaluate()
	if a.Active {
		t.Fatalf("non-FI host must not activate (would leak credential): %+v", a)
	}
	if a.InactiveReason != ReasonConflictingEndpoints {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonConflictingEndpoints)
	}
}

func TestActivation_MalformedEndpointAndKey_Inactive(t *testing.T) {
	clearActivationEnv(t)
	// userinfo → NormalizeEndpoint rejects → endpoint invalid.
	t.Setenv("FREEINFERENCE_BASE_URL", "https://token@freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a := Evaluate()
	if a.Active {
		t.Fatalf("malformed endpoint must not activate: %+v", a)
	}
	if a.InactiveReason != ReasonEndpointInvalid {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonEndpointInvalid)
	}
}

func TestActivation_CredentialBearingURL_Inactive(t *testing.T) {
	clearActivationEnv(t)
	// query secret → NormalizeEndpoint rejects → endpoint invalid.
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1?api_key=x")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a := Evaluate()
	if a.Active {
		t.Fatalf("credential-bearing URL must not activate: %+v", a)
	}
	if a.InactiveReason != ReasonEndpointInvalid {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonEndpointInvalid)
	}
}

func TestActivation_ConflictingFIAndNonFI_Inactive(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a := Evaluate()
	if a.Active {
		t.Fatalf("conflicting endpoints must not activate: %+v", a)
	}
	if a.InactiveReason != ReasonConflictingEndpoints {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonConflictingEndpoints)
	}
}

func TestActivation_DisabledFlag_Inactive(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	t.Setenv("FI_DISABLED", "1")
	a := Evaluate()
	if a.Active {
		t.Fatalf("FI_DISABLED=1 must not activate")
	}
	if !a.Disabled {
		t.Errorf("Disabled flag not set")
	}
	if a.InactiveReason != ReasonDisabled {
		t.Errorf("reason = %q, want %q", a.InactiveReason, ReasonDisabled)
	}
}

func TestActivation_RemovedAfterActive_ImmediatelyInactive(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	if a := Evaluate(); !a.Active {
		t.Fatalf("expected active on first eval: %+v", a)
	}
	// Simulate removal: clear env and re-evaluate.
	t.Setenv("FREEINFERENCE_BASE_URL", "")
	t.Setenv("FREEINFERENCE_API_KEY", "")
	a := Evaluate()
	if a.Active {
		t.Fatalf("after endpoint+key removed, must be inactive: %+v", a)
	}
}

func TestActivation_HostedModelWithVendorPrefix_Active(t *testing.T) {
	// A FreeInference deployment may serve models beginning with deepseek-,
	// llama-, mistral-, etc. Model identity never overrules a validated
	// endpoint+key pair.
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a := EvaluateWithModel("deepseek-r1")
	if !a.Active {
		t.Fatalf("FreeInference-hosted vendor-prefix model must stay active: %+v", a)
	}
	if a.ModelID != "deepseek-r1" {
		t.Errorf("ModelID = %q", a.ModelID)
	}
}

func TestActivation_AnthropicRuntimeMatchesCredential_Active(t *testing.T) {
	// ANTHROPIC_BASE_URL on a FreeInference Anthropic-compat endpoint with
	// ANTHROPIC_API_KEY is a valid activation (credential kind matches runtime).
	clearActivationEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	a := Evaluate()
	if !a.Active {
		t.Fatalf("expected active for anthropic-compat FI host: %+v", a)
	}
	if a.RuntimeKind != RuntimeAnthropic {
		t.Errorf("RuntimeKind = %q, want %q", a.RuntimeKind, RuntimeAnthropic)
	}
}

func TestActivation_OpenAIRuntimeMatchesCredential_Active(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://api.freeinference.org/v1")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	a := Evaluate()
	if !a.Active {
		t.Fatalf("expected active for openai-compat FI host: %+v", a)
	}
	if a.RuntimeKind != RuntimeOpenAI {
		t.Errorf("RuntimeKind = %q, want %q", a.RuntimeKind, RuntimeOpenAI)
	}
}

func TestActivation_CredentialKindMismatch_Inactive(t *testing.T) {
	// ANTHROPIC_API_KEY with OPENAI_BASE_URL — credential kind does not match
	// runtime. Without a FREEINFERENCE_API_KEY, this is inactive.
	clearActivationEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://api.freeinference.org/v1")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	a := Evaluate()
	if a.Active {
		t.Fatalf("credential kind mismatch must not activate: %+v", a)
	}
}

func TestActivation_UnsafeForce_ActiveWithFlag(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FI_UNSAFE_FORCE_ACTIVATION", "1")
	a := Evaluate()
	if !a.Active {
		t.Fatalf("FI_UNSAFE_FORCE_ACTIVATION=1 must activate")
	}
	if !a.UnsafeForced {
		t.Errorf("UnsafeForced flag not set")
	}
}

func TestIsFreeInferenceHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"freeinference.org", true},
		{"api.freeinference.org", true},
		{"sub.api.freeinference.org", true},
		{"FREEINFERENCE.ORG", true}, // case-insensitive
		{"evilfreeinference.org", false},
		{"freeinference.org.evil.com", false},
		{"api.anthropic.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsFreeInferenceHost(c.host); got != c.want {
			t.Errorf("IsFreeInferenceHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestActivation_IdentityStableAndDistinct(t *testing.T) {
	ResetSaltCache()
	clearActivationEnv(t)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	a1 := Evaluate()
	id1, err := a1.Identity(DefaultSaltLoader())
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	// Same env → same identity (idempotent).
	id2, err := a1.Identity(DefaultSaltLoader())
	if err != nil {
		t.Fatalf("Identity second call: %v", err)
	}
	if id1 != id2 {
		t.Errorf("identity not stable across calls")
	}
	// Different key on same endpoint → different fingerprint.
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-other-key-99999")
	a3 := Evaluate()
	id3, err := a3.Identity(DefaultSaltLoader())
	if err != nil {
		t.Fatalf("Identity after key change: %v", err)
	}
	if id1.EndpointOrigin != id3.EndpointOrigin {
		t.Errorf("endpoint origin changed unexpectedly")
	}
	if id1.CredentialFP == id3.CredentialFP {
		t.Errorf("credential fingerprint did not change with key — namespacing broken")
	}
}
