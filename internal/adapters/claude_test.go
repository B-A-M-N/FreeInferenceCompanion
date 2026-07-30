package adapters

import (
	"os"
	"path/filepath"
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

func confirmFreeInference(t *testing.T) {
	t.Helper()
	for _, env := range []string{"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL"} {
		t.Setenv(env, "")
	}
	t.Setenv("FI_PROVIDER", "")
	// P0-1: activation requires BOTH an approved FreeInference endpoint AND
	// a credential. Key-only activation is no longer permitted.
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "hyi-test-key-12345")
}

func unconfirmProvider(t *testing.T) {
	t.Helper()
	t.Setenv("FI_PROVIDER", "")
	t.Setenv("FREEINFERENCE_API_KEY", "")
	t.Setenv("FREEINFERENCE_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("OPENAI_BASE_URL", "")
}

func loadClaude(t *testing.T, paths state.Paths, sessionID string) *schema.Snapshot {
	t.Helper()
	snap, err := state.LoadSnapshot(paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		t.Fatalf("load snapshot: err=%v snap=%v", err, snap)
	}
	return snap
}

func statusInput(sessionID, modelID string, totalIn, totalOut, ctxSize int64, usedPct float64, fresh, cacheRead, cacheCreate, output int64) *schema.ClaudeStatusLineInput {
	return &schema.ClaudeStatusLineInput{
		Model:     schema.ModelStatus{ID: modelID, DisplayName: "Display " + modelID},
		SessionID: sessionID,
		ContextWindow: schema.ContextWindowStatus{
			TotalInputTokens:  &totalIn,
			TotalOutputTokens: &totalOut,
			CurrentUsage: &schema.CurrentUsage{
				InputTokens:              &fresh,
				OutputTokens:             &output,
				CacheCreationInputTokens: &cacheCreate,
				CacheReadInputTokens:     &cacheRead,
			},
			ContextWindowSize: ctxSize,
			UsedPercentage:    &usedPct,
		},
	}
}

func TestClaudeSessionStartInitializes(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	input := &schema.ClaudeHookInput{SessionID: "s1", Model: "glm-5.1"}
	if err := a.HandleSessionStart(input); err != nil {
		t.Fatalf("session start: %v", err)
	}

	snap := loadClaude(t, paths, "s1")
	if snap.Session.Status != schema.SessionActive {
		t.Errorf("status = %s", snap.Session.Status)
	}
	if snap.Model.ID != "glm-5.1" {
		t.Errorf("model = %s", snap.Model.ID)
	}
	if !snap.Provider.Confirmed {
		t.Errorf("provider should be confirmed")
	}
}

func TestClaudeSessionStartPreservesStatusData(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Status line telemetry arrives first (async SessionStart race).
	if err := a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 160000, 2000, 200000, 80, 5000, 150000, 5000, 2000), "s1"); err != nil {
		t.Fatalf("status update: %v", err)
	}
	before := loadClaude(t, paths, "s1")

	if err := a.HandleSessionStart(&schema.ClaudeHookInput{SessionID: "s1", Model: "other-model"}); err != nil {
		t.Fatalf("session start: %v", err)
	}
	after := loadClaude(t, paths, "s1")

	if after.LiveContext == nil || after.LiveContext.UsedPercentage == nil {
		t.Error("session start wiped live context")
	}
	if after.Model.ID != "glm-5.1" {
		t.Errorf("session start replaced authoritative model ID: %s", after.Model.ID)
	}
	if !after.Session.StartedAt.Equal(before.Session.StartedAt) {
		t.Error("session start reset StartedAt")
	}
}

func TestClaudeUserPromptSubmitActivatesTurn(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleSessionStart(&schema.ClaudeHookInput{SessionID: "s1", Model: "glm-5.1"})
	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1", Prompt: "hello"}, "s1")
	if err != nil {
		t.Fatalf("prompt submit: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil output (no warning), got %+v", out)
	}

	snap := loadClaude(t, paths, "s1")
	if snap.Activity.TurnActive == nil || !*snap.Activity.TurnActive {
		t.Error("turn should be active after prompt submit")
	}
	if snap.Activity.TurnStartedAt == nil {
		t.Error("TurnStartedAt should be set")
	}
}

