package tracing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateTraceIDIsOpaqueUniqueAndBounded(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 256; i++ {
		id, err := GenerateTraceID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != TraceIDLength || !ValidateTraceID(id) || seen[id] {
			t.Fatalf("invalid or repeated trace id %q", id)
		}
		seen[id] = true
		if strings.ContainsAny(id, "/\\:@\r\n\x00") {
			t.Fatalf("trace id contains unsafe data: %q", id)
		}
	}
}

func TestValidateTraceIDRejectsControlsArbitraryAndWrongLength(t *testing.T) {
	valid, err := GenerateTraceID()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{
		"", valid + "x", strings.ToUpper(valid),
		"fic-v1-aaaaaaaaaaaaaaaaaaaaaaaaa\n",
		"fic-v1-aaaaaaaaaaaaaaaaaaaaaaaa!",
		"fic-v1-" + strings.Repeat("a", 100000),
		"fic-v1-user-name",
	} {
		if ValidateTraceID(candidate) {
			t.Errorf("ValidateTraceID(%q) accepted unsafe/arbitrary value", candidate)
		}
	}
}

func TestComposeClaudeHeadersPreservesExistingAndUnrelatedHeaders(t *testing.T) {
	generated, err := GenerateTraceID()
	if err != nil {
		t.Fatal(err)
	}
	existing := "X-Foo: bar\ncontent-type: application/json"
	composed, got, source, err := ComposeClaudeCustomHeaders(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if got != generated || source != SourceCompanionGenerated || !strings.Contains(composed, existing) || !strings.Contains(composed, "X-Session-ID: "+generated) {
		t.Fatalf("composed headers = %q, id=%q, source=%q", composed, got, source)
	}

	userID, _ := GenerateTraceID()
	userBlock := "X-Foo: bar\nx-session-id: " + userID
	composed, got, source, err = ComposeClaudeCustomHeaders(userBlock, generated)
	if err != nil || composed != userBlock || got != userID || source != SourceExistingHeader {
		t.Fatalf("existing session header was not preserved: %q, %q, %q, %v", composed, got, source, err)
	}
}

func TestComposeClaudeHeadersAddsStaticClassification(t *testing.T) {
	generated, err := GenerateTraceID()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := NewCorrelationMetadata("claude-code", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	composed, got, source, err := ComposeClaudeCustomHeadersWithMetadata("X-Unrelated: retained", generated, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if got != generated || source != SourceCompanionGenerated {
		t.Fatalf("trace = %q, %q", got, source)
	}
	for _, want := range []string{
		"X-Session-ID: " + generated,
		"X-FI-Client: claude-code",
		"X-FI-Companion-Version: 0.2.0",
		"X-FI-Workload: coding-agent",
	} {
		if !strings.Contains(composed, want) {
			t.Fatalf("composed headers missing %q: %q", want, composed)
		}
	}
	for _, forbidden := range []string{"prompt", "cwd", "repository", "api-key", "transcript"} {
		if strings.Contains(strings.ToLower(composed), forbidden) {
			t.Fatalf("composed headers contain forbidden metadata %q: %q", forbidden, composed)
		}
	}
}

func TestComposeClaudeHeadersPreservesExistingClassificationAndRejectsConflicts(t *testing.T) {
	generated, _ := GenerateTraceID()
	existingID, _ := GenerateTraceID()
	metadata, err := NewCorrelationMetadata("codex", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	existing := "X-Session-ID: " + existingID + "\nX-FI-Client: codex"
	composed, got, source, err := ComposeClaudeCustomHeadersWithMetadata(existing, generated, metadata)
	if err != nil || got != existingID || source != SourceExistingHeader {
		t.Fatalf("existing classification was not preserved: %q, %q, %q, %v", composed, got, source, err)
	}
	if !strings.Contains(composed, "X-FI-Companion-Version: 0.2.0") || !strings.Contains(composed, "X-FI-Workload: coding-agent") {
		t.Fatalf("missing static metadata after existing trace: %q", composed)
	}

	conflict := "X-FI-Client: another-client"
	if _, _, _, err := ComposeClaudeCustomHeadersWithMetadata(conflict, generated, metadata); err == nil {
		t.Fatal("conflicting static classification must fail open")
	}
}

func TestCorrelationMetadataAndCodexMappingsAreBounded(t *testing.T) {
	if _, err := NewCorrelationMetadata("unknown-client", "0.2.0"); err == nil {
		t.Fatal("unknown client accepted")
	}
	if _, err := NewCorrelationMetadata("codex", "0.2.0;secret"); err == nil {
		t.Fatal("unsafe version accepted")
	}
	mappings := CodexHeaderMappings()
	if len(mappings) != 4 {
		t.Fatalf("Codex mappings = %#v", mappings)
	}
	for _, mapping := range mappings {
		if strings.Contains(mapping.Header, "Session-ID") && mapping.Env != TraceSessionEnv {
			t.Fatalf("session mapping = %#v", mapping)
		}
		if strings.Contains(mapping.Header, "Companion-Version") && mapping.Env != TraceCompanionVersionEnv {
			t.Fatalf("version mapping = %#v", mapping)
		}
	}
}

func TestComposeClaudeHeadersMalformedFailsOpen(t *testing.T) {
	generated, _ := GenerateTraceID()
	for _, block := range []string{"not-a-header", "X-Foo: ok\n\nX-Bar: ok", "X-Foo: bad\r\nX-Bar: ok", "X-Foo: bad\x01"} {
		if _, _, _, err := ComposeClaudeCustomHeaders(block, generated); err == nil {
			t.Errorf("malformed block %q was accepted", block)
		}
	}
}

func TestComposeClaudeHeadersRejectsUnverifiedExistingSession(t *testing.T) {
	generated, err := GenerateTraceID()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ComposeClaudeCustomHeaders("X-Session-ID: user-session", generated); err == nil {
		t.Fatal("arbitrary existing X-Session-ID must be reported as a trace conflict")
	}
	if err := ValidateClaudeCustomHeaders("X-Session-ID: user-session"); err == nil {
		t.Fatal("header validation must report an unverified session header")
	}
}

func FuzzComposeClaudeCustomHeadersDoesNotPanic(f *testing.F) {
	f.Add("X-Client: cli\nContent-Type: application/json")
	f.Add("X-Session-ID: user-session")
	f.Add("X-Broken")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > maxHeaderBlockBytes*2 {
			return
		}
		generated, err := GenerateTraceID()
		if err != nil {
			t.Fatal(err)
		}
		composed, id, source, composeErr := ComposeClaudeCustomHeaders(input, generated)
		if len(composed) > maxHeaderBlockBytes+TraceIDLength+len(SessionHeader)+2 && composeErr == nil {
			t.Fatalf("composed headers exceeded expected bound: %d", len(composed))
		}
		if composeErr == nil && source != SourceNone && !ValidateTraceID(id) {
			t.Fatalf("successful composition returned invalid id %q", id)
		}
	})
}

func TestReceiptIsPrivateAndCannotDeleteArbitraryPath(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	r := LaunchReceipt{
		TraceID:        "fic-v1-aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Client:         "claude-code",
		Provider:       "freeinference",
		EndpointOrigin: "https://freeinference.org",
		StartedAt:      time.Now().UTC(),
		HeaderName:     SessionHeader,
		Source:         SourceCompanionGenerated,
	}
	path, err := WriteLaunchReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("receipt permissions = %v, %v", info.Mode().Perm(), err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil || dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("receipt directory permissions = %v, %v", dirInfo.Mode().Perm(), err)
	}

	arbitrary := filepath.Join(t.TempDir(), "do-not-delete")
	if err := os.WriteFile(arbitrary, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConsumeLaunchReceipt(arbitrary, "claude-code", r.EndpointOrigin); err == nil {
		t.Fatal("arbitrary receipt path was accepted")
	}
	if _, err := os.Stat(arbitrary); err != nil {
		t.Fatalf("arbitrary file was changed: %v", err)
	}
	consumed, err := ConsumeLaunchReceipt(path, r.Client, r.EndpointOrigin)
	if err != nil || consumed.TraceID != r.TraceID {
		t.Fatalf("consume = %#v, %v", consumed, err)
	}
}
