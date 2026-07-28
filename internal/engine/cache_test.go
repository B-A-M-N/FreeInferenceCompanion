package engine

import (
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func i64(v int64) *int64 { return &v }

func obs(model string, totalIn, totalOut, fresh, read, creation, output int64) schema.UsageObservation {
	return schema.UsageObservation{
		Fingerprint:              ObservationFingerprint(model, totalIn, totalOut, i64(fresh), i64(read), i64(creation), i64(output)),
		ObservedAt:               time.Now(),
		ModelID:                  model,
		TotalInputTokens:         totalIn,
		TotalOutputTokens:        totalOut,
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