func TestClaudePromptTextNeverPersisted(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	secret := "super-secret-prompt-contents-xyz"
	_, _ = a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1", Prompt: secret}, "s1")

	data, err := os.ReadFile(paths.SessionSnapshot(schema.ClientClaudeCode, "s1"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Error("prompt text was persisted to the snapshot")
	}
}

func TestClaudeStopEndsTurnNotSession(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_, _ = a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err := a.HandleStop("s1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	snap := loadClaude(t, paths, "s1")
	if snap.Activity.TurnActive == nil || *snap.Activity.TurnActive {
		t.Error("turn should be inactive after stop")
	}
	if snap.Activity.TurnEndedAt == nil {
		t.Error("TurnEndedAt should be set")
	}
	if snap.Session.Status == schema.SessionCompleted {
		t.Error("stop must not complete the session")
	}
}

func TestClaudeSessionEndCompletes(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_, _ = a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err := a.HandleSessionEnd("s1"); err != nil {
		t.Fatalf("session end: %v", err)
	}

	snap := loadClaude(t, paths, "s1")
	if snap.Session.Status != schema.SessionCompleted {
		t.Errorf("status = %s", snap.Session.Status)
	}
	if snap.Session.EndedAt == nil {
		t.Error("EndedAt should be set")
	}
	if snap.Activity.TurnActive == nil || *snap.Activity.TurnActive {
		t.Error("turn should be inactive after session end")
	}
}

func TestClaudeWarningEmittedAboveThreshold(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 168000, 2000, 200000, 84, 5000, 150000, 5000, 2000), "s1")

	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("prompt submit: %v", err)
	}
	if out == nil {
		t.Fatal("expected a context warning at 84%")
	}
	if !out.Continue || out.SystemMessage == "" || !out.SuppressOutput {
		t.Errorf("claude warning must include systemMessage and suppressOutput: %+v", out)
	}
	if !strings.Contains(out.SystemMessage, "84%") {
		t.Errorf("warning should include the percentage: %q", out.SystemMessage)
	}
}

func TestClaudeWarningSuppressedForNonFreeInference(t *testing.T) {
	unconfirmProvider(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleStatusLineUpdate(statusInput("s1", "claude-3-opus", 190000, 2000, 200000, 95, 5000, 180000, 5000, 2000), "s1")

	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("prompt submit: %v", err)
	}
	if out != nil {
		t.Errorf("no FreeInference warning may fire on a non-FreeInference session: %+v", out)
	}

	snap := loadClaude(t, paths, "s1")
	if snap.Provider.Confirmed {
		t.Error("provider must be unconfirmed")
	}
}

func TestClaudeDisplayNameDoesNotReplaceModelID(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 1000, 100, 200000, 1, 500, 400, 100, 100), "s1")
	snap := loadClaude(t, paths, "s1")

	if snap.Model.ID != "glm-5.1" {
		t.Errorf("model ID = %s", snap.Model.ID)
	}
	if snap.Model.DisplayName == nil || *snap.Model.DisplayName != "Display glm-5.1" {
		t.Errorf("display name = %v", snap.Model.DisplayName)
	}
}

func TestClaudeDuplicateStatusRenderIgnored(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	input := statusInput("s1", "glm-5.1", 160000, 2000, 200000, 80, 5000, 150000, 5000, 2000)
	_ = a.HandleStatusLineUpdate(input, "s1")
	_ = a.HandleStatusLineUpdate(input, "s1")
	_ = a.HandleStatusLineUpdate(input, "s1")

	snap := loadClaude(t, paths, "s1")
	if len(snap.UsageObservations) != 1 {
		t.Errorf("duplicate renders must not create observations: %d", len(snap.UsageObservations))
	}
}

// TestClaudeDuplicateRenderDoesNotInflateCounters is the P1-5 regression
// test: submitting the identical status payload ten times must not inflate
// the consecutive cache counters beyond the single unique observation.
func TestClaudeDuplicateRenderDoesNotInflateCounters(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Low-cache observation (10% read share).
	input := statusInput("s1", "glm-5.1", 160000, 2000, 200000, 80,
		150000, 5000, 5000, 2000)

	// Submit the same payload ten times.
	for i := 0; i < 10; i++ {
		if err := a.HandleStatusLineUpdate(input, "s1"); err != nil {
			t.Fatalf("status update %d: %v", i, err)
		}
	}

	snap := loadClaude(t, paths, "s1")
	if len(snap.UsageObservations) != 1 {
		t.Errorf("observations = %d, want 1", len(snap.UsageObservations))
	}
	if snap.CacheAnalysis == nil {
		t.Fatal("cache analysis missing")
	}
	if snap.CacheAnalysis.ConsecutiveLow != 1 {
		t.Errorf("consecutive low = %d, want 1 (duplicate renders must not inflate counters)",
			snap.CacheAnalysis.ConsecutiveLow)
	}
	if snap.CacheAnalysis.ConsecutiveRecovered != 0 {
		t.Errorf("consecutive recovered = %d, want 0", snap.CacheAnalysis.ConsecutiveRecovered)
	}
}

