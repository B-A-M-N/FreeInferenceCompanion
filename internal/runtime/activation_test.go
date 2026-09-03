package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// clearActivationEnv removes every activation-relevant variable so each test
// controls the full activation environment.
func clearActivationEnv(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"FI_PROVIDER", "FI_DISABLED", "FI_RUNTIME_INACTIVE", "FI_UNSAFE_FORCE_ACTIVATION",
		"FREEINFERENCE_BASE_URL", "ANTHROPIC_BASE_URL", "OPENAI_BASE_URL",
		"FREEINFERENCE_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"FI_ALLOW_CUSTOM_API_ENDPOINT", "FI_ALLOW_INSECURE_LOCALHOST",
		ProxyUpstreamEnv,
		"CODEX_HOME", "CODEX_PROFILE",
	} {
		t.Setenv(env, "")
	}
}

func writeCodexConfig(t *testing.T, contents string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestActivationForClient_ClaudeRequiresClaudeRouteAndCredential(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("FREEINFERENCE_API_KEY", "generic-key-is-not-claude-evidence")
	if a := EvaluateForClient(ClientClaudeCode); a.Active {
		t.Fatalf("generic FI key must not activate Claude: %+v", a)
	}

	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anthropic-runtime-key")
	a := EvaluateForClient(ClientClaudeCode)
	if !a.Active || a.CredentialSource != CredAnthropicAuthToken || a.Client != ClientClaudeCode {
		t.Fatalf("Claude route + Claude credential should activate: %+v", a)
	}
}

func TestActivationForClient_ClaudeRejectsOffHostAndConflictingRoutes(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anthropic-runtime-key")
	if a := EvaluateForClient(ClientClaudeCode); a.Active || a.InactiveReason != ReasonEndpointNotApproved {
		t.Fatalf("off-host Claude route must remain inactive: %+v", a)
	}

	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	if a := EvaluateForClient(ClientClaudeCode); !a.Active {
		t.Fatalf("unrelated runtime routes must not disable Claude: %+v", a)
	}

	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	if a := EvaluateForClient(ClientClaudeCode); !a.Active {
		t.Fatalf("unrelated OpenAI route must not disable Claude: %+v", a)
	}
}

func TestActivationForClient_CodexUsesSelectedProviderAndEnvKey(t *testing.T) {
	clearActivationEnv(t)
	writeCodexConfig(t, `model_provider = "freeinference"

[model_providers.freeinference]
base_url = "https://freeinference.org/v1"
env_key = "CODEX_FI_KEY"
`)
	t.Setenv("FREEINFERENCE_API_KEY", "coincidental-key")
	t.Setenv("CODEX_FI_KEY", "selected-provider-key")
	a := EvaluateForClient(ClientCodex)
	if !a.Active || a.Evidence.ProviderID != "freeinference" || a.CredentialSource != CredentialSource("CODEX_FI_KEY") {
		t.Fatalf("selected Codex provider should activate: %+v", a)
	}

	t.Setenv("CODEX_FI_KEY", "")
	a = EvaluateForClient(ClientCodex)
	if a.Active || a.InactiveReason != ReasonEndpointOnly {
		t.Fatalf("missing selected provider key must remain inactive: %+v", a)
	}
}

func TestActivationForClient_CodexRejectsUnapprovedProvider(t *testing.T) {
	clearActivationEnv(t)
	writeCodexConfig(t, `model_provider = "openai"

[model_providers.openai]
base_url = "https://api.openai.com/v1"
env_key = "OPENAI_API_KEY"
`)
	t.Setenv("OPENAI_API_KEY", "openai-key")
	a := EvaluateForClient(ClientCodex)
	if a.Active || a.InactiveReason != ReasonEndpointNotApproved {
		t.Fatalf("off-host Codex provider must remain inactive: %+v", a)
	}
}

func TestResolveCodexProviderConfigurationHonorsSelectedProfile(t *testing.T) {
	clearActivationEnv(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`model_provider = "openai"

[model_providers.openai]
base_url = "https://api.openai.com/v1"
env_key = "OPENAI_API_KEY"

[model_providers.freeinference]
base_url = "https://freeinference.org/v1"
env_key = "CODEX_FI_KEY"
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "fi.config.toml"), []byte(`model_provider = "freeinference"
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_PROFILE", "fi")
	t.Setenv("FREEINFERENCE_API_KEY", "coincidental-key")
	t.Setenv("CODEX_FI_KEY", "selected-provider-key")

	evidence, err := ResolveCodexProviderConfiguration()
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if evidence.ProviderID != "freeinference" || evidence.ProviderEnvKey != "CODEX_FI_KEY" || !evidence.ProviderSelectionVerified {
		t.Fatalf("profile selection evidence = %+v", evidence)
	}
	if got := evidence.CredentialValue; got != "selected-provider-key" {
		t.Fatalf("credential = %q, want selected provider credential", got)
	}
	if config, err := CodexProviderConfiguration(); err != nil || !config.FreeInferenceConfigured || config.CredentialSource != "CODEX_FI_KEY" {
		t.Fatalf("safe provider summary = %+v, err=%v", config, err)
	}
}

func TestResolveCodexProviderRejectsNonCredentialEnvironmentName(t *testing.T) {
	clearActivationEnv(t)
	writeCodexConfig(t, `model_provider = "freeinference"

[model_providers.freeinference]
base_url = "https://freeinference.org/v1"
env_key = "HOME"
`)
	if _, err := ResolveCodexProviderConfiguration(); err == nil {
		t.Fatal("provider env_key HOME must not be accepted as a credential reference")
	}
}

func TestResolveCodexProviderReadDirFailureIsUnknown(t *testing.T) {
	clearActivationEnv(t)
	writeCodexConfig(t, `model_provider = "freeinference"

[model_providers.freeinference]
base_url = "https://freeinference.org/v1"
env_key = "CODEX_FI_KEY"
`)
	t.Setenv("CODEX_FI_KEY", "selected-provider-key")
	evidence, err := resolveCodexProviderConfigurationWith("", func(string) ([]os.DirEntry, error) {
		return nil, errors.New("permission denied")
	})
	if err == nil || evidence.ProviderSelectionVerified {
		t.Fatalf("profile directory failure must remain unverified: evidence=%+v err=%v", evidence, err)
	}
}

func TestActivation_PersistentDisablePreventsActivation(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")

	if err := DisablePersistently(); err != nil {
		t.Fatal(err)
	}
	if disabled, err := PersistentDisableState(); err != nil || !disabled {
		t.Fatalf("persistent disable = %t, err = %v", disabled, err)
	}
	a := Evaluate()
	if a.Active || !a.Disabled || !a.DisabledByMarker || a.DisabledByEnv {
		t.Fatalf("persistent marker must short-circuit activation: %+v", a)
	}

	dir, err := CompanionConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("config dir permissions = %v, err = %v; want 0700", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(dir + "/.companion-disabled"); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("marker permissions = %v, err = %v; want 0600", info.Mode().Perm(), err)
	}

	if err := EnablePersistently(); err != nil {
		t.Fatal(err)
	}
	if a := Evaluate(); !a.Active {
		t.Fatalf("activation should resume after enable: %+v", a)
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

func TestActivation_DocumentedClaudeCodeCredential_Active(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "free-inference-test")
	a := Evaluate()
	if !a.Active {
		t.Fatalf("expected documented Claude Code setup to activate, got %+v", a)
	}
	if a.CredentialSource != CredAnthropicAuthToken {
		t.Errorf("CredentialSource = %q, want %q", a.CredentialSource, CredAnthropicAuthToken)
	}
	if got := a.ManagementBaseURL(); got != "https://freeinference.org/v1" {
		t.Errorf("ManagementBaseURL = %q, want https://freeinference.org/v1", got)
	}
}

func TestActivation_ClaudeLocalProxyRequiresExplicitApprovedUpstream(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:8765")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "free-inference-test")
	if a := EvaluateForClient(ClientClaudeCode); a.Active {
		t.Fatalf("loopback Claude route without upstream attestation must stay inactive: %+v", a)
	}

	t.Setenv(ProxyUpstreamEnv, "https://api.example.com/anthropic")
	if a := EvaluateForClient(ClientClaudeCode); a.Active {
		t.Fatalf("unapproved proxy upstream must stay inactive: %+v", a)
	}

	t.Setenv(ProxyUpstreamEnv, "https://freeinference.org/anthropic")
	a := EvaluateForClient(ClientClaudeCode)
	if !a.Active || !a.ProxyActive {
		t.Fatalf("approved proxy route should activate: %+v", a)
	}
	if a.Origin != "https://freeinference.org" || a.ProxyUpstreamURL != "https://freeinference.org/anthropic" {
		t.Fatalf("proxy identity = %+v", a)
	}
	if got := a.ManagementBaseURL(); got != "https://freeinference.org/v1" {
		t.Fatalf("proxy management URL = %q", got)
	}
}

func TestActivation_ClaudeProxyAttestationDoesNotActivateDirectOrUnrelatedRoutes(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv(ProxyUpstreamEnv, "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "runtime-key")

	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	if a := EvaluateForClient(ClientClaudeCode); a.Active {
		t.Fatalf("proxy attestation must not activate ordinary Claude: %+v", a)
	}

	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:8765")
	t.Setenv(ProxyUpstreamEnv, "https://freeinference.org/v1")
	if a := EvaluateForClient(ClientClaudeCode); a.Active {
		t.Fatalf("non-Anthropic proxy route must stay inactive: %+v", a)
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
		{"sub.api.freeinference.org", false},
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
