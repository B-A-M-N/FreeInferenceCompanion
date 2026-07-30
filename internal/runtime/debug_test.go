package runtime

import (
	"os"
	"testing"
)

func TestDebugEvaluate(t *testing.T) {
	os.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	os.Setenv("FREEINFERENCE_API_KEY", "test-key")
	os.Setenv("FI_DISABLED", "0")
	os.Setenv("FI_NO_BACKGROUND", "1")

	a := Evaluate()
	t.Logf("Active: %v", a.Active)
	t.Logf("Disabled: %v", a.Disabled)
	t.Logf("UnsafeForced: %v", a.UnsafeForced)
	t.Logf("EndpointPresent: %v", a.EndpointPresent)
	t.Logf("EndpointValid: %v", a.EndpointValid)
	t.Logf("KeyPresent: %v", a.KeyPresent)
	t.Logf("EndpointSource: %s", a.EndpointSource)
	t.Logf("CredentialSource: %s", a.CredentialSource)
	t.Logf("Origin: %s", a.Origin)
	t.Logf("RuntimeKind: %s", a.RuntimeKind)
	t.Logf("InactiveReason: %s", a.InactiveReason)
	t.Logf("ModelID: %s", a.ModelID)
}

func TestDebugEvaluate2(t *testing.T) {
	// Clean env first
	os.Unsetenv("ANTHROPIC_BASE_URL")
	os.Unsetenv("OPENAI_BASE_URL")

	os.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	os.Setenv("FREEINFERENCE_API_KEY", "test-key")
	os.Setenv("FI_DISABLED", "0")
	os.Setenv("FI_NO_BACKGROUND", "1")

	a := Evaluate()
	t.Logf("Active: %v", a.Active)
	t.Logf("EndpointPresent: %v", a.EndpointPresent)
	t.Logf("EndpointValid: %v", a.EndpointValid)
	t.Logf("KeyPresent: %v", a.KeyPresent)
	t.Logf("EndpointSource: %s", a.EndpointSource)
	t.Logf("CredentialSource: %s", a.CredentialSource)
	t.Logf("Origin: %s", a.Origin)
	t.Logf("RuntimeKind: %s", a.RuntimeKind)
	t.Logf("InactiveReason: %s", a.InactiveReason)
}