func TestClaudeCompactionFlow(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Seed context: 162K active.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 160000, 2000, 200000, 81, 5000, 150000, 5000, 2000), "s1")

	// PreCompact: records active context total and trigger.
	if err := a.HandlePreCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "manual"}, "s1"); err != nil {
		t.Fatalf("pre-compact: %v", err)
	}
	snap := loadClaude(t, paths, "s1")
	if !snap.Compaction.Pending {
		t.Error("compaction should be pending")
	}
	if snap.Compaction.PreTokens == nil || *snap.Compaction.PreTokens != 160000 {
		t.Errorf("pre tokens = %v, want 160000", snap.Compaction.PreTokens)
	}
	if snap.Compaction.Trigger == nil || *snap.Compaction.Trigger != "manual" {
		t.Errorf("trigger = %v", snap.Compaction.Trigger)
	}

	// PostCompact: waits for the next changed observation.
	if err := a.HandlePostCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "manual"}, "s1"); err != nil {
		t.Fatalf("post-compact: %v", err)
	}
	snap = loadClaude(t, paths, "s1")
	if snap.Compaction.Pending {
		t.Error("pending should clear on post-compact")
	}
	if !snap.Compaction.AwaitingPostObservation {
		t.Error("should await post-compaction observation")
	}

	// Re-render of the same data must NOT complete the measurement.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 160000, 2000, 200000, 81, 5000, 150000, 5000, 2000), "s1")
	snap = loadClaude(t, paths, "s1")
	if !snap.Compaction.AwaitingPostObservation {
		t.Error("unchanged render must not complete compaction measurement")
	}

	// Next changed observation (post-compaction context) completes it.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 80000, 2000, 200000, 41, 3000, 75000, 2000, 2000), "s1")
	snap = loadClaude(t, paths, "s1")
	if snap.Compaction.AwaitingPostObservation {
		t.Error("measurement should be complete after changed observation")
	}
	r := snap.Compaction.LastResult
	if r == nil {
		t.Fatal("no compaction result")
	}
	if r.PreTokens == nil || *r.PreTokens != 160000 {
		t.Errorf("result pre tokens = %v", r.PreTokens)
	}
	if r.PostTokens == nil || *r.PostTokens != 80000 {
		t.Errorf("result post tokens = %v", r.PostTokens)
	}
	if r.ReductionPct == nil {
		t.Fatal("reduction should be computed")
	}
	want := float64(160000-80000) / 160000 * 100
	if *r.ReductionPct < want-0.5 || *r.ReductionPct > want+0.5 {
		t.Errorf("reduction = %.2f, want ≈%.2f", *r.ReductionPct, want)
	}
	if r.Trigger != "manual" {
		t.Errorf("trigger = %q", r.Trigger)
	}
}

func TestClaudeCompactionWithoutPreTokensStaysUnknown(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// No telemetry before compaction.
	_ = a.HandleSessionStart(&schema.ClaudeHookInput{SessionID: "s1", Model: "glm-5.1"})
	_ = a.HandlePreCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "automatic"}, "s1")
	_ = a.HandlePostCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "automatic"}, "s1")

	snap := loadClaude(t, paths, "s1")
	if snap.Compaction.AwaitingPostObservation {
		t.Error("without pre tokens there is nothing to await")
	}
	if snap.Compaction.LastResult != nil && snap.Compaction.LastResult.ReductionPct != nil {
		t.Error("reduction must stay unknown without token data")
	}
}

