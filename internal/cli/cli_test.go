package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func testPaths(t *testing.T) state.Paths {
	t.Helper()
	return state.NewPathsWithDir(t.TempDir())
}

func TestDoctorRunsAllChecksWithoutEarlyExit(t *testing.T) {
	// Point the API client at a local catalog server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "glm-5.1", "context_length": 200000}},
		})
	}))
	defer server.Close()

	t.Setenv("FREEINFERENCE_BASE_URL", server.URL)
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("FI_HEALTH_URL", "")
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")

	// Put the running binary on PATH so `fi` resolves correctly.
	if exe, err := os.Executable(); err == nil {
		fiDir := t.TempDir()
		fiPath := filepath.Join(fiDir, "fi")
		if err := os.Symlink(exe, fiPath); err == nil {
			t.Setenv("PATH", fiDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}

	var out, errOut strings.Builder
	code := cmdDoctor(testPaths(t), nil, &out, &errOut)
	output := out.String()

	// Every check must appear — doctor does not stop at the first failure.
	for _, want := range []string{
		"Cache directory:",
		"State schema:",
		"fi binary:",
		"Provider detection:",
		"Health source:",
		"API endpoint:",
		"Model catalog:",
		"Authentication:",
		"Model access:",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("doctor output missing %q:\n%s", want, output)
		}
	}
	// Catalog is reachable locally → no failures → exit 0.
	if code != 0 {
		t.Errorf("doctor exit = %d, output:\n%s", code, output)
	}
	// Authentication must be reported unknown, never inferred.
	if !strings.Contains(output, "Authentication:        ?") {
		t.Errorf("authentication must be unknown:\n%s", output)
	}
}

func TestDoctorFailsWhenEndpointDown(t *testing.T) {
	t.Setenv("FREEINFERENCE_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("FI_HEALTH_URL", "")

	var out, errOut strings.Builder
	code := cmdDoctor(testPaths(t), nil, &out, &errOut)
	if code != 1 {
		t.Errorf("doctor exit = %d, want 1 when checks fail", code)
	}
	if !strings.Contains(out.String(), "failed") {
		t.Errorf("doctor should summarize failures:\n%s", out.String())
	}
}

func TestRefreshWorkerFlag(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": "m1"}}})
	}))
	defer server.Close()

	// Use the test server URL with the insecure-localhost opt-in since
	// httptest serves on HTTP loopback.
	t.Setenv("FREEINFERENCE_BASE_URL", server.URL)
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")

	var out, errOut strings.Builder
	code := cmdRefresh(testPaths(t), []string{"--worker", "models"}, &out, &errOut)
	if code != 0 {
		t.Errorf("worker exit = %d (%s)", code, errOut.String())
	}
	if calls != 1 {
		t.Errorf("server calls = %d", calls)
	}
	if !strings.Contains(out.String(), "Models refreshed") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRefreshWorkerUnknownName(t *testing.T) {
	var out, errOut strings.Builder
	code := cmdRefresh(testPaths(t), []string{"--worker", "bogus"}, &out, &errOut)
	if code != 2 {
		t.Errorf("unknown worker should be a usage error, exit = %d, want 2", code)
	}
}

func TestReportFormatValidation(t *testing.T) {
	var out, errOut strings.Builder
	code := cmdReport(testPaths(t), []string{"--format", "yaml"}, &out, &errOut)
	if code != 2 {
		t.Errorf("bad format exit = %d, want 2 (usage error)", code)
	}
}

// TestStrictFlagParsing verifies that unknown flags and unexpected arguments
// are rejected with usage errors (exit 2) rather than silently ignored.
func TestStrictFlagParsing(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--unknown-flag"}},
		{"unknown flag with value", []string{"--bogus", "value"}},
		{"missing client value", []string{"--client"}},
		{"missing session value", []string{"--session"}},
		{"missing format value", []string{"--format"}},
		{"unknown client", []string{"--client", "vim"}},
		{"unexpected arg", []string{"extra-arg"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut strings.Builder
			code := cmdStatus(testPaths(t), tt.args, nil, &out, &errOut)
			if code != 2 {
				t.Errorf("%s: exit = %d, want 2 (usage error)", tt.name, code)
			}
		})
	}
}

