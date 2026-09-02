package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func TestCmdRunUsesDocumentedClientExecutables(t *testing.T) {
	clearRunTraceEnv(t)
	binDir := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)

	originalExec := execTarget
	t.Cleanup(func() { execTarget = originalExec })
	var gotPath string
	var gotArgv []string
	execTarget = func(path string, argv []string, _ []string) error {
		gotPath = path
		gotArgv = append([]string(nil), argv...)
		return errors.New("test exec boundary")
	}

	for _, name := range []string{"claude", "codex"} {
		gotPath = ""
		gotArgv = nil
		var stderr strings.Builder
		if code := cmdRun([]string{name, "--help"}, nil, &stderr); code != 1 {
			t.Fatalf("run %s exit=%d stderr=%q, want exec-boundary failure", name, code, stderr.String())
		}
		if filepath.Base(gotPath) != name || len(gotArgv) == 0 || gotArgv[0] != name || gotArgv[1] != "--help" {
			t.Fatalf("run %s invoked path=%q argv=%q", name, gotPath, gotArgv)
		}
	}
}

func clearRunTraceEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"FI_CONFIG_DIR", "FI_TRACING", "FI_DISABLED", "FI_RUNTIME_INACTIVE", "FI_UNSAFE_FORCE_ACTIVATION",
		"FREEINFERENCE_BASE_URL", "FREEINFERENCE_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY",
		"OPENAI_BASE_URL", "OPENAI_API_KEY", "ANTHROPIC_CUSTOM_HEADERS", "FI_TRACE_SESSION_ID", "FI_TRACE_MANAGED", "FI_TRACE_SOURCE", "FI_TRACE_CLIENT", "FI_TRACE_COMPANION_VERSION", "FI_TRACE_WORKLOAD", "FI_TRACE_RECEIPT",
		"CODEX_HOME", "CODEX_PROFILE",
	} {
		t.Setenv(key, "")
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func TestPrepareLaunchClaudeGatesAndInjectsCorrelationHeaders(t *testing.T) {
	clearRunTraceEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "claude-key")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Unrelated: retained")
	prepared, err := prepareLaunch(schema.ClientClaudeCode, os.Environ(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Trace == nil || !tracing.ValidateTraceID(prepared.Trace.SessionID) || prepared.Trace.Header != tracing.SessionHeader {
		t.Fatalf("trace = %#v", prepared.Trace)
	}
	headers := envValue(prepared.Env, "ANTHROPIC_CUSTOM_HEADERS")
	if !strings.Contains(headers, "X-Unrelated: retained") || !strings.Contains(headers, "X-Session-ID: "+prepared.Trace.SessionID) ||
		!strings.Contains(headers, "X-FI-Client: claude-code") ||
		!strings.Contains(headers, "X-FI-Companion-Version: 0.1.0") ||
		!strings.Contains(headers, "X-FI-Workload: coding-agent") {
		t.Fatalf("Claude headers were not composed: %q", envValue(prepared.Env, "ANTHROPIC_CUSTOM_HEADERS"))
	}
	if envValue(prepared.Env, "X-Probe") != "" || envValue(prepared.Env, "X-Request-ID") != "" {
		t.Fatal("normal launch trace added a diagnostic/request header")
	}
	tracing.RemoveLaunchReceipt(prepared.ReceiptPath)
}

func TestPrepareLaunchClaudePreservesExistingTraceAndFailsOpenMalformed(t *testing.T) {
	clearRunTraceEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "claude-key")
	existing, _ := tracing.GenerateTraceID()
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Session-ID: "+existing)
	prepared, err := prepareLaunch(schema.ClientClaudeCode, os.Environ(), time.Now())
	if err != nil || prepared.Trace == nil || prepared.Trace.SessionID != existing {
		t.Fatalf("existing trace header was not preserved: %#v, %v", prepared.Trace, err)
	}
	tracing.RemoveLaunchReceipt(prepared.ReceiptPath)

	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "malformed")
	prepared, err = prepareLaunch(schema.ClientClaudeCode, os.Environ(), time.Now())
	if err == nil || prepared.Trace != nil || envValue(prepared.Env, tracing.TraceManagedEnv) != "" {
		t.Fatalf("malformed custom headers should fail open: %#v, %v", prepared, err)
	}
}