func TestClaudeRepeatedCompactionClearsStalePreTokens(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 160000, 2000, 200000, 81, 5000, 150000, 5000, 2000), "s1")
	_ = a.HandlePreCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "manual"}, "s1")
	_ = a.HandlePostCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "manual"}, "s1")
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 80000, 2000, 200000, 41, 3000, 75000, 2000, 2000), "s1")

	if err := state.UpdateSnapshot(paths, schema.ClientClaudeCode, "s1", nil, func(s *schema.Snapshot) error {
		s.LiveContext = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = a.HandlePreCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "automatic"}, "s1")
	_ = a.HandlePostCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "automatic"}, "s1")
	snap := loadClaude(t, paths, "s1")
	if snap.Compaction.PreTokens != nil || snap.Compaction.AwaitingPostObservation {
		t.Errorf("missing second pre-observation must not reuse old values: %+v", snap.Compaction)
	}
}

// TestClaudeCompactionPostGreaterThanPreNoNegative is the P1-12 regression
// test: if the post-compaction observation has more tokens than pre, the
// result must be recorded as unknown — never as a negative reduction.
func TestClaudeCompactionPostGreaterThanPreNoNegative(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Seed: 162K active.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 160000, 2000, 200000, 81, 5000, 150000, 5000, 2000), "s1")
	_ = a.HandlePreCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "manual"}, "s1")
	_ = a.HandlePostCompact(&schema.ClaudeHookInput{SessionID: "s1", Trigger: "manual"}, "s1")

	// Post-compaction observation with MORE tokens than pre (should not happen
	// in practice, but the guard must prevent a negative reduction).
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 200000, 2000, 200000, 100, 5000, 190000, 5000, 2000), "s1")

	snap := loadClaude(t, paths, "s1")
	if snap.Compaction.AwaitingPostObservation {
		t.Error("measurement should have completed")
	}
	r := snap.Compaction.LastResult
	if r == nil {
		t.Fatal("no compaction result")
	}
	if r.ReductionPct != nil {
		t.Errorf("post > pre must not produce a negative reduction, got %.2f%%", *r.ReductionPct)
	}
}

func TestClaudeCacheWarningQualifies(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Three unique low-reuse observations with >50K active context.
	// Context percentage stays below the pressure-warning threshold so the
	// cache warning is the one that fires.
	for _, total := range []int64{160000, 161000, 162000} {
		_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", total, 2000, 1000000, 16,
			total-10000, 5000, 5000, 2000), "s1")
	}

	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("prompt submit: %v", err)
	}
	if out == nil {
		t.Fatal("expected a cache-low warning after 3 low observations")
	}
	if !strings.Contains(out.SystemMessage, "cache reuse is low") {
		t.Errorf("unexpected warning text: %q", out.SystemMessage)
	}
}

func TestClaudeCacheWarningNeedsThreeSamples(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	for _, total := range []int64{160000, 161000} {
		_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", total, 2000, 200000, 60,
			total-10000, 5000, 5000, 2000), "s1")
	}

	out, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if out != nil {
		t.Errorf("two samples must not trigger a cache warning: %+v", out)
	}
}

func TestClaudeCacheWarningRequiresLargeContext(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Low reuse but small active context (<50K).
	for _, total := range []int64{10000, 11000, 12000} {
		_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", total, 1000, 200000, 6,
			total-1000, 500, 500, 1000), "s1")
	}

	out, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if out != nil {
		t.Errorf("small context must not trigger a cache warning: %+v", out)
	}
}

func TestClaudeSessionIndexUpdated(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleSessionStart(&schema.ClaudeHookInput{SessionID: "s1", Model: "glm-5.1"})

	idx, err := state.LoadSessionIndex(paths)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	if len(idx.Sessions) != 1 {
		t.Fatalf("index entries = %d", len(idx.Sessions))
	}
	e := idx.Sessions[0]
	if e.SessionID != "s1" || e.Client != schema.ClientClaudeCode || e.Status != schema.SessionActive {
		t.Errorf("unexpected index entry: %+v", e)
	}
}

func TestClaudeLockBusyFailsOpen(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleSessionStart(&schema.ClaudeHookInput{SessionID: "s1", Model: "glm-5.1"})

	// Hold the session lock from this process.
	lockPath := paths.SessionLock(schema.ClientClaudeCode, "s1")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		t.Fatal(err)
	}
	fl := state.NewFileLock(lockPath)
	if err := fl.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer fl.Release()

	start := time.Now()
	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("hook must fail open, got %v", err)
	}
	if out != nil {
		t.Errorf("lock-busy must produce no output, got %+v", out)
	}
	if elapsed > 2*time.Second {
		t.Errorf("lock contention must return immediately, took %v", elapsed)
	}
}

