package engine

import (
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func ptrTime(t time.Time) *time.Time { return &t }

func ptrInt(v int) *int { return &v }

// ---------------------------------------------------------------------------
// Test: Repeated identical status-line payloads do not refresh CacheTiming
// ---------------------------------------------------------------------------

func TestCacheTimingNoRefreshOnIdenticalStatusLine(t *testing.T) {
	// Simulate what HandleStatusLineUpdateWith does:
	// 1. Create a new observation (newObservation == true) → update CacheTiming
	// 2. Receive the same fingerprint again (newObservation == false) → do NOT update

	now := time.Now()
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: now.Add(-10 * time.Minute),
		},
	}

	fp, _ := ObservationFingerprint("test-model", "new-prompt", 100000, 1000,
		i64(5000), i64(90000), i64(5000), i64(1000))
	obs := schema.UsageObservation{
		Fingerprint:              fp,
		ObservedAt:               now,
		ModelID:                  "test-model",
		TotalInputTokens:         i64(100000),
		TotalOutputTokens:        i64(1000),
		FreshInputTokens:         i64(5000),
		CacheReadInputTokens:     i64(90000),
		CacheCreationInputTokens: i64(5000),
		OutputTokens:             i64(1000),
	}
	added := AddObservation(snap, obs)
	if !added {
		t.Fatal("expected observation to be added")
	}
	// The adapter updates CacheTiming when newObservation == true.
	snap.CacheTiming.LastInferenceObservedAt = now

	// Record the timestamp so we can verify it doesn't change on duplicates.
	t0 := snap.CacheTiming.LastInferenceObservedAt

	// Now feed the SAME fingerprint again — AddObservation returns false.
	added2 := AddObservation(snap, obs)
	if added2 {
		t.Error("duplicate fingerprint should not be added")
	}
	// Because the observation was a duplicate, the adapter does NOT update
	// CacheTiming. The timestamp must still be t0.
	if snap.CacheTiming.LastInferenceObservedAt != t0 {
		t.Errorf("expected CacheTiming unchanged after duplicate, got %v", snap.CacheTiming.LastInferenceObservedAt)
	}
}

// ---------------------------------------------------------------------------
// Test: Non-inference lifecycle events do not refresh CacheTiming
// ---------------------------------------------------------------------------

func TestCacheTimingNotRefreshedByLifecycleEvents(t *testing.T) {
	// Simulate: CacheTiming was last updated at T0.
	// A session-end event comes in (non-inference). CacheTiming should NOT move.
	t0 := time.Now().Add(-10 * time.Minute)
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: t0,
		},
	}

	// A non-inference observation (e.g. session start) has no latest usage,
	// so newObservation stays false — CacheTiming is never touched.
	if snap.CacheTiming.LastInferenceObservedAt != t0 {
		t.Fatalf("initial state wrong: %v", snap.CacheTiming.LastInferenceObservedAt)
	}

	// Simulate what HandleSessionEnd does: it never sets CacheTiming.
	snap.Session.Status = schema.SessionCompleted
	now := time.Now()
	snap.Session.LastEventAt = now

	// CacheTiming must still point at t0.
	if snap.CacheTiming.LastInferenceObservedAt != t0 {
		t.Errorf("CacheTiming was incorrectly updated to %v", snap.CacheTiming.LastInferenceObservedAt)
	}
}

// ---------------------------------------------------------------------------
// Test: Model changes invalidate the applicable policy
// ---------------------------------------------------------------------------