func TestPrepareLaunchDoesNotTraceNonFreeInferenceClaude(t *testing.T) {
	clearRunTraceEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "claude-key")
	prepared, err := prepareLaunch(schema.ClientClaudeCode, os.Environ(), time.Now())
	if err != nil || prepared.Trace != nil || envValue(prepared.Env, tracing.TraceSessionEnv) != "" {
		t.Fatalf("off-host Claude launch was traced: %#v, %v", prepared, err)
	}
}

func TestPrepareLaunchCodexAddsDocumentedMapping(t *testing.T) {
	clearRunTraceEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte("model_provider = \"freeinference\"\n\n[model_providers.freeinference]\nbase_url = \"https://freeinference.org/v1\"\nenv_key = \"CODEX_FI_KEY\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.BackupCodexTraceConfig(configPath); err != nil {
		t.Fatal(err)
	}
	if mapping, err := runtime.EnsureCodexTraceHeader(configPath, "freeinference"); err != nil || !mapping.Ready {
		t.Fatalf("Codex trace setup = %#v, %v", mapping, err)
	}
	t.Setenv("CODEX_FI_KEY", "codex-key")
	prepared, err := prepareLaunch(schema.ClientCodex, os.Environ(), time.Now())
	if err != nil || prepared.Trace == nil || envValue(prepared.Env, tracing.TraceSessionEnv) != prepared.Trace.SessionID {
		t.Fatalf("Codex launch trace = %#v, %v", prepared.Trace, err)
	}
	updated, _ := os.ReadFile(configPath)
	if !strings.Contains(string(updated), "env_http_headers") || !strings.Contains(string(updated), "FI_TRACE_SESSION_ID") ||
		!strings.Contains(string(updated), "X-FI-Client") ||
		!strings.Contains(string(updated), "FI_TRACE_COMPANION_VERSION") ||
		!strings.Contains(string(updated), "X-FI-Workload") {
		t.Fatalf("Codex mapping missing:\n%s", updated)
	}
	if envValue(prepared.Env, tracing.TraceCompanionVersionEnv) != "0.1.0" || envValue(prepared.Env, tracing.TraceWorkloadEnv) != tracing.WorkloadCodingAgent {
		t.Fatalf("static correlation environment missing: %q / %q", envValue(prepared.Env, tracing.TraceCompanionVersionEnv), envValue(prepared.Env, tracing.TraceWorkloadEnv))
	}
	tracing.RemoveLaunchReceipt(prepared.ReceiptPath)
}