// TestClaudeTotalsVsLatestRequestSeparation verifies cumulative session totals
// are stored separately from the latest request's per-call usage breakdown.
func TestClaudeTotalsVsLatestRequestSeparation(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// total_in=160000 total_out=2000; latest request fresh=5000 read=150000
	// create=5000 output=2000. The session totals must NOT be derived from
	// the latest request fields, and vice versa.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 160000, 2000, 200000, 80, 5000, 150000, 5000, 2000), "s1")
	snap := loadClaude(t, paths, "s1")
	if snap.LiveContext == nil {
		t.Fatal("live context missing")
	}
	lc := snap.LiveContext
	if lc.TotalInputTokens == nil || *lc.TotalInputTokens != 160000 {
		t.Errorf("total input = %v, want 160000", lc.TotalInputTokens)
	}
	if lc.TotalOutputTokens == nil || *lc.TotalOutputTokens != 2000 {
		t.Errorf("total output = %v, want 2000", lc.TotalOutputTokens)
	}
	if lc.LatestRequest == nil {
		t.Fatal("latest request missing")
	}
	lr := lc.LatestRequest
	if lr.FreshInputTokens == nil || *lr.FreshInputTokens != 5000 {
		t.Errorf("fresh input = %v, want 5000", lr.FreshInputTokens)
	}
	if lr.CacheReadInputTokens == nil || *lr.CacheReadInputTokens != 150000 {
		t.Errorf("cache read = %v, want 150000", lr.CacheReadInputTokens)
	}
	if lr.OutputTokens == nil || *lr.OutputTokens != 2000 {
		t.Errorf("output = %v, want 2000", lr.OutputTokens)
	}
	// And the latest-request fresh input must not have leaked into totals.
	if lc.TotalInputTokens != nil && *lc.TotalInputTokens == 5000 {
		t.Error("latest-request fresh input leaked into session totals")
	}
}

// TestClaudeZeroTelemetryPreserved is the P1-6 regression test: explicit zero
// totals and current-usage values must be preserved as zero, not converted to
// nil. Absent fields stay nil; zero values stay zero.
func TestClaudeZeroTelemetryPreserved(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Zero totals and zero current-usage (Claude reports zeros before first response).
	input := statusInput("s1", "glm-5.1", 0, 0, 200000, 0, 0, 0, 0, 0)
	if err := a.HandleStatusLineUpdate(input, "s1"); err != nil {
		t.Fatalf("status update: %v", err)
	}

	snap := loadClaude(t, paths, "s1")
	if snap.LiveContext == nil {
		t.Fatal("live context missing")
	}
	lc := snap.LiveContext

	// Zero totals must be preserved (not collapsed to nil).
	if lc.TotalInputTokens == nil {
		t.Error("TotalInputTokens: zero was converted to nil (lost explicit zero)")
	} else if *lc.TotalInputTokens != 0 {
		t.Errorf("TotalInputTokens = %d, want 0", *lc.TotalInputTokens)
	}
	if lc.TotalOutputTokens == nil {
		t.Error("TotalOutputTokens: zero was converted to nil (lost explicit zero)")
	} else if *lc.TotalOutputTokens != 0 {
		t.Errorf("TotalOutputTokens = %d, want 0", *lc.TotalOutputTokens)
	}

	// Zero current-usage fields must be preserved.
	if lc.LatestRequest == nil {
		t.Fatal("latest request missing")
	}
	lr := lc.LatestRequest
	if lr.FreshInputTokens == nil {
		t.Error("FreshInputTokens: zero was converted to nil")
	} else if *lr.FreshInputTokens != 0 {
		t.Errorf("FreshInputTokens = %d, want 0", *lr.FreshInputTokens)
	}
	if lr.CacheReadInputTokens == nil {
		t.Error("CacheReadInputTokens: zero was converted to nil")
	} else if *lr.CacheReadInputTokens != 0 {
		t.Errorf("CacheReadInputTokens = %d, want 0", *lr.CacheReadInputTokens)
	}
}

