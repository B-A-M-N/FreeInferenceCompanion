package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func ptrI64(v int64) *int64 { return &v }

func TestAttributeCacheMisses_InsufficientData(t *testing.T) {
	snap := &schema.Snapshot{
		UsageObservations: []schema.UsageObservation{},
		CacheAnalysis:     &schema.CacheAnalysis{RequestSamples: 0},
	}
	attr := AttributeCacheMisses(snap)
	if attr.Pattern != PatternInsufficientData {
		t.Errorf("expected insufficient_data, got %s", attr.Pattern)
	}
}

func TestAttributeCacheMisses_NoneWhenGoodReadShare(t *testing.T) {
	readShare := 0.65
	snap := &schema.Snapshot{
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(10000), CacheReadInputTokens: ptrI64(80000), CacheCreationInputTokens: ptrI64(10000)},
			{FreshInputTokens: ptrI64(10000), CacheReadInputTokens: ptrI64(80000), CacheCreationInputTokens: ptrI64(10000)},
			{FreshInputTokens: ptrI64(10000), CacheReadInputTokens: ptrI64(80000), CacheCreationInputTokens: ptrI64(10000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: ptrFloat(0.10),
			FreshInputShare:    ptrFloat(0.15),
		},
	}
	attr := AttributeCacheMisses(snap)
	if attr.Pattern != PatternNone {
		t.Errorf("expected none with good read share, got %s", attr.Pattern)
	}
}

