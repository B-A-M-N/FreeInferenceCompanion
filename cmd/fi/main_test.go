package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var fiBinary string

// TestMain builds the real binary once and runs every check against it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "freeinference-test-bin")
	if err != nil {
		panic(err)
	}
	fiBinary = filepath.Join(dir, "freeinference")
	build := exec.Command("go", "build", "-o", fiBinary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("build freeinference: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// runFI executes the built binary with a hermetic environment.
func runFI(t *testing.T, cacheDir string, stdin string, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(fiBinary, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Env = append([]string{
		"FI_CACHE_DIR=" + cacheDir,
		"FI_NO_BACKGROUND=1",
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}, env...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run freeinference: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func hookInput(sessionID string) string {
	return `{"session_id":"` + sessionID + `","hook_event_name":"UserPromptSubmit","model":"glm-5.1"}`
}

func statusPayload(sessionID string, usedPct float64, totalIn int64) string {
	return `{
		"model": {"id": "glm-5.1", "display_name": "GLM 5.1"},
		"session_id": "` + sessionID + `",
		"transcript_path": "/tmp/t",
		"context_window": {
			"total_input_tokens": ` + jsonInt(totalIn) + `,
			"total_output_tokens": 2000,
			"context_window_size": 200000,
			"used_percentage": ` + jsonFloat(usedPct) + `,
			"current_usage": {"input_tokens": 5000, "output_tokens": 2000, "cache_creation_input_tokens": 5000, "cache_read_input_tokens": 150000}
		}
	}`
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func jsonFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestHookMalformedJSONExitsZero(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runFI(t, dir, "{not json", nil, "hook", "claude-code", "UserPromptSubmit")
	if code != 0 {
		t.Errorf("malformed hook JSON must exit 0, got %d", code)
	}
}

func TestHookUnknownClientExitsZero(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runFI(t, dir, "{}", nil, "hook", "unknown-client", "SessionStart")
	if code != 0 {
		t.Errorf("unknown client must exit 0, got %d", code)
	}
}

func TestHookUnknownEventExitsZero(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runFI(t, dir, hookInput("s1"), nil, "hook", "claude-code", "NotARealEvent")
	if code != 0 {
		t.Errorf("unknown event must exit 0, got %d", code)
	}
}

func TestHookMissingArgsExitsZero(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runFI(t, dir, "", nil, "hook")
	if code != 0 {
		t.Errorf("missing hook args must exit 0, got %d", code)
	}
}

func TestHookMissingCacheDirExitsZero(t *testing.T) {
	// A path that cannot be created.
	_, _, code := runFI(t, "/proc/1/freeinference-impossible", hookInput("s1"), nil, "hook", "claude-code", "SessionStart")
	if code != 0 {
		t.Errorf("unwritable state must exit 0, got %d", code)
	}
}

func TestPersistentDisablePreventsHookStateWrites(t *testing.T) {
	dir := t.TempDir()
	configDir := t.TempDir()
	env := []string{
		"FREEINFERENCE_BASE_URL=https://freeinference.org/v1",
		"FREEINFERENCE_API_KEY=hyi-test-12345",
		"FI_CONFIG_DIR=" + configDir,
	}
	_, stderr, code := runFI(t, dir, "", env, "companion", "disable", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("disable exit=%d stderr=%q", code, stderr)
	}
	stdout, stderr, code := runFI(t, dir, hookInput("disabled-session"), env, "hook", "claude-code", "SessionStart")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("disabled hook exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) != 0 {
		t.Fatalf("disabled hook wrote cache state: %v", entries)
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestHookLockBusyExitsZero(t *testing.T) {
	dir := t.TempDir()
	sessionID := "lock-test"

	// Create the session first (needs activation env vars).
	runFI(t, dir, hookInput(sessionID),
		[]string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"},
		"hook", "claude-code", "SessionStart")

	// Hold the session lock from the test process.
	lockDir := filepath.Join(dir, "sessions", "claude-code")
	matches, err := filepath.Glob(filepath.Join(lockDir, "*", "lock"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("session lock not found: %v", matches)
	}
	f, err := os.OpenFile(matches[0], os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock: %v", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	start := time.Now()
	_, _, code := runFI(t, dir, hookInput(sessionID), nil, "hook", "claude-code", "UserPromptSubmit")
	if code != 0 {
		t.Errorf("lock busy must exit 0, got %d", code)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("lock busy must return promptly")
	}
}

func TestHookNoWarningEmptyStdout(t *testing.T) {
	dir := t.TempDir()
	sessionID := "quiet-session"

	runFI(t, dir, statusPayload(sessionID, 40, 80000), nil, "status", "--compact", "--client", "claude-code")

	stdout, _, code := runFI(t, dir, hookInput(sessionID),
		[]string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"},
		"hook", "claude-code", "UserPromptSubmit")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("no-warning hook must produce no stdout, got %q", stdout)
	}
}

func TestHookWarningProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	sessionID := "warn-session"

	runFI(t, dir, statusPayload(sessionID, 84, 168000),
		[]string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"},
		"status", "--compact", "--client", "claude-code")

	stdout, _, code := runFI(t, dir, hookInput(sessionID),
		[]string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"},
		"hook", "claude-code", "UserPromptSubmit")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	var parsed struct {
		Continue       bool   `json:"continue"`
		SystemMessage  string `json:"systemMessage"`
		SuppressOutput bool   `json:"suppressOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &parsed); err != nil {
		t.Fatalf("warning must be valid JSON: %v (%q)", err, stdout)
	}
	if !parsed.Continue || parsed.SystemMessage == "" || !parsed.SuppressOutput {
		t.Errorf("claude warning shape wrong: %+v", parsed)
	}
}

func TestHookWarningSuppressedOnOtherProvider(t *testing.T) {
	dir := t.TempDir()
	sessionID := "other-provider"

	runFI(t, dir, statusPayload(sessionID, 95, 190000), nil, "status", "--compact", "--client", "claude-code")

	stdout, _, code := runFI(t, dir, hookInput(sessionID),
		[]string{"FREEINFERENCE_API_KEY=", "ANTHROPIC_BASE_URL=https://api.anthropic.com"},
		"hook", "claude-code", "UserPromptSubmit")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("no FI warning may fire on a non-FI session, got %q", stdout)
	}
}

func TestCodexHookNeverEmitsSuppressOutput(t *testing.T) {
	dir := t.TempDir()
	sessionID := "codex-session"
	input := `{"session_id":"` + sessionID + `","hook_event_name":"UserPromptSubmit","model":"glm-5.1"}`

	stdout, _, code := runFI(t, dir, input,
		[]string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"},
		"hook", "codex", "UserPromptSubmit")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if strings.Contains(stdout, "suppressOutput") {
		t.Errorf("codex output must not contain suppressOutput: %q", stdout)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("codex must not emit warnings without telemetry: %q", stdout)
	}
}

func TestStatusResolvesLatestSession(t *testing.T) {
	dir := t.TempDir()
	env := []string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"}

	runFI(t, dir, hookInput("sess-resolve"), env, "hook", "claude-code", "SessionStart")

	// The expanded themed renderer shows FREEINFERENCE header + Provider line when a session resolves.
	stdout, _, code := runFI(t, dir, "", env, "status", "--include-identifiers")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout, "FREEINFERENCE") {
		t.Errorf("freeinference status should show companion info when session resolves, got %q", stdout)
	}
	if !strings.Contains(stdout, "Provider") {
		t.Errorf("freeinference status should show provider info when session resolves, got %q", stdout)
	}
}

func TestStatusWithNoSessionIsHonest(t *testing.T) {
	dir := t.TempDir()
	env := []string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"}
	stdout, _, code := runFI(t, dir, "", env, "status")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout, "no session") {
		t.Errorf("expected honest no-data output, got %q", stdout)
	}
}

func TestReportReadsCodexState(t *testing.T) {
	dir := t.TempDir()
	env := []string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"}
	input := `{"session_id":"codex-report","hook_event_name":"SessionStart","model":"glm-5.1"}`
	runFI(t, dir, input, env, "hook", "codex", "SessionStart")

	// Default report masks the session ID — the raw value must NOT appear.
	stdout, _, code := runFI(t, dir, "", env, "report", "--client", "codex")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if strings.Contains(stdout, "codex-report") {
		t.Errorf("report must mask session ID by default, got %q", stdout)
	}
	if !strings.Contains(stdout, "code...port") {
		t.Errorf("report should show masked session ID, got %q", stdout)
	}
	if !strings.Contains(stdout, "glm-5.1") {
		t.Errorf("report should still include the model ID, got %q", stdout)
	}
	if strings.Contains(stdout, "FI_HEALTH_URL") || strings.Contains(stdout, os.Getenv("FI_HEALTH_URL")) && os.Getenv("FI_HEALTH_URL") != "" {
		t.Errorf("report must not contain environment values")
	}

	// --include-identifiers reveals the raw session ID for local debugging.
	stdout2, _, code2 := runFI(t, dir, "", env, "report", "--client", "codex", "--include-identifiers")
	if code2 != 0 {
		t.Errorf("exit = %d", code2)
	}
	if !strings.Contains(stdout2, "codex-report") {
		t.Errorf("report --include-identifiers must reveal the full session ID, got %q", stdout2)
	}
}

func TestContextMissingMetricsPrintsUnknown(t *testing.T) {
	dir := t.TempDir()
	env := []string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"}
	runFI(t, dir, hookInput("ctx-unknown"), env, "hook", "claude-code", "SessionStart")

	stdout, _, code := runFI(t, dir, "", env, "context")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout, "unknown") {
		t.Errorf("missing metrics must print unknown, got %q", stdout)
	}
	if strings.Contains(stdout, "0.0%") {
		t.Errorf("missing metrics must never render as zero, got %q", stdout)
	}
}

func TestSessionsCommand(t *testing.T) {
	dir := t.TempDir()
	env := []string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"}
	runFI(t, dir, hookInput("sess-list"), env, "hook", "claude-code", "SessionStart")

	stdout, _, code := runFI(t, dir, "", env, "sessions", "--include-identifiers")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout, "sess-list") {
		t.Errorf("sessions should list the session, got %q", stdout)
	}
}

func TestSnapshotJSON(t *testing.T) {
	dir := t.TempDir()
	env := []string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"}
	runFI(t, dir, statusPayload("snap-json", 42, 84000), env, "status", "--compact", "--client", "claude-code")

	stdout, _, code := runFI(t, dir, "", env, "snapshot", "--json", "--session", "snap-json")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	var vm map[string]any
	if err := json.Unmarshal([]byte(stdout), &vm); err != nil {
		t.Fatalf("snapshot must be valid JSON: %v", err)
	}
	if vm["model_id"] != "glm-5.1" {
		t.Errorf("model_id = %v", vm["model_id"])
	}
}

func TestRenderModes(t *testing.T) {
	dir := t.TempDir()
	env := []string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"}
	runFI(t, dir, statusPayload("render-test", 42, 84000), env, "status", "--compact", "--client", "claude-code")

	// Disable colors for stable test assertions
	line, _, code := runFI(t, dir, "", append(env, "NO_COLOR=1"), "render", "--mode", "line", "--session", "render-test")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.HasPrefix(line, "FI glm-5.1") {
		t.Errorf("line render = %q", line)
	}

	expanded, _, _ := runFI(t, dir, "", append(env, "NO_COLOR=1"), "render", "--mode", "expanded", "--session", "render-test")
	if !strings.Contains(expanded, "FREEINFERENCE") || !strings.Contains(expanded, "Pressure") {
		t.Errorf("expanded render = %q", expanded)
	}
}

func TestStatusLineCompactFromStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runFI(t, dir, statusPayload("sl-test", 42, 84000),
		[]string{"FREEINFERENCE_BASE_URL=https://freeinference.org/v1", "FREEINFERENCE_API_KEY=hyi-test-12345"},
		"status", "--compact", "--client", "claude-code")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(stdout, "ctx 42%") {
		t.Errorf("compact status line = %q", stdout)
	}
}

func TestStatusReportingLevelsAndPipedJSON(t *testing.T) {
	dir := t.TempDir()
	configDir := t.TempDir()
	env := []string{
		"FREEINFERENCE_BASE_URL=https://freeinference.org/v1",
		"FREEINFERENCE_API_KEY=hyi-test-12345",
		"FI_CONFIG_DIR=" + configDir,
		"NO_COLOR=1",
	}
	payload := statusPayload("report-levels", 42, 84000)

	summary, stderr, code := runFI(t, dir, payload, env, "status", "--level", "summary", "--client", "claude-code")
	if code != 0 || stderr != "" {
		t.Fatalf("summary exit=%d stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(summary, "FI glm-5.1") || strings.Contains(summary, "\n• Provider") {
		t.Errorf("summary must be one line, got %q", summary)
	}

	standard, _, code := runFI(t, dir, payload, env, "status", "--level", "standard", "--client", "claude-code")
	if code != 0 {
		t.Fatalf("standard exit=%d", code)
	}
	if !strings.Contains(standard, "Pressure") || strings.Contains(standard, "Cache Analysis") {
		t.Errorf("standard output should have core metrics only, got:\n%s", standard)
	}

	detailed, _, code := runFI(t, dir, payload, env, "status", "--level", "detailed", "--client", "claude-code")
	if code != 0 {
		t.Fatalf("detailed exit=%d", code)
	}
	if !strings.Contains(detailed, "Cache Analysis") {
		t.Errorf("detailed output should include diagnostics, got:\n%s", detailed)
	}

	jsonOut, _, code := runFI(t, dir, payload, env, "status", "--json", "--client", "claude-code")
	if code != 0 {
		t.Fatalf("piped json exit=%d", code)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("piped status --json must emit JSON: %v\n%s", err, jsonOut)
	}
	if parsed["model"] != "glm-5.1" {
		t.Errorf("piped status json model = %v", parsed["model"])
	}

	_, stderr, code = runFI(t, dir, "", env, "config", "set", "reporting.level", "summary")
	if code != 0 || stderr != "" {
		t.Fatalf("set reporting level exit=%d stderr=%q", code, stderr)
	}
	configured, _, code := runFI(t, dir, payload, env, "status", "--client", "claude-code")
	if code != 0 || !strings.HasPrefix(configured, "FI glm-5.1") {
		t.Errorf("configured summary output = %q (exit %d)", configured, code)
	}
}

// TestReleaseVersionStamp verifies that release builds update the package-level
// version used by both CLI output and installation/update comparisons. Tags use
// a leading "v" externally, but the binary contract is canonical semver.
func TestReleaseVersionStamp(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "freeinference")
	build := exec.Command("go", "build", "-ldflags", "-X github.com/b-a-m-n/freeinference-companion/pkg/version.Version=2.3.4 -X main.commit=test", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build stamped binary: %v", err)
	}

	cmd := exec.Command(bin, "version", "--json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run stamped binary: %v", err)
	}
	var got struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse stamped version output: %v", err)
	}
	if got.Version != "2.3.4" || got.Commit != "test" {
		t.Errorf("stamped version = %+v, want version 2.3.4 and commit test", got)
	}

	help := exec.Command(bin, "help")
	helpOut, err := help.Output()
	if err != nil {
		t.Fatalf("run stamped binary help: %v", err)
	}
	if !strings.HasPrefix(string(helpOut), "FreeInference Companion v2.3.4\n") {
		t.Errorf("stamped help header = %q", strings.SplitN(string(helpOut), "\n", 2)[0])
	}
}