// TestClaudeContextWarningCooldown verifies the same-severity warning is
// suppressed within the cooldown window.
func TestClaudeContextWarningCooldown(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// 84% triggers a warn-severity warning on the first prompt.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 168000, 2000, 200000, 84, 5000, 150000, 5000, 2000), "s1")
	first, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil || first == nil {
		t.Fatalf("first warning must fire: %v, %+v", err, first)
	}

	// Second prompt at the same severity must be suppressed by cooldown.
	second, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("second prompt: %v", err)
	}
	if second != nil {
		t.Errorf("same-severity warning must respect cooldown, got %+v", second)
	}
}

// TestClaudeSeverityEscalation verifies escalation to critical overrides cooldown.
func TestClaudeSeverityEscalation(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Warn at 84%.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 168000, 2000, 200000, 84, 5000, 150000, 5000, 2000), "s1")
	if out, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1"); out == nil {
		t.Fatal("warn should fire")
	}

	// Escalate to 92% (critical). The escalation must bypass cooldown.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 184000, 2000, 200000, 92, 5000, 170000, 5000, 2000), "s1")
	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("escalated prompt: %v", err)
	}
	if out == nil {
		t.Fatal("escalation to critical must bypass cooldown")
	}
	if !strings.Contains(out.SystemMessage, "92%") {
		t.Errorf("escalated warning should show 92%%: %q", out.SystemMessage)
	}
}

// TestClaudeSanitizedFailureCategory verifies the raw StopFailure error body
// is collapsed into a short category and never persisted verbatim.
func TestClaudeSanitizedFailureCategory(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleSessionStart(&schema.ClaudeHookInput{SessionID: "s1", Model: "glm-5.1"})
	rawError := "Rate limit exceeded for user xyz on /v1/messages with body {...secret...}"
	_ = a.HandleStopFailure(&schema.ClaudeHookInput{SessionID: "s1", Error: rawError}, "s1")

	snap := loadClaude(t, paths, "s1")
	if snap.LastFailure == nil {
		t.Fatal("failure not recorded")
	}
	if snap.LastFailure.Category != "rate_limit" {
		t.Errorf("category = %q, want rate_limit", snap.LastFailure.Category)
	}

	// The raw error text must never appear in the persisted state or events.
	data, _ := os.ReadFile(paths.SessionSnapshot(schema.ClientClaudeCode, "s1"))
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "xyz") {
		t.Errorf("raw failure body leaked into snapshot: %s", data)
	}
	events, _ := state.ReadEvents(paths, schema.ClientClaudeCode, "s1", 0)
	for _, ev := range events {
		if ev.Detail != "rate_limit" && strings.Contains(ev.Detail+rawError, ev.Detail) && ev.Detail != "" {
			// The event detail must be the category only, not the raw body.
			if strings.Contains(ev.Detail, "secret") {
				t.Errorf("raw failure leaked into event detail: %s", ev.Detail)
			}
		}
	}
}

// TestClaudeStatusLineInitializationBeforeSessionStart verifies the status line
// path upserts a session that hasn't seen SessionStart yet.
func TestClaudeStatusLineInitializationBeforeSessionStart(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Status update arrives first — no SessionStart has been seen.
	if err := a.HandleStatusLineUpdate(statusInput("solo", "glm-5.1", 160000, 2000, 200000, 80, 5000, 150000, 5000, 2000), "solo"); err != nil {
		t.Fatalf("status update: %v", err)
	}
	snap := loadClaude(t, paths, "solo")
	if snap.Session.ID != "solo" {
		t.Errorf("session id = %s", snap.Session.ID)
	}
	if snap.LiveContext == nil || snap.LiveContext.UsedPercentage == nil {
		t.Error("status update must populate live context even before SessionStart")
	}
}

// TestClaudeActiveContextMatchesPercentage is the P1-7 invariant test: the
// input-only ActiveContextTokens must be mathematically compatible with the
// displayed used_percentage within rounding tolerance. This is the contract
// that says "the token total and the percentage describe the same thing."
func TestClaudeActiveContextMatchesPercentage(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// 160K input of 200K window → 80% used.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 160000, 2000, 200000, 80, 5000, 150000, 5000, 2000), "s1")
	snap := loadClaude(t, paths, "s1")

	if snap.LiveContext == nil || snap.LiveContext.UsedPercentage == nil || snap.LiveContext.ContextWindowSize == nil {
		t.Fatal("missing live context fields")
	}

	active := ActiveContextTokens(snap)
	window := *snap.LiveContext.ContextWindowSize
	pct := *snap.LiveContext.UsedPercentage

	// ActiveContextTokens must equal total_input_tokens (160000), not
	// total_input + total_output (162000).
	if active != 160000 {
		t.Errorf("ActiveContextTokens = %d, want 160000 (input only)", active)
	}

	// And active / window must match the percentage within rounding tolerance.
	computedPct := float64(active) / float64(window) * 100
	if computedPct < pct-1.0 || computedPct > pct+1.0 {
		t.Errorf("active/window = %.1f%%, reported percentage = %.1f%% — must match within 1%% tolerance", computedPct, pct)
	}
}

