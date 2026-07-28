package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	t.Setenv("FREEINFERENCE_BASE_URL", server.URL)
	t.Setenv("FREEINFERENCE_API_KEY", "")

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
	if code != 0 {
		t.Errorf("unknown worker should be a quiet skip, exit = %d", code)
	}
}

func TestReportFormatValidation(t *testing.T) {
	var out, errOut strings.Builder
	code := cmdReport(testPaths(t), []string{"--format", "yaml"}, &out, &errOut)
	if code != 1 {
		t.Errorf("bad format exit = %d", code)
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
	if code != 1 {
		t.Errorf("exit = %d", code)
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