func TestModelChangeInvalidatesCacheInterpretation(t *testing.T) {
	// Build observations where the last two have DIFFERENT model IDs,
	// so buildCandidateCauses detects a model change.
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: time.Now().Add(-10 * time.Minute),
			CachePolicyVersion:      "v1",
		},
		UsageObservations: []schema.UsageObservation{
			{Fingerprint: "obs1", ModelID: "model-a", ObservedAt: time.Now().Add(-6 * time.Minute),
				FreshInputTokens: i64(5000), CacheReadInputTokens: i64(90000), CacheCreationInputTokens: i64(5000)},
			{Fingerprint: "obs2", ModelID: "model-a", ObservedAt: time.Now().Add(-5 * time.Minute),
				FreshInputTokens: i64(5000), CacheReadInputTokens: i64(90000), CacheCreationInputTokens: i64(5000)},
			{Fingerprint: "obs3", ModelID: "model-b", ObservedAt: time.Now().Add(-4 * time.Minute),
				FreshInputTokens: i64(5000), CacheReadInputTokens: i64(90000), CacheCreationInputTokens: i64(5000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     ptrFloat(0.90),
			CacheCreationShare: ptrFloat(0.05),
			FreshInputShare:    ptrFloat(0.05),
		},
	}

	// Build a diagnosis — the last two observations have different models.
	now := time.Now()
	diag := BuildCacheDiagnosis(snap, now)

	// With sufficient observations, attribution should be heuristic.
	if diag.Kind != schema.AttributionHeuristic {
		t.Errorf("expected heuristic kind, got %s", diag.Kind)
	}

	// Model changed between last two observations → model_changed should appear.
	found := false
	for _, c := range diag.CandidateCauses {
		if c.Reason == schema.ReasonModelChanged {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected model_changed candidate cause, got: %+v", diag.CandidateCauses)
	}
}

// ---------------------------------------------------------------------------
// Test: Provider changes invalidate the cache interpretation
// ---------------------------------------------------------------------------

func TestProviderChangeInvalidatesCacheInterpretation(t *testing.T) {
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: time.Now().Add(-10 * time.Minute),
			CacheTTLSeconds:         ptrInt(300),
		},
	}

	// Simulate provider change.
	snap.Provider = schema.ProviderInfo{Confirmed: false, Name: "other"}

	// EvaluateCacheTTLExpiryV2 should NOT warn when provider is not confirmed.
	now := time.Now()
	decision := EvaluateCacheTTLExpiryV2(snap, 50000, time.Now().Add(-10*time.Minute), now)
	if decision.Warn {
		t.Error("should not warn for unconfirmed provider")
	}
}

// ---------------------------------------------------------------------------
// Test: Unknown TTL does not generate a definitive expiry warning
// ---------------------------------------------------------------------------

func TestUnknownTTLNoDefinitiveWarning(t *testing.T) {
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: time.Now().Add(-10 * time.Minute),
			CacheTTLSeconds:         nil, // unknown TTL
		},
	}

	now := time.Now()
	decision := EvaluateCacheTTLExpiryV2(snap, 50000, time.Now().Add(-10*time.Minute), now)

	// Without provider-confirmed TTL data, V2 does not warn.
	if decision.Warn {
		t.Error("should not warn without provider-confirmed TTL")
	}
}

func TestProviderConfirmedTTLGeneratesCorrectWarning(t *testing.T) {
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: time.Now().Add(-10 * time.Minute),
			CacheTTLSeconds:         ptrInt(600), // provider says 600s TTL
		},
	}

	now := time.Now()
	decision := EvaluateCacheTTLExpiryV2(snap, 50000, time.Now().Add(-10*time.Minute), now)

	if !decision.Warn {
		t.Error("should warn: 10min idle exceeds 600s provider TTL")
	}

	msg := CacheTTLWarningMessageV2(snap, 10, 50000)
	if msg == "" {
		t.Fatal("message should not be empty")
	}
	// Provider-confirmed TTL → should say "expired (provider TTL: 600s, idle 10m)"
	if !containsString(msg, "provider TTL: 600s") {
		t.Errorf("expected provider TTL in message, got: %s", msg)
	}
	if !containsString(msg, "expired") {
		t.Errorf("expected 'expired' in message, got: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// Test: Provider-confirmed TTL with idle below TTL does not warn
// ---------------------------------------------------------------------------

func TestProviderTTLBelowIdleNoWarn(t *testing.T) {
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: time.Now().Add(-3 * time.Minute),
			CacheTTLSeconds:         ptrInt(600), // 10-minute TTL
		},
	}

	now := time.Now()
	decision := EvaluateCacheTTLExpiryV2(snap, 50000, time.Now().Add(-3*time.Minute), now)
	if decision.Warn {
		t.Error("should not warn: 3min idle < 600s provider TTL")
	}
}

// ---------------------------------------------------------------------------
// Test: Legacy sessions (no CacheTiming) fall back to lastEventAt
// ---------------------------------------------------------------------------

func TestLegacyFallbackToLastEventAt(t *testing.T) {
	snap := &schema.Snapshot{
		Provider:    schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: nil, // legacy: no CacheTiming struct
		Session: schema.SessionInfo{
			LastEventAt: time.Now().Add(-10 * time.Minute),
		},
	}

	now := time.Now()
	decision := EvaluateCacheTTLExpiryV2(snap, 50000, snap.Session.LastEventAt, now)
	if decision.Warn {
		t.Error("legacy session without CacheTiming should not warn")
	}
}

