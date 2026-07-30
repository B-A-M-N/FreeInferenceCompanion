package engine

import (
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func i64(v int64) *int64 { return &v }

func obs(model string, totalIn, totalOut, fresh, read, creation, output int64) schema.UsageObservation {
	return schema.UsageObservation{
		Fingerprint: func() string {
			fp, _ := ObservationFingerprint(model, "", totalIn, totalOut, i64(fresh), i64(read), i64(creation), i64(output))
			return fp
		}(),
		ObservedAt:               time.Now(),
		ModelID:                  model,
		TotalInputTokens:         i64(totalIn),
		TotalOutputTokens:        i64(totalOut),
		FreshInputTokens:         i64(fresh),
		CacheReadInputTokens:     i64(read),
		CacheCreationInputTokens: i64(creation),
		OutputTokens:             i64(output),
	}
}

func TestAddObservationDeduplicates(t *testing.T) {
	snap := &schema.Snapshot{}
	o := obs("m", 100000, 1000, 5000, 90000, 5000, 1000)

	if !AddObservation(snap, o) {
		t.Error("first observation should be added")
	}
	if AddObservation(snap, o) {
		t.Error("duplicate fingerprint must be ignored")
	}
	if len(snap.UsageObservations) != 1 {
		t.Errorf("observations = %d", len(snap.UsageObservations))
	}
}

func TestAddObservationBounded(t *testing.T) {
	snap := &schema.Snapshot{}
	for i := 0; i < MaxUsageObservations+10; i++ {
		AddObservation(snap, obs("m", int64(100000+i), 1000, 5000, 90000, 5000, 1000))
	}
	if len(snap.UsageObservations) != MaxUsageObservations {
		t.Errorf("observations = %d, want %d", len(snap.UsageObservations), MaxUsageObservations)
	}
}

func TestAnalyzeCacheShares(t *testing.T) {
	snap := &schema.Snapshot{}
	// 90% read share.
	AddObservation(snap, obs("m", 100000, 1000, 5000, 90000, 5000, 1000))
	AnalyzeCache(snap, 100000, time.Now())

	a := snap.CacheAnalysis
	if a == nil || a.CacheReadShare == nil {
		t.Fatal("no analysis")
	}
	if *a.CacheReadShare < 0.89 || *a.CacheReadShare > 0.91 {
		t.Errorf("read share = %v", *a.CacheReadShare)
	}
	if a.RequestSamples != 1 {
		t.Errorf("samples = %d", a.RequestSamples)
	}
}

func TestAnalyzeCacheTrend(t *testing.T) {
	snap := &schema.Snapshot{}

	AddObservation(snap, obs("m", 100000, 1000, 5000, 90000, 5000, 1000)) // 90%
	AnalyzeCache(snap, 100000, time.Now())
	if snap.CacheAnalysis.Trend != schema.TrendInsufficientData {
		t.Errorf("first trend = %s", snap.CacheAnalysis.Trend)
	}

	// Within ±5 points → stable.
	AddObservation(snap, obs("m", 101000, 1000, 5000, 92000, 5000, 1000)) // ≈90%
	AnalyzeCache(snap, 100000, time.Now())
	if snap.CacheAnalysis.Trend != schema.TrendStable {
		t.Errorf("trend = %s, want stable", snap.CacheAnalysis.Trend)
	}

	// Drop by >5 points → declining.
	AddObservation(snap, obs("m", 102000, 1000, 50000, 30000, 5000, 1000)) // 35% pulls window down hard
	AnalyzeCache(snap, 100000, time.Now())
	if snap.CacheAnalysis.Trend != schema.TrendDeclining {
		t.Errorf("trend = %s, want declining", snap.CacheAnalysis.Trend)
	}
}

func TestAnalyzeCacheTrendRising(t *testing.T) {
	snap := &schema.Snapshot{}
	AddObservation(snap, obs("m", 100000, 1000, 50000, 45000, 5000, 1000)) // 45%
	AnalyzeCache(snap, 100000, time.Now())
	AddObservation(snap, obs("m", 101000, 1000, 5000, 90000, 5000, 1000)) // window avg jumps
	AnalyzeCache(snap, 100000, time.Now())
	if snap.CacheAnalysis.Trend != schema.TrendRising {
		t.Errorf("trend = %s, want rising", snap.CacheAnalysis.Trend)
	}
}

func TestQualifyCacheWarningNeedsThreeObservations(t *testing.T) {
	snap := &schema.Snapshot{}
	for _, total := range []int64{100000, 101000} {
		AddObservation(snap, obs("m", total, 1000, total-10000, 5000, 5000, 1000))
		AnalyzeCache(snap, total, time.Now())
	}
	d := QualifyCacheWarning(snap, 100000, true, time.Now())
	if d.Warn {
		t.Error("two observations must not warn")
	}
}

func TestQualifyCacheWarningUnder50KContext(t *testing.T) {
	snap := &schema.Snapshot{}
	for _, total := range []int64{10000, 11000, 12000} {
		AddObservation(snap, obs("m", total, 1000, total-1000, 500, 500, 1000))
		AnalyzeCache(snap, total, time.Now())
	}
	d := QualifyCacheWarning(snap, 12000, true, time.Now())
	if d.Warn {
		t.Error("under-50K context must not warn")
	}
}

func TestQualifyCacheWarningRequiresConfirmedProvider(t *testing.T) {
	snap := &schema.Snapshot{}
	for _, total := range []int64{100000, 101000, 102000} {
		AddObservation(snap, obs("m", total, 1000, total-10000, 5000, 5000, 1000))
		AnalyzeCache(snap, total, time.Now())
	}
	d := QualifyCacheWarning(snap, 100000, false, time.Now())
	if d.Warn {
		t.Error("unconfirmed provider must not warn")
	}
}

func TestQualifyCacheWarningActivatesAfterThree(t *testing.T) {
	snap := &schema.Snapshot{}
	now := time.Now()
	for _, total := range []int64{100000, 101000, 102000} {
		AddObservation(snap, obs("m", total, 1000, total-10000, 5000, 5000, 1000))
		AnalyzeCache(snap, total, now)
	}
	if snap.CacheAnalysis.ConsecutiveLow != 3 {
		t.Fatalf("consecutive low = %d", snap.CacheAnalysis.ConsecutiveLow)
	}
	d := QualifyCacheWarning(snap, 100000, true, now)
	if !d.Warn {
		t.Error("three low observations should warn")
	}
}

func TestQualifyCacheWarningCooldown(t *testing.T) {
	snap := &schema.Snapshot{}
	now := time.Now()
	for _, total := range []int64{100000, 101000, 102000} {
		AddObservation(snap, obs("m", total, 1000, total-10000, 5000, 5000, 1000))
		AnalyzeCache(snap, total, now)
	}
	shown := now.Add(-10 * time.Minute) // inside 30-minute cooldown
	snap.Warnings.LastCacheShownAt = &shown
	d := QualifyCacheWarning(snap, 100000, true, now)
	if d.Warn {
		t.Error("cooldown must suppress repeat warnings")
	}

	shown = now.Add(-31 * time.Minute)
	snap.Warnings.LastCacheShownAt = &shown
	d = QualifyCacheWarning(snap, 100000, true, now)
	if !d.Warn {
		t.Error("expired cooldown should warn again")
	}
}

func TestQualifyCacheWarningResolvesAfterRecovery(t *testing.T) {
	snap := &schema.Snapshot{}
	snap.Warnings.CacheLowActive = true
	now := time.Now()
	// Three healthy observations (90% read share).
	for _, total := range []int64{100000, 101000, 102000} {
		AddObservation(snap, obs("m", total, 1000, 5000, total-10000, 5000, 1000))
		AnalyzeCache(snap, total, now)
	}
	if snap.CacheAnalysis.ConsecutiveRecovered != 3 {
		t.Fatalf("consecutive recovered = %d", snap.CacheAnalysis.ConsecutiveRecovered)
	}
	d := QualifyCacheWarning(snap, 100000, true, now)
	if !d.Resolved {
		t.Error("three recovered observations should resolve the warning")
	}
}

// TestAnalyzeCacheIdempotentCounters verifies that re-running analysis on
// unchanged state never inflates consecutive counters — duplicate status-line
// renders of the same response cannot manufacture a cache-low warning.
func TestAnalyzeCacheIdempotentCounters(t *testing.T) {
	snap := &schema.Snapshot{}
	now := time.Now()
	// Add three low-cache observations (10% read share).
	for _, total := range []int64{100000, 101000, 102000} {
		AddObservation(snap, obs("m", total, 1000, total-10000, 5000, 5000, 1000))
	}

	// Analyze once.
	AnalyzeCache(snap, 100000, now)
	if snap.CacheAnalysis.ConsecutiveLow != 3 {
		t.Fatalf("after first analysis, consecutive low = %d, want 3", snap.CacheAnalysis.ConsecutiveLow)
	}

	// Analyze the same state ten more times.
	for i := 0; i < 10; i++ {
		AnalyzeCache(snap, 100000, now)
	}

	if snap.CacheAnalysis.ConsecutiveLow != 3 {
		t.Fatalf("after reanalysis, consecutive low = %d, want 3 (counters must be idempotent)", snap.CacheAnalysis.ConsecutiveLow)
	}
	if snap.CacheAnalysis.ConsecutiveRecovered != 0 {
		t.Fatalf("after reanalysis, consecutive recovered = %d, want 0", snap.CacheAnalysis.ConsecutiveRecovered)
	}
}

func TestObservationFingerprintWithPromptID(t *testing.T) {
	// Same token counts but different prompt IDs → distinct fingerprints.
	fp1, src1 := ObservationFingerprint("m", "prompt-aaa", 100000, 1000, i64(5000), i64(90000), i64(5000), i64(1000))
	fp2, src2 := ObservationFingerprint("m", "prompt-bbb", 100000, 1000, i64(5000), i64(90000), i64(5000), i64(1000))
	if fp1 == fp2 {
		t.Fatal("different prompt IDs must produce different fingerprints")
	}
	if src1 != schema.FingerprintClientTurnID {
		t.Errorf("prompt ID source = %s, want %s", src1, schema.FingerprintClientTurnID)
	}
	if src2 != schema.FingerprintClientTurnID {
		t.Errorf("prompt ID source = %s, want %s", src2, schema.FingerprintClientTurnID)
	}

	// Same prompt ID → same fingerprint even with different tokens (prompt_id wins).
	fp3, _ := ObservationFingerprint("m", "prompt-aaa", 200000, 2000, i64(10000), i64(180000), i64(10000), i64(2000))
	if fp1 != fp3 {
		t.Fatal("same prompt ID must produce same fingerprint")
	}

	// No prompt ID → falls back to token-based fingerprint.
	fp4, src4 := ObservationFingerprint("m", "", 100000, 1000, i64(5000), i64(90000), i64(5000), i64(1000))
	fp5, src5 := ObservationFingerprint("m", "", 100000, 1000, i64(5000), i64(90000), i64(5000), i64(1000))
	if fp4 != fp5 {
		t.Fatal("token-based fallback must be deterministic")
	}
	if src4 != schema.FingerprintFallback {
		t.Errorf("token fallback source = %s, want %s", src4, schema.FingerprintFallback)
	}
	if src5 != schema.FingerprintFallback {
		t.Errorf("token fallback source = %s, want %s", src5, schema.FingerprintFallback)
	}
	fp6, _ := ObservationFingerprint("m", "", 100001, 1000, i64(5000), i64(90000), i64(5000), i64(1000))
	if fp4 == fp6 {
		t.Fatal("different token counts must produce different fingerprints without prompt_id")
	}
}

// TestObservationFingerprintSource verifies that the fingerprint source is
// correctly reported for both prompt-ID and token-based paths.
func TestObservationFingerprintSource(t *testing.T) {
	// Prompt-ID path: source must be FingerprintClientTurnID.
	_, src1 := ObservationFingerprint("m", "some-request-id", 1000, 100, i64(100), i64(800), i64(100), i64(100))
	if src1 != schema.FingerprintClientTurnID {
		t.Errorf("prompt-ID source = %s, want %s", src1, schema.FingerprintClientTurnID)
	}

	// Token fallback path: source must be FingerprintFallback.
	_, src2 := ObservationFingerprint("m", "", 1000, 100, i64(100), i64(800), i64(100), i64(100))
	if src2 != schema.FingerprintFallback {
		t.Errorf("token fallback source = %s, want %s", src2, schema.FingerprintFallback)
	}
}

// TestDistinctRequestsSameTokenCounts verifies that requests with the same
// token counts but different model IDs produce different fingerprints.
func TestDistinctRequestsSameTokenCounts(t *testing.T) {
	fpA, _ := ObservationFingerprint("model-a", "", 100000, 1000, i64(5000), i64(90000), i64(5000), i64(1000))
	fpB, _ := ObservationFingerprint("model-b", "", 100000, 1000, i64(5000), i64(90000), i64(5000), i64(1000))
	if fpA == fpB {
		t.Fatal("different models must produce different fingerprints when no prompt ID is available")
	}
}
