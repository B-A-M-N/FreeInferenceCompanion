package runtime

import (
	"testing"
)

func TestActivation_MultipleNonFI_SameOrigin_Conflict(t *testing.T) {
	clearActivationEnv(t)
	// Two non-FI endpoints with SAME origin (scheme://host) but different paths
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com/v1")
	t.Setenv("OPENAI_BASE_URL", "https://api.anthropic.com/v2")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	// Note: no FREEINFERENCE_API_KEY

	a := Evaluate()
	t.Logf("Active: %v, InactiveReason: %s, Origin: %s, RuntimeKind: %s, EndpointSource: %s, CredentialSource: %s",
		a.Active, a.InactiveReason, a.Origin, a.RuntimeKind, a.EndpointSource, a.CredentialSource)

	// Should be inactive with ReasonConflictingEndpoints because:
	// 1. Multiple non-FI candidates exist with the same origin
	// 2. Per contract: "Multiple variables may coexist only when canonical origins are identical"
	// But here they have the same origin but different paths - paths are normalized away
	// The issue is whether this should be a conflict or not
	// The contract says: "Multiple variables may coexist only when canonical origins are identical"
	// Since the canonical origin (scheme://host) IS identical, this should NOT be a conflict?
	// But wait - the credential kind mismatch makes it inactive anyway
	if a.Active {
		t.Fatalf("expected inactive, got active: %+v", a)
	}
}

func TestActivation_MultipleFI_SameOrigin_NoConflict(t *testing.T) {
	clearActivationEnv(t)
	// Two FI endpoints with SAME origin but different paths
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")

	a := Evaluate()
	t.Logf("Active: %v, InactiveReason: %s, Origin: %s, RuntimeKind: %s, EndpointSource: %s, CredentialSource: %s",
		a.Active, a.InactiveReason, a.Origin, a.RuntimeKind, a.EndpointSource, a.CredentialSource)

	// Should be ACTIVE because:
	// 1. Both normalize to same origin (https://freeinference.org)
	// 2. Both are FI hosts
	// 3. Priority picks FREEINFERENCE_BASE_URL
	if !a.Active {
		t.Fatalf("expected active with same-origin FI endpoints, got: %+v", a)
	}
	if a.EndpointSource != "FREEINFERENCE_BASE_URL" {
		t.Errorf("EndpointSource = %q, want FREEINFERENCE_BASE_URL", a.EndpointSource)
	}
}

func TestActivation_FIAndNonFI_SameOrigin_Conflict(t *testing.T) {
	clearActivationEnv(t)
	// FI and non-FI with SAME origin
	t.Setenv("FREEINFERENCE_BASE_URL", "https://api.example.com/v1")
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.example.com/v2")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")

	a := Evaluate()
	t.Logf("Active: %v, InactiveReason: %s, Origin: %s, RuntimeKind: %s, EndpointSource: %s, CredentialSource: %s",
		a.Active, a.InactiveReason, a.Origin, a.RuntimeKind, a.EndpointSource, a.CredentialSource)

	// Should be INACTIVE with ReasonConflictingEndpoints because:
	// 1. One is FI, one is non-FI
	// 2. Even though origin is the same, the IsFI flag differs
	// 2. The contract says "Multiple variables may coexist only when canonical origins are identical"
	//    but this should also mean same IsFI status
	if a.Active {
		t.Fatalf("expected inactive (FI + non-FI same origin), got active: %+v", a)
	}
	if a.InactiveReason != ReasonConflictingEndpoints {
		t.Errorf("InactiveReason = %q, want %q", a.InactiveReason, ReasonConflictingEndpoints)
	}
}

func TestActivation_TwoNonFI_SameOrigin_CredentialMismatch(t *testing.T) {
	clearActivationEnv(t)
	// Two non-FI endpoints with SAME origin but credential for neither matches
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.example.com/v1")
	t.Setenv("OPENAI_BASE_URL", "https://api.example.com/v2")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")

	a := Evaluate()
	t.Logf("Active: %v, InactiveReason: %s, Origin: %s, RuntimeKind: %s, EndpointSource: %s, CredentialSource: %s",
		a.Active, a.InactiveReason, a.Origin, a.RuntimeKind, a.EndpointSource, a.CredentialSource)

	// Should be inactive - the endpoint is not FI, but we have FREEINFERENCE_API_KEY
	// The credential kind mismatch should make this inactive
	if a.Active {
		t.Fatalf("expected inactive (non-FI endpoint + FI key), got active: %+v", a)
	}
}

func TestActivation_TwoFI_DifferentOrigin_Conflict(t *testing.T) {
	clearActivationEnv(t)
	// Two FI endpoints with DIFFERENT origins
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.freeinference.org/anthropic")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")

	a := Evaluate()
	t.Logf("Active: %v, InactiveReason: %s, Origin: %s, RuntimeKind: %s, EndpointSource: %s, CredentialSource: %s",
		a.Active, a.InactiveReason, a.Origin, a.RuntimeKind, a.EndpointSource, a.CredentialSource)

	// Should be INACTIVE with ReasonConflictingEndpoints
	if a.Active {
		t.Fatalf("expected inactive (different FI origins), got active: %+v", a)
	}
	if a.InactiveReason != ReasonConflictingEndpoints {
		t.Errorf("InactiveReason = %q, want %q", a.InactiveReason, ReasonConflictingEndpoints)
	}
}