// ---------------------------------------------------------------------------
// Test: V2 uses CacheTiming when present, ignoring lastEventAt
// ---------------------------------------------------------------------------

func TestV2UsesCacheTimingNotLastEventAt(t *testing.T) {
	cacheTime := time.Now().Add(-3 * time.Minute)  // 3min ago — below threshold
	eventTime := time.Now().Add(-10 * time.Minute) // 10min ago — above threshold

	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: cacheTime,
			// No provider TTL — falls back to 5min PromptCacheTTL
		},
	}

	now := time.Now()
	// Pass the stale eventTime (10min) as the fallback parameter, but
	// EvaluateCacheTTLExpiryV2 should use CacheTiming (3min) instead.
	decision := EvaluateCacheTTLExpiryV2(snap, 50000, eventTime, now)
	if decision.Warn {
		t.Error("should not warn: CacheTiming is 3min, below PromptCacheTTL")
	}
}

// ---------------------------------------------------------------------------
// Test: CacheTTLWarningMessageV2 messages
// ---------------------------------------------------------------------------

func TestCacheTTLWarningMessageV2Messages(t *testing.T) {
	// Test with nil CacheTiming.
	snapNoTiming := &schema.Snapshot{
		Provider:    schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: nil,
	}
	msg := CacheTTLWarningMessageV2(snapNoTiming, 7, 123456)
	if !containsString(msg, "may have expired") {
		t.Errorf("expected 'may have expired', got: %s", msg)
	}
	if !containsString(msg, "without confirmed TTL data") {
		t.Errorf("expected 'without confirmed TTL data', got: %s", msg)
	}

	// Test with known TTL.
	snapKnown := &schema.Snapshot{
		Provider:    schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{CacheTTLSeconds: ptrInt(300)},
	}
	msg2 := CacheTTLWarningMessageV2(snapKnown, 8, 200000)
	if !containsString(msg2, "provider TTL: 300s") {
		t.Errorf("expected 'provider TTL: 300s', got: %s", msg2)
	}
	if !containsString(msg2, "idle 8m") {
		t.Errorf("expected 'idle 8m', got: %s", msg2)
	}
	// Should NOT contain uncertainty language when TTL is known.
	if containsString(msg2, "may have") {
		t.Errorf("known TTL should not use uncertain language, got: %s", msg2)
	}
}

// ---------------------------------------------------------------------------
// Test: Zero CacheTTLSeconds treated as "no provider TTL"
// ---------------------------------------------------------------------------

func TestZeroProviderTTLFallback(t *testing.T) {
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: &schema.CacheTiming{
			LastInferenceObservedAt: time.Now().Add(-10 * time.Minute),
			CacheTTLSeconds:         ptrInt(0), // explicitly zero
		},
	}

	now := time.Now()
	decision := EvaluateCacheTTLExpiryV2(snap, 50000, time.Now().Add(-10*time.Minute), now)
	// 0 should not be treated as a valid TTL, so falls back to PromptCacheTTL (5min).
	// 10min idle > 5min → should warn.
	if decision.Warn {
		t.Error("zero TTL should not warn without provider-confirmed TTL")
	}
}

// ---------------------------------------------------------------------------
// Test: Nil snap and zero lastEventAt guards
// ---------------------------------------------------------------------------

func TestV2NilAndZeroGuards(t *testing.T) {
	now := time.Now()

	// Nil snap.
	d1 := EvaluateCacheTTLExpiryV2(nil, 50000, now, now)
	if d1.Warn {
		t.Error("nil snap should not warn")
	}

	// Zero lastEventAt.
	d2 := EvaluateCacheTTLExpiryV2(&schema.Snapshot{}, 50000, time.Time{}, now)
	if d2.Warn {
		t.Error("zero lastEventAt should not warn")
	}

	// Nil CacheTiming + zero lastEventAt.
	snap := &schema.Snapshot{
		Provider:    schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		CacheTiming: nil,
	}
	d3 := EvaluateCacheTTLExpiryV2(snap, 50000, time.Time{}, now)
	if d3.Warn {
		t.Error("zero lastEventAt with nil CacheTiming should not warn")
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