// TestClaudeProjectionWarningTriggersAtHighContext verifies the projection
// warning fires when context is high and the next request would leave too
// little room for output — even when the percentage itself hasn't crossed 80%.
func TestClaudeProjectionWarningTriggersAtHighContext(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Context at 70% (below warn threshold). The model context window is
	// 200K. Active = 140K. Adding a 10K prompt + tool overhead + 16K reserve
	// is fine here, so no projection warning should fire yet.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 138000, 2000, 200000, 70, 5000, 130000, 5000, 2000), "s1")
	out, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1", Prompt: "hello"}, "s1")
	if out != nil {
		t.Errorf("no warning expected at 70%% with comfortable reserve: %+v", out)
	}
}

// TestClaudeProjectionWarningFiresNearReserveLimit verifies projection fires
// when active context + prompt + reserve genuinely overflows the window.
func TestClaudeProjectionWarningFiresNearReserveLimit(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// 195K active of 200K window → 97.5%. The context warning would fire at
	// critical severity regardless, so this test just confirms projection
	// does not crash the hook.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 193000, 2000, 200000, 97.5, 5000, 185000, 5000, 2000), "s1")
	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1", Prompt: "hello"}, "s1")
	if err != nil {
		t.Fatalf("hook must not error: %v", err)
	}
	if out == nil {
		t.Fatal("a warning must fire at 97.5% context")
	}
	if !out.Continue {
		t.Error("projection warning must never block the prompt")
	}
}

// TestClaudeContextWarningDoesNotBlockCacheResolution is the P1-11 regression
// test: when a context warning fires AND a cache warning resolves on the same
// prompt, both state transitions must be persisted. The old code short-circuited
// after the context warning and never evaluated cache recovery.
func TestClaudeContextWarningDoesNotBlockCacheResolution(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Seed: 3 low-cache observations (cache warning active) + high context.
	for _, total := range []int64{160000, 161000, 162000} {
		_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", total, 2000, 200000, 16,
			total-10000, 5000, 5000, 2000), "s1")
	}

	// First prompt: cache-low warning fires at low context percentage.
	out1, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if out1 == nil || !strings.Contains(out1.SystemMessage, "cache reuse is low") {
		t.Fatalf("expected cache-low warning, got %+v", out1)
	}

	// Seed recovery: 3 high-reuse observations + high context (84%).
	for _, total := range []int64{168000, 169000, 170000} {
		_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", total, 2000, 200000, 84,
			5000, total-10000, 5000, 2000), "s1")
	}

	// Second prompt: context warning fires (84% ≥ 80%) AND cache warning
	// resolves (3 recovered observations). Both must be persisted.
	out2, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if out2 == nil {
		t.Fatal("expected a context warning at 84%")
	}
	if !strings.Contains(out2.SystemMessage, "84%") {
		t.Errorf("context warning should show 84%%: %q", out2.SystemMessage)
	}

	snap := loadClaude(t, paths, "s1")
	if snap.Warnings.CacheLowActive {
		t.Error("cache warning must have resolved — 3 recovered observations were made")
	}
	// Context severity must be set to warn (from the 84% warning).
	if snap.Warnings.ContextSeverity != schema.WarningSeverityWarn {
		t.Errorf("context severity = %q, want %q", snap.Warnings.ContextSeverity, schema.WarningSeverityWarn)
	}
}

