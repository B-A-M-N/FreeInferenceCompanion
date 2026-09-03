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

	"github.com/b-a-m-n/freeinference-companion/internal/render"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func testPaths(t *testing.T) state.Paths {
	t.Helper()
	return state.NewPathsWithDir(t.TempDir())
}

func exposeRunningBinaryOnPath(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		return
	}
	fiDir := t.TempDir()
	fiPath := filepath.Join(fiDir, "freeinference")
	if err := os.Symlink(exe, fiPath); err == nil {
		t.Setenv("PATH", fiDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
}

func TestRenderConfigHonorsSpacedColorFlag(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if got := renderConfigWith([]string{"--color", "always"}).ColorMode; got != render.ColorAlways {
		t.Errorf("--color always mode = %v, want ColorAlways", got)
	}
	if got := renderConfigWith([]string{"--color", "never"}).ColorMode; got != render.ColorNever {
		t.Errorf("--color never mode = %v, want ColorNever", got)
	}
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

	t.Setenv("FI_CUSTOM_ENDPOINT", server.URL)
	t.Setenv("FI_CUSTOM_API_KEY", "custom-test-key")
	t.Setenv("FREEINFERENCE_BASE_URL", "")
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("FI_HEALTH_URL", "")
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")

	// Put the running binary on PATH so `freeinference` resolves correctly.
	exposeRunningBinaryOnPath(t)

	var out, errOut strings.Builder
	code := cmdDoctor(testPaths(t), nil, &out, &errOut)
	output := out.String()

	// Every check must appear — doctor does not stop at the first failure.
	for _, want := range []string{
		"Cache directory:",
		"State schema:",
		"freeinference binary:",
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
	t.Setenv("FI_CUSTOM_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("FI_CUSTOM_API_KEY", "custom-test-key")
	t.Setenv("FREEINFERENCE_BASE_URL", "")
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("FI_HEALTH_URL", "")
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")

	var out, errOut strings.Builder
	code := cmdDoctor(testPaths(t), nil, &out, &errOut)
	if code != 1 {
		t.Errorf("doctor exit = %d, want 1 when checks fail", code)
	}
	if !strings.Contains(out.String(), "failed") {
		t.Errorf("doctor should summarize failures:\n%s", out.String())
	}
}

func TestDoctorDoesNotProbeUnverifiedEndpoint(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("FI_CUSTOM_ENDPOINT", "")
	t.Setenv("FI_CUSTOM_API_KEY", "")
	t.Setenv("FREEINFERENCE_BASE_URL", server.URL)
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("FI_ALLOW_INSECURE_LOCALHOST", "1")
	t.Setenv("FI_HEALTH_URL", "")
	exposeRunningBinaryOnPath(t)

	var out, errOut strings.Builder
	code := cmdDoctor(testPaths(t), []string{"--probe", "--model", "glm-5.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("unverified doctor exit = %d, want 0 with skipped network checks:\n%s", code, out.String())
	}
	if calls != 0 {
		t.Fatalf("unverified doctor made %d network request(s)", calls)
	}
	if !strings.Contains(out.String(), "FreeInference route not verified; no request sent") {
		t.Fatalf("doctor did not disclose skipped probe:\n%s", out.String())
	}
}

func TestNewAPIClientUsesActiveRuntimeEndpointAndCredential(t *testing.T) {
	t.Setenv("FREEINFERENCE_BASE_URL", "")
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "free-inference-test")
	t.Setenv("ANTHROPIC_API_KEY", "")

	client, err := newAPIClient()
	if err != nil {
		t.Fatal(err)
	}
	if got := client.BaseURL(); got != "https://freeinference.org/v1" {
		t.Errorf("base URL = %q", got)
	}
	if got := client.APIKey(); got != "free-inference-test" {
		t.Errorf("API key was not sourced from active runtime credential")
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
		{"missing color value", []string{"--color"}},
		{"unknown color value", []string{"--color", "purple"}},
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
	code := Run([]string{"freeinference", "bogus-command"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Errorf("exit = %d", code)
	}
}

func TestRunHookNeverPanics(t *testing.T) {
	// Even with completely empty environment, hook commands return 0.
	t.Setenv("FI_CACHE_DIR", t.TempDir())
	var out, errOut strings.Builder
	code := Run([]string{"freeinference", "hook", "claude-code", "SessionStart"}, strings.NewReader("{bad json"), &out, &errOut)
	if code != 0 {
		t.Errorf("hook exit = %d", code)
	}
}

func TestAutomaticClaudeStatusIsNoOpForGenericOnlyEnvironment(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "state")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FI_CACHE_DIR", cacheDir)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "generic-only-key")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	var out, errOut strings.Builder
	code := Run([]string{"freeinference", "status", "--compact"}, strings.NewReader(`{"session_id":"ordinary","model":{"id":"m"}}`), &out, &errOut)
	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("generic-only automatic status leaked output: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("inactive automatic status created state directory: err=%v", err)
	}
}

func TestInactiveClaudeHookDoesNotCreateState(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "state")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FI_CACHE_DIR", cacheDir)
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "generic-only-key")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	var out, errOut strings.Builder
	code := Run([]string{"freeinference", "hook", "claude-code", "SessionStart"}, strings.NewReader(`{"session_id":"ordinary","model":"m"}`), &out, &errOut)
	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("inactive hook leaked output: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("inactive hook created state directory: err=%v", err)
	}
}

func TestAutomaticRefreshRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("FI_AUTO_REFRESH", "")
	if automaticRefreshEnabled() {
		t.Fatal("automatic lifecycle refresh must be disabled by default")
	}

	t.Setenv("FI_AUTO_REFRESH", "1")
	if !automaticRefreshEnabled() {
		t.Fatal("FI_AUTO_REFRESH=1 must enable automatic lifecycle refresh")
	}
}

func TestNormalLifecycleRefreshDoesNotSpawnWorkers(t *testing.T) {
	t.Setenv("FI_AUTO_REFRESH", "")
	t.Setenv("FI_NO_BACKGROUND", "")
	activation := runtime.Activation{
		Active: true,
		Client: runtime.ClientClaudeCode,
		Origin: "https://freeinference.org",
	}
	spawned := false
	maybeRequestDetachedRefreshWith(state.NewPathsWithDir(t.TempDir()), activation, func(string, []string) error {
		spawned = true
		return nil
	})
	if spawned {
		t.Fatal("normal lifecycle operation must not spawn refresh workers")
	}
}

func TestClaudeHookStillDispatchesStopFailure(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	var out strings.Builder

	handleClaudeHook(paths, "SessionStart",
		strings.NewReader(`{"session_id":"claude-stop-failure","model":"m1"}`),
		&out, runtime.Activation{})
	handleClaudeHook(paths, "StopFailure",
		strings.NewReader(`{"session_id":"claude-stop-failure","error":"429 rate limit"}`),
		&out, runtime.Activation{})

	snap, err := state.LoadSnapshot(paths, schema.ClientClaudeCode, "claude-stop-failure")
	if err != nil || snap == nil || snap.LastFailure == nil {
		t.Fatalf("Claude StopFailure was not dispatched: snap=%#v err=%v", snap, err)
	}
	events, err := state.ReadEvents(paths, schema.ClientClaudeCode, "claude-stop-failure", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == state.EventTurnFailed {
			return
		}
	}
	t.Fatal("Claude StopFailure did not append turn_failed event")
}

func TestCodexFooterCommandIsReversible(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	original := "[tui]\nstatus_line = [\"model\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)

	var out, errOut strings.Builder
	if code := Run([]string{"freeinference", "codex-footer", "install"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("install exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"freeinference", "codex-footer", "status", "--json"}, strings.NewReader(""), &out, &errOut); code != 0 || !strings.Contains(out.String(), `"status": "installed"`) {
		t.Fatalf("status exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if code := Run([]string{"freeinference", "codex-footer", "uninstall"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("uninstall exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("footer command did not restore config: %q", restored)
	}
}

func TestHistoricalSnapshotRemainsInspectableWhenInactive(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("FI_CACHE_DIR", cacheDir)
	t.Setenv("FREEINFERENCE_BASE_URL", "")
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	paths := state.NewPathsWithDir(cacheDir)
	snap := minimalSnapshot("historical")
	snap.Session.Status = schema.SessionCompleted
	if err := state.SaveSnapshot(paths, schema.ClientClaudeCode, snap.Session.ID, snap); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	code := Run([]string{"freeinference", "snapshot", "--session", "historical"}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("historical snapshot exit = %d, stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Historical session — FreeInference is not currently active.") {
		t.Fatalf("historical snapshot lacked explicit diagnostic label: %q", out.String())
	}
}

func TestCodexStatusJSONReportsTelemetryUnavailable(t *testing.T) {
	input := minimalSnapshot("codex-json")
	input.Client.Type = schema.ClientCodex
	input.Provider = schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true}
	usedPct := 42.0
	input.LiveContext = &schema.LiveContext{UsedPercentage: &usedPct}
	input.CacheAnalysis = &schema.CacheAnalysis{RequestSamples: 4, ObservationCount: 4}

	var out strings.Builder
	statusJSON(&out, input, nil, false, "", nil, schema.ClientCodex, input.Session.ID, input.Model.ID, input.Provider.Name)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"context", "cache"} {
		section, ok := decoded[field].(map[string]any)
		if !ok || section["availability"] != "unavailable" || section["reason"] != "client_telemetry_unavailable" {
			t.Errorf("%s = %#v, want explicit unavailable telemetry", field, decoded[field])
		}
	}
}

func TestCodexStatusWithoutSessionShowsVerifiedConfiguration(t *testing.T) {
	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(`model_provider = "freeinference"

[model_providers.freeinference]
name = "FreeInference"
base_url = "https://freeinference.org/v1"
env_key = "FREEINFERENCE_API_KEY"
wire_api = "responses"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("FREEINFERENCE_API_KEY", "codex-status-test-key")
	t.Setenv("FI_CACHE_DIR", t.TempDir())
	t.Setenv("FI_DISABLED", "")

	var out, errOut strings.Builder
	code := cmdStatus(testPaths(t), []string{"--client", "codex", "--level", "standard"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("status exit = %d, stderr=%q", code, errOut.String())
	}
	for _, want := range []string{
		"Client:   codex",
		"Provider: freeinference (verified from Codex configuration)",
		"Session:  no local Codex session (plugin is skill-only)",
		"Live Context: unavailable",
		"Cache Analysis: unavailable",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "FI: no session") {
		t.Fatalf("Codex configuration-only status regressed to generic no-session output: %q", out.String())
	}
}

func TestCodexStatusWithoutSessionJSONPreservesAvailabilityBoundary(t *testing.T) {
	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(`model_provider = "freeinference"

[model_providers.freeinference]
base_url = "https://freeinference.org/v1"
env_key = "FREEINFERENCE_API_KEY"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("FREEINFERENCE_API_KEY", "codex-status-test-key")
	t.Setenv("FI_CACHE_DIR", t.TempDir())
	t.Setenv("FI_DISABLED", "")

	var out, errOut strings.Builder
	code := cmdStatus(testPaths(t), []string{"--client", "codex", "--json"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("status --json exit = %d, stderr=%q", code, errOut.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["provider"] != string(schema.ProviderFreeInference) || decoded["selection_verified"] != true {
		t.Fatalf("configuration evidence = %#v", decoded)
	}
	for _, field := range []string{"context", "cache"} {
		section, ok := decoded[field].(map[string]any)
		if !ok || section["availability"] != "unavailable" || section["reason"] != "client_telemetry_unavailable" {
			t.Errorf("%s = %#v, want explicit unavailable telemetry", field, decoded[field])
		}
	}
}

// TestDoctorProbeWithInvalidEndpoint is the P0-1 regression test: an invalid
// API URL combined with `freeinference doctor --probe` must NOT panic or make a
// request. It reports the unverified route and exits cleanly.
func TestDoctorProbeWithInvalidEndpoint(t *testing.T) {
	// Invalid URL containing userinfo — fails ValidateBaseURL.
	t.Setenv("FREEINFERENCE_BASE_URL", "https://user:pass@freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
	t.Setenv("FI_HEALTH_URL", "")
	exposeRunningBinaryOnPath(t)

	var out, errOut strings.Builder
	code := cmdDoctor(testPaths(t), []string{"--probe", "--model", "test-model"}, &out, &errOut)
	if code != 0 {
		t.Errorf("doctor --probe with invalid endpoint: exit = %d, want 0 (output:\n%s)", code, out.String())
	}
	// The inference probe must be skipped, not panicked against.
	output := out.String()
	if strings.Contains(output, "panic") {
		t.Fatalf("doctor panicked on invalid endpoint:\n%s", output)
	}
	if !strings.Contains(output, "Inference probe") {
		t.Errorf("expected 'Inference probe' check in output:\n%s", output)
	}
	if !strings.Contains(output, "FreeInference route not verified; no request sent") {
		t.Errorf("expected probe to be skipped without a request:\n%s", output)
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
	code := Run([]string{"freeinference", "hook", "claude-code", "SessionStart"}, strings.NewReader("{}"), &out, &errOut)
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
	// Provider-state commands (status, report) must be blocked in
	// disabled mode: exit 1 and print the DISABLED warning.
	// Note: doctor is NOT blocked — it runs and reports skipped checks.
	tests := []struct {
		args []string
	}{
		{args: []string{"freeinference", "status"}},
		{args: []string{"freeinference", "report"}},
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

	// The diagnostic contract includes the binary-resolvability check. CI does
	// not normally put the Go test executable on PATH, so expose it under the
	// command name the installed hooks use.
	if exe, err := os.Executable(); err == nil {
		fiDir := t.TempDir()
		fiPath := filepath.Join(fiDir, "freeinference")
		if err := os.Symlink(exe, fiPath); err == nil {
			t.Setenv("PATH", fiDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}

	var out, errOut strings.Builder
	code := Run([]string{"freeinference", "doctor"}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Errorf("disabled doctor exit = %d, want 0 (diagnostic commands work when disabled):\n%s", code, out.String())
	}
	// Diagnostic commands must not probe the provider in disabled mode.
	output := out.String()
	if strings.Contains(output, "Inference probe") {
		t.Error("disabled doctor must not probe inference endpoints")
	}
	if !strings.Contains(output, "skipped - disabled") {
		t.Error("disabled doctor should report skipped checks, got:", output)
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