func TestReportJSONFormat(t *testing.T) {
	t.Setenv("FI_HEALTH_URL", "")
	var out, errOut strings.Builder
	code := cmdReport(testPaths(t), []string{"--format", "json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("report exit = %d", code)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("report --format json must be valid JSON: %v", err)
	}
	note, _ := parsed["note"].(string)
	if !strings.Contains(note, "Review it before sharing") {
		t.Errorf("report note = %q", note)
	}
}

func TestStatusLineSubcommandValidation(t *testing.T) {
	var out, errOut strings.Builder
	code := cmdStatusLine([]string{"bogus"}, &out, &errOut)
	if code != 2 {
		t.Errorf("exit = %d, want 2 (usage error)", code)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut strings.Builder
	code := Run([]string{"fi", "bogus-command"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Errorf("exit = %d", code)
	}
}

func TestRunHookNeverPanics(t *testing.T) {
	// Even with completely empty environment, hook commands return 0.
	t.Setenv("FI_CACHE_DIR", t.TempDir())
	var out, errOut strings.Builder
	code := Run([]string{"fi", "hook", "claude-code", "SessionStart"}, strings.NewReader("{bad json"), &out, &errOut)
	if code != 0 {
		t.Errorf("hook exit = %d", code)
	}
}

// TestDoctorProbeWithInvalidEndpoint is the P0-1 regression test: an invalid
// API URL combined with `fi doctor --probe` must NOT panic. It must skip the
// inference probe, report the configuration failure, and exit 1 (not a runtime
// panic exit 2).
func TestDoctorProbeWithInvalidEndpoint(t *testing.T) {
	// Invalid URL containing userinfo — fails ValidateBaseURL.
	t.Setenv("FREEINFERENCE_BASE_URL", "https://user:pass@freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	t.Setenv("FI_HEALTH_URL", "")

	var out, errOut strings.Builder
	code := cmdDoctor(testPaths(t), []string{"--probe", "--model", "test-model"}, &out, &errOut)
	if code != 1 {
		t.Errorf("doctor --probe with invalid endpoint: exit = %d, want 1 (output:\n%s)", code, out.String())
	}
	// The inference probe must be skipped, not panicked against.
	output := out.String()
	if strings.Contains(output, "panic") {
		t.Fatalf("doctor panicked on invalid endpoint:\n%s", output)
	}
	if !strings.Contains(output, "Inference probe") {
		t.Errorf("expected 'Inference probe' check in output:\n%s", output)
	}
	if !strings.Contains(output, "skipped due to invalid endpoint") {
		t.Errorf("expected probe to be skipped due to invalid endpoint:\n%s", output)
	}
}

func TestContextUnknownNeverZero(t *testing.T) {
	paths := testPaths(t)
	// Seed a session with no telemetry.
	snap := minimalSnapshot("s1")
	if err := state.SaveSnapshot(paths, "claude-code", "s1", snap); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FI_SESSION_ID", "s1")

	var out, errOut strings.Builder
	code := cmdContext(paths, nil, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("context exit = %d", code)
	}
	output := out.String()
	if !strings.Contains(output, "Context:    unknown") {
		t.Errorf("context output:\n%s", output)
	}
	if strings.Contains(output, "0.0%") {
		t.Errorf("missing metrics must not render as zero:\n%s", output)
	}
	if !strings.Contains(output, "insufficient telemetry") {
		t.Errorf("suggestion should say insufficient telemetry:\n%s", output)
	}
}

func minimalSnapshot(id string) *schema.Snapshot {
	return &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session: schema.SessionInfo{
			ID:          id,
			StartedAt:   time.Now().UTC(),
			LastEventAt: time.Now().UTC(),
			Status:      schema.SessionActive,
		},
		Model:    schema.ModelInfo{ID: "glm-5.1"},
		Pressure: schema.PressureState{State: schema.PressureUnknown},
	}
}

// ============================================================
// Disabled mode tests
// ============================================================

func TestDisabledModeHookExitsZero(t *testing.T) {
	t.Setenv("FI_DISABLED", "1")
	t.Setenv("FI_CACHE_DIR", t.TempDir())
	var out, errOut strings.Builder
	// Even with disabled, hook commands must return 0 and produce no output.
	code := Run([]string{"fi", "hook", "claude-code", "SessionStart"}, strings.NewReader("{}"), &out, &errOut)
	if code != 0 {
		t.Errorf("disabled hook exit = %d, want 0", code)
	}
	if out.String() != "" {
		t.Errorf("disabled hook stdout = %q, want empty", out.String())
	}
	if errOut.String() != "" {
		t.Errorf("disabled hook stderr = %q, want empty", errOut.String())
	}
}

func TestDisabledModeBlocksProviderStateCommands(t *testing.T) {
	t.Setenv("FI_DISABLED", "1")
	// Provider-state commands (status, report, doctor) must be blocked in
	// disabled mode: exit 1 and print the DISABLED warning.
	tests := []struct {
		args []string
	}{
		{args: []string{"fi", "status"}},
		{args: []string{"fi", "report"}},
		{args: []string{"fi", "doctor"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args[1:], ""), func(t *testing.T) {
			var out, errOut strings.Builder
			code := Run(tt.args, strings.NewReader(""), &out, &errOut)
			if code != 1 {
				t.Errorf("disabled %s exit = %d, want 1", tt.args[1], code)
			}
			errStr := errOut.String()
			if !strings.Contains(errStr, "DISABLED") && !strings.Contains(errStr, "disabled") {
				t.Errorf("disabled %s must print DISABLED warning to stderr:\n%s", tt.args[1], errStr)
			}
		})
	}
}

func TestDisabledModeDiagnosticCommandsDoNotProbe(t *testing.T) {
	t.Setenv("FI_DISABLED", "1")
	t.Setenv("FI_CACHE_DIR", t.TempDir())
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "test-key")
	var out, errOut strings.Builder
	code := Run([]string{"fi", "doctor"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Errorf("disabled doctor exit = %d, want 1", code)
	}
	// Diagnostic commands must not probe the provider in disabled mode.
	output := out.String()
	if strings.Contains(output, "Inference probe") || strings.Contains(output, "API endpoint:") {
		t.Error("disabled doctor must not probe provider endpoints")
	}
}

// TestDisabledModeActivationGate ensures the runtime activation gate correctly
// blocks when FI_DISABLED=1 even with valid endpoint+key configured.
func TestDisabledModeActivationGate(t *testing.T) {
	t.Setenv("FI_DISABLED", "1")
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "test-key")

	a := runtime.Evaluate()
	if a.Active {
		t.Fatal("FI_DISABLED=1 must prevent activation even with valid endpoint+key")
	}
	if !a.Disabled {
		t.Error("Disabled flag should be set")
	}
}