func TestAttributeCacheMisses_Thrashing(t *testing.T) {
	// High cache_creation, low cache_read.
	readShare := 0.10
	creationShare := 0.50
	freshShare := 0.40
	snap := &schema.Snapshot{
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     4,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	attr := AttributeCacheMisses(snap)
	if attr.Pattern != PatternThrashing {
		t.Errorf("expected thrashing, got %s", attr.Pattern)
	}
	if attr.Recommendation == "" {
		t.Error("thrashing should have a recommendation")
	}
}

func TestAttributeCacheMisses_NoCaching(t *testing.T) {
	// High fresh input, negligible creation and read.
	readShare := 0.05
	creationShare := 0.05
	freshShare := 0.90
	snap := &schema.Snapshot{
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(90000), CacheReadInputTokens: ptrI64(5000), CacheCreationInputTokens: ptrI64(5000)},
			{FreshInputTokens: ptrI64(90000), CacheReadInputTokens: ptrI64(5000), CacheCreationInputTokens: ptrI64(5000)},
			{FreshInputTokens: ptrI64(90000), CacheReadInputTokens: ptrI64(5000), CacheCreationInputTokens: ptrI64(5000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	attr := AttributeCacheMisses(snap)
	if attr.Pattern != PatternNoCaching {
		t.Errorf("expected no_caching, got %s", attr.Pattern)
	}
}

func TestAttributeCacheMisses_Decay(t *testing.T) {
	// Read share was good but is now declining.
	prevShare := 0.45
	readShare := 0.15
	creationShare := 0.10
	freshShare := 0.75
	snap := &schema.Snapshot{
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(50000), CacheReadInputTokens: ptrI64(45000), CacheCreationInputTokens: ptrI64(5000)},
			{FreshInputTokens: ptrI64(70000), CacheReadInputTokens: ptrI64(20000), CacheCreationInputTokens: ptrI64(10000)},
			{FreshInputTokens: ptrI64(75000), CacheReadInputTokens: ptrI64(15000), CacheCreationInputTokens: ptrI64(10000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
			PreviousReadShare:  &prevShare,
			Trend:              schema.TrendDeclining,
		},
	}
	attr := AttributeCacheMisses(snap)
	if attr.Pattern != PatternDecay {
		t.Errorf("expected decay, got %s (diag: %s)", attr.Pattern, attr.Diagnosis)
	}
}

func TestAttributeCacheMisses_Intermittent(t *testing.T) {
	// Alternating good/bad read shares with aggregate below 0.20 threshold.
	// 5 observations: bad, good, bad, good, bad
	// bad:  100K fresh, 2K read, 3K create → read = 2/105 = 0.019
	// good: 10K fresh, 40K read, 5K create → read = 40/55 = 0.727
	// bad:  100K fresh, 2K read, 3K create → read = 0.019
	// good: 10K fresh, 40K read, 5K create → read = 0.727
	// bad:  100K fresh, 2K read, 3K create → read = 0.019
	// Aggregate: fresh=320, read=86, create=19, total=425
	// readShare=86/425=0.202 — borderline. Add one more bad to push it under.
	// Use 6: bad, good, bad, good, bad, bad
	// fresh=420, read=88, create=22, total=530
	// readShare=88/530=0.166 < 0.20 ✓
	// creationShare=22/530=0.042 < 0.30 (no thrashing) ✓
	// freshShare=420/530=0.792 ≥ 0.70 — this will match NoCaching!
	// Need to lower fresh input on bad observations or increase create.
	// Adjust bad: 80K fresh, 2K read, 15K create → read=0.021, create high
	// but aggregate creation will still be moderate.
	// Let's use:
	// bad:  60K fresh, 2K read, 8K create
	// good: 10K fresh, 40K read, 5K create
	// 5 obs: bad, good, bad, good, bad
	// fresh=250, read=86, create=34, total=370
	// readShare=86/370=0.232 — too high. Add one more bad:
	// 6 obs: bad, good, bad, good, bad, bad
	// fresh=310, read=88, create=42, total=440
	// readShare=88/440=0.20 — still borderline. Use 7 obs:
	// fresh=370, read=90, create=50, total=510
	// readShare=90/510=0.176 < 0.20 ✓
	// creationShare=50/510=0.098 < 0.30 ✓
	// freshShare=370/510=0.725 ≥ 0.70 — will match NoCaching!
	// Need freshShare < 0.70. Reduce fresh or increase other components.
	// Use: bad=40K fresh, 2K read, 8K create; good=15K fresh, 40K read, 5K create
	// 7 obs: bad,good,bad,good,bad,good,bad
	// bad(4): 160K fresh, 8K read, 32K create
	// good(3): 45K fresh, 120K read, 15K create
	// total: 205 fresh, 128 read, 47 create, sum=380
	// readShare=128/380=0.337 — too high again.
	// The problem: "good" observations have high read which inflates aggregate.
	// Solution: make bad observations dominate with extreme fresh, make good
	// observations fewer/smaller.
	// bad(5): 90K fresh, 1K read, 4K create each
	// good(2): 5K fresh, 30K read, 5K create each
	// fresh=460, read=65, create=30, total=555
	// readShare=65/555=0.117 < 0.20 ✓
	// creationShare=30/555=0.054 < 0.30 ✓
	// freshShare=460/555=0.829 ≥ 0.70 → NoCaching fires first!
	// The NoCaching gate requires creation < 0.10 AND read < 0.10.
	// readShare=0.117 > 0.10 → NoCaching doesn't fire ✓
	readShare := 0.117
	creationShare := 0.054
	freshShare := 0.829
	bad := schema.UsageObservation{FreshInputTokens: ptrI64(90000), CacheReadInputTokens: ptrI64(1000), CacheCreationInputTokens: ptrI64(4000)}
	good := schema.UsageObservation{FreshInputTokens: ptrI64(5000), CacheReadInputTokens: ptrI64(30000), CacheCreationInputTokens: ptrI64(5000)}
	snap := &schema.Snapshot{
		UsageObservations: []schema.UsageObservation{bad, good, bad, good, bad, bad, bad},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     7,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	attr := AttributeCacheMisses(snap)
	if attr.Pattern != PatternIntermittent {
		t.Errorf("expected intermittent, got %s (diag: %s)", attr.Pattern, attr.Diagnosis)
	}
}

func ptrFloat(v float64) *float64 { return &v }

// ============================================================
// BuildCacheDiagnosis tests (new structured API)
// ============================================================

func TestBuildCacheDiagnosisHonestUnknown(t *testing.T) {
	snap := &schema.Snapshot{
		Provider:          schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{},
		CacheAnalysis:     &schema.CacheAnalysis{RequestSamples: 0},
	}
	now := time.Now()
	diag := BuildCacheDiagnosis(snap, now)

	if diag.Kind != schema.AttributionUnknown {
		t.Errorf("kind = %s, want unknown", diag.Kind)
	}
	if diag.Confidence != 0 {
		t.Errorf("confidence = %f, want 0", diag.Confidence)
	}
	if len(diag.Evidence) == 0 {
		t.Error("evidence must mention insufficient observations")
	}
}

func TestBuildCacheDiagnosisCandidateCauses(t *testing.T) {
	readShare := 0.10
	creationShare := 0.50
	freshShare := 0.40
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	diag := BuildCacheDiagnosis(snap, time.Now())
	if len(diag.CandidateCauses) == 0 {
		t.Error("thrashing pattern should produce candidate causes")
	}
	if diag.ReasonCode == schema.ReasonUnknown && len(diag.CandidateCauses) > 0 {
		t.Error("reason code should be set from top candidate cause")
	}
}

func TestBuildCacheDiagnosisEvidence(t *testing.T) {
	readShare := 0.90
	creationShare := 0.05
	freshShare := 0.05
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(5000), CacheReadInputTokens: ptrI64(90000), CacheCreationInputTokens: ptrI64(5000)},
			{FreshInputTokens: ptrI64(5000), CacheReadInputTokens: ptrI64(90000), CacheCreationInputTokens: ptrI64(5000)},
			{FreshInputTokens: ptrI64(5000), CacheReadInputTokens: ptrI64(90000), CacheCreationInputTokens: ptrI64(5000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	diag := BuildCacheDiagnosis(snap, time.Now())

	// Should have evidence about cache read share
	found := false
	for _, e := range diag.Evidence {
		if e.Source == "client_observed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("evidence should contain client_observed items")
	}
}

func TestBuildCacheDiagnosisMissingEvidence(t *testing.T) {
	readShare := 0.90
	creationShare := 0.05
	freshShare := 0.05
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(5000), CacheReadInputTokens: ptrI64(90000), CacheCreationInputTokens: ptrI64(5000)},
			{FreshInputTokens: ptrI64(5000), CacheReadInputTokens: ptrI64(90000), CacheCreationInputTokens: ptrI64(5000)},
			{FreshInputTokens: ptrI64(5000), CacheReadInputTokens: ptrI64(90000), CacheCreationInputTokens: ptrI64(5000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	diag := BuildCacheDiagnosis(snap, time.Now())

	// Missing evidence should list provider-supplied metadata we lack
	if len(diag.MissingEvidence) == 0 {
		t.Error("missing evidence should list unavailable provider metadata")
	}
}

func TestBuildCacheDiagnosisModelChange(t *testing.T) {
	readShare := 0.10
	creationShare := 0.10
	freshShare := 0.80
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{
			{ModelID: "model-a", FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000), ObservedAt: time.Now().Add(-6 * time.Minute)},
			{ModelID: "model-a", FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000), ObservedAt: time.Now().Add(-5 * time.Minute)},
			{ModelID: "model-b", FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000), ObservedAt: time.Now().Add(-4 * time.Minute)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	diag := BuildCacheDiagnosis(snap, time.Now())

	// Model changed between last two observations → model_changed should appear
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

func TestBuildCacheDiagnosisConfidenceFromSamples(t *testing.T) {
	readShare := 0.10
	_ = (0.10) // creationShare — not used, confidence depends on sample count
	_ = (0.80) // freshShare — not used, confidence depends on sample count

	// With only 2 observations, confidence should be 0 (below minimum).
	snap2 := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000)},
			{FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{RequestSamples: 2, CacheReadShare: &readShare},
	}
	diag2 := BuildCacheDiagnosis(snap2, time.Now())
	if diag2.Confidence != 0 {
		t.Errorf("confidence with 2 samples = %f, want 0", diag2.Confidence)
	}

	// With 3+ observations, confidence > 0.
	snap3 := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000)},
			{FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000)},
			{FreshInputTokens: ptrI64(80000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(10000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{RequestSamples: 3, CacheReadShare: &readShare},
	}
	diag3 := BuildCacheDiagnosis(snap3, time.Now())
	if diag3.Confidence <= 0 {
		t.Errorf("confidence with 3 samples = %f, want > 0", diag3.Confidence)
	}
	if diag3.Confidence > 0.8 {
		t.Errorf("confidence capped at 0.8, got %f", diag3.Confidence)
	}
}

// TestCacheWarningHonestLanguage verifies that cache diagnosis uses heuristic
// language — it must never claim "root cause" or "provider_confirmed" when the
// provider has not explicitly confirmed the diagnosis.
func TestCacheWarningHonestLanguage(t *testing.T) {
	readShare := 0.10
	creationShare := 0.50
	freshShare := 0.40
	snap := &schema.Snapshot{
		Provider: schema.ProviderInfo{Confirmed: true, Name: schema.ProviderFreeInference},
		UsageObservations: []schema.UsageObservation{
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
			{FreshInputTokens: ptrI64(40000), CacheReadInputTokens: ptrI64(10000), CacheCreationInputTokens: ptrI64(50000)},
		},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples:     3,
			CacheReadShare:     &readShare,
			CacheCreationShare: &creationShare,
			FreshInputShare:    &freshShare,
		},
	}
	diag := BuildCacheDiagnosis(snap, time.Now())

	// Kind must be heuristic — provider supplied no diagnosis.
	if diag.Kind != schema.AttributionHeuristic {
		t.Errorf("kind = %s, want heuristic (provider supplies no diagnosis)", diag.Kind)
	}

	// Candidate causes must be present and reason code derived from top cause.
	if len(diag.CandidateCauses) == 0 {
		t.Error("candidate causes must be present")
	}
	if diag.ReasonCode == schema.ReasonUnknown {
		t.Error("reason code must be derived from top candidate cause")
	}

	// Evidence must contain client_observed telemetry.
	hasClientObserved := false
	for _, e := range diag.Evidence {
		if e.Source == "client_observed" {
			hasClientObserved = true
			break
		}
	}
	if !hasClientObserved {
		t.Error("evidence must include client_observed items")
	}

	// Missing evidence should list provider-supplied metadata we lack.
	if len(diag.MissingEvidence) == 0 {
		t.Error("missing evidence should list unavailable provider metadata")
	}

	// Candidate cause labels must not claim "root cause" or "confirmed".
	for _, cc := range diag.CandidateCauses {
		if strings.Contains(strings.ToLower(cc.Label), "root cause") {
			t.Errorf("candidate cause label must not claim 'root cause': %s", cc.Label)
		}
		if strings.Contains(strings.ToLower(cc.Label), "confirmed") {
			t.Errorf("candidate cause label must not claim 'confirmed': %s", cc.Label)
		}
	}
}