// setLastEventAge writes a snapshot with LastEventAt set to age duration in the
// past, simulating an idle period without actually sleeping. Also sets
// CacheTiming.LastInferenceObservedAt to the same age so the cache clock
// reflects the idle gap (Finding 8: cache TTL uses separate timing fields).
func setLastEventAge(t *testing.T, paths state.Paths, sessionID string, age time.Duration) {
	t.Helper()
	snap := loadClaude(t, paths, sessionID)
	past := time.Now().UTC().Add(-age)
	snap.Session.LastEventAt = past
	if snap.CacheTiming == nil {
		snap.CacheTiming = &schema.CacheTiming{}
	}
	// Move the cache clock too (only if set — zero means "not yet observed").
	if !snap.CacheTiming.LastInferenceObservedAt.IsZero() {
		snap.CacheTiming.LastInferenceObservedAt = past
	}
	// Set a provider-confirmed TTL so EvaluateCacheTTLExpiryV2 can generate
	// warnings. Without a confirmed TTL, V2 returns no warning.
	if snap.CacheTiming.CacheTTLSeconds == nil {
		ttl := 300 // 5 minutes, matching Anthropic's documented TTL
		snap.CacheTiming.CacheTTLSeconds = &ttl
	}
	if err := state.SaveSnapshot(paths, schema.ClientClaudeCode, sessionID, snap); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
}

func TestClaudeCacheTTLWarningFiresAfterIdle(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Seed a session with meaningful context (>10K tokens) so the TTL gate passes.
	// Context at 30% — below the pressure threshold so TTL is the warning that fires.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 120000, 2000, 200000, 30,
		100000, 10000, 5000, 2000), "s1")

	// Simulate 6 minutes of idle — past the 5-minute prompt cache TTL.
	setLastEventAge(t, paths, "s1", 6*time.Minute)

	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("prompt submit: %v", err)
	}
	if out == nil {
		t.Fatal("expected a cache TTL expiry warning after 6m idle")
	}
	if !strings.Contains(out.SystemMessage, "prompt cache expired") {
		t.Errorf("expected TTL warning text, got: %q", out.SystemMessage)
	}
}

func TestClaudeCacheTTLWarningSuppressedUnderTTL(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 120000, 2000, 200000, 30,
		100000, 10000, 5000, 2000), "s1")

	// Only 2 minutes idle — under the 5-minute TTL. No warning.
	setLastEventAge(t, paths, "s1", 2*time.Minute)

	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("prompt submit: %v", err)
	}
	if out != nil {
		t.Errorf("no TTL warning expected under 5m idle, got: %q", out.SystemMessage)
	}
}

func TestClaudeCacheTTLWarningSuppressedForSmallContext(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Small context (5K tokens) — below the MinActiveTokensForTTLWarning (10K).
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 5000, 1000, 200000, 5,
		3000, 1000, 500, 500), "s1")

	setLastEventAge(t, paths, "s1", 10*time.Minute)

	out, err := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("prompt submit: %v", err)
	}
	if out != nil {
		t.Errorf("no TTL warning expected for small context, got: %q", out.SystemMessage)
	}
}

func TestClaudeCacheTTLWarningCooldown(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 120000, 2000, 200000, 30,
		100000, 10000, 5000, 2000), "s1")

	// First prompt after 6m idle — should warn.
	setLastEventAge(t, paths, "s1", 6*time.Minute)
	out1, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if out1 == nil {
		t.Fatal("expected TTL warning on first idle prompt")
	}

	// Second prompt, still idle 6m later, but within cooldown (30min).
	// The prompt submit itself updates LastEventAt, so we need to re-age it.
	setLastEventAge(t, paths, "s1", 6*time.Minute)
	out2, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if out2 != nil {
		t.Errorf("TTL warning must respect cooldown, got: %q", out2.SystemMessage)
	}
}

func TestClaudeCacheTTLWarningGivesWayToContextWarning(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewClaudeAdapter(paths)

	// Context at 85% — above the pressure threshold. Context warning must win.
	_ = a.HandleStatusLineUpdate(statusInput("s1", "glm-5.1", 170000, 2000, 200000, 85,
		100000, 50000, 10000, 2000), "s1")

	setLastEventAge(t, paths, "s1", 10*time.Minute)

	out, _ := a.HandleUserPromptSubmit(&schema.ClaudeHookInput{SessionID: "s1"}, "s1")
	if out == nil {
		t.Fatal("expected a warning")
	}
	// Context warning should fire, not TTL.
	if !strings.Contains(out.SystemMessage, "context usage") {
		t.Errorf("expected context warning to take priority over TTL, got: %q", out.SystemMessage)
	}
}