func TestPrepareLaunchCodexVerifiesExplicitProfile(t *testing.T) {
	clearRunTraceEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	configPath := filepath.Join(home, "config.toml")
	base := "model_provider = \"openai\"\n\n[model_providers.openai]\nbase_url = \"https://api.openai.com/v1\"\nenv_key = \"OPENAI_API_KEY\"\n\n[model_providers.freeinference]\nbase_url = \"https://freeinference.org/v1\"\nenv_key = \"CODEX_FI_KEY\"\n"
	if err := os.WriteFile(configPath, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.BackupCodexTraceConfig(configPath); err != nil {
		t.Fatal(err)
	}
	if mapping, err := runtime.EnsureCodexTraceHeader(configPath, "freeinference"); err != nil || !mapping.Ready {
		t.Fatalf("Codex trace setup = %#v, %v", mapping, err)
	}
	if err := os.WriteFile(filepath.Join(home, "fi.config.toml"), []byte("model_provider = \"freeinference\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_FI_KEY", "codex-key")
	prepared, err := prepareLaunch(schema.ClientCodex, os.Environ(), time.Now(), "fi")
	if err != nil || prepared.Trace == nil || envValue(prepared.Env, tracing.TraceClientEnv) != schema.ClientCodex || envValue(prepared.Env, "CODEX_PROFILE") != "fi" {
		t.Fatalf("explicit Codex profile was not carried through: %#v, %v", prepared, err)
	}
	tracing.RemoveLaunchReceipt(prepared.ReceiptPath)
}

func TestReportIncludesTraceOnlyForValidActiveTrace(t *testing.T) {
	snap := &schema.Snapshot{
		Client:   schema.ClientInfo{Type: schema.ClientClaudeCode},
		Provider: schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true},
		Trace: &schema.TraceInfo{
			Enabled:   true,
			Verified:  true,
			SessionID: "fic-v1-aaaaaaaaaaaaaaaaaaaaaaaaaa",
			Source:    schema.TraceSourceCompanionGenerated,
			Provider:  schema.ProviderFreeInference,
			Client:    schema.ClientClaudeCode,
			Header:    schema.TraceHeaderSessionID,
			StartedAt: time.Now().UTC(),
		},
	}
	if report := buildReportTrace(snap); report == nil || report.TraceID != snap.Trace.SessionID {
		t.Fatalf("valid trace missing from report: %#v", report)
	}
	snap.Trace.SessionID = "raw-user-session-id"
	if report := buildReportTrace(snap); report != nil {
		t.Fatal("invalid/arbitrary trace ID appeared in report")
	}
}

func TestReportIncludesInheritedTraceWithoutResolvedSession(t *testing.T) {
	clearRunTraceEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "claude-key")
	t.Setenv(tracing.TraceManagedEnv, "1")
	t.Setenv(tracing.TraceSessionEnv, "fic-v1-aaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv(tracing.TraceSourceEnv, string(tracing.SourceCompanionGenerated))
	t.Setenv(tracing.TraceClientEnv, schema.ClientClaudeCode)

	var out, errOut strings.Builder
	if code := cmdReport(state.NewPathsWithDir(t.TempDir()), []string{"--format", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("report exit=%d stderr=%q", code, errOut.String())
	}
	if strings.Contains(out.String(), `"trace"`) || strings.Contains(out.String(), `fic-v1-aaaaaaaaaaaaaaaaaaaaaaaaaa`) {
		t.Fatalf("unverified inherited trace reached durable report: %s", out.String())
	}
}

func TestLaunchReceiptIsConsumedByMatchingSessionStart(t *testing.T) {
	clearRunTraceEnv(t)
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_BASE_URL", "https://freeinference.org/anthropic")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "claude-key")
	prepared, err := prepareLaunch(schema.ClientClaudeCode, os.Environ(), time.Now().UTC())
	if err != nil || prepared.Trace == nil {
		t.Fatalf("prepare = %#v, %v", prepared.Trace, err)
	}
	for _, key := range []string{tracing.TraceReceiptEnv, tracing.TraceSessionEnv, tracing.TraceManagedEnv, tracing.TraceSourceEnv, tracing.TraceClientEnv} {
		t.Setenv(key, envValue(prepared.Env, key))
	}
	activation := runtime.EvaluateForClient(runtime.ClientClaudeCode)
	trace := consumeTraceForHook(schema.ClientClaudeCode, activation)
	if trace == nil || trace.SessionID != prepared.Trace.SessionID {
		t.Fatalf("matching receipt was not consumed: %#v", trace)
	}
	if _, err := os.Stat(prepared.ReceiptPath); !os.IsNotExist(err) {
		t.Fatalf("receipt remains after SessionStart consumption: %v", err)
	}
}

func TestSessionEventsNeverContainTraceID(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	activation := runtime.Activation{
		Active: true, Client: runtime.ClientClaudeCode, RuntimeKind: runtime.RuntimeAnthropic,
		Origin: "https://freeinference.org", EndpointURL: "https://freeinference.org/anthropic",
	}
	trace := &schema.TraceInfo{
		Enabled: true, Verified: true, SessionID: "fic-v1-aaaaaaaaaaaaaaaaaaaaaaaaaa", Source: schema.TraceSourceCompanionGenerated,
		StartedAt: time.Now().UTC(), Provider: schema.ProviderFreeInference, Client: schema.ClientClaudeCode, Header: schema.TraceHeaderSessionID,
	}
	if err := adapters.NewClaudeAdapter(paths).HandleSessionStartWithTrace(&schema.ClaudeHookInput{SessionID: "trace-session", Model: "glm-5.1"}, activation, trace); err != nil {
		t.Fatal(err)
	}
	events, err := os.ReadFile(paths.SessionEvents(schema.ClientClaudeCode, "trace-session"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(events), trace.SessionID) {
		t.Fatalf("trace ID appeared in routine event line: %s", events)
	}
}
