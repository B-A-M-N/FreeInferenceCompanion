package engine

import (
	"testing"
	"time"

	"github.com/bamn/freeinference-companion/pkg/schema"
)

func TestComputePressure_Healthy(t *testing.T) {
	tests := []struct {
		pct        float64
		current    string
		want       string
	}{
		{50, schema.PressureHealthy, schema.PressureHealthy},
		{69, schema.PressureHealthy, schema.PressureHealthy},
		{70, schema.PressureHealthy, schema.PressureWatch},
		{80, schema.PressureHealthy, schema.PressureWarn},
		{90, schema.PressureHealthy, schema.PressureCritical},
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Hysteresis_Warn(t *testing.T) {
	// Hysteresis: enter WARN at 80%, but don't leave until below 65%
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{80, schema.PressureHealthy, schema.PressureWarn},  // enter
		{75, schema.PressureWarn, schema.PressureWarn},     // stays (above leave threshold)
		{65, schema.PressureWarn, schema.PressureWarn},     // still at threshold
		{60, schema.PressureWarn, schema.PressureRecovering}, // below leave threshold
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Hysteresis_Critical(t *testing.T) {
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{90, schema.PressureHealthy, schema.PressureCritical},  // enter
		{80, schema.PressureCritical, schema.PressureCritical},  // stays (above leave threshold)
		{75, schema.PressureCritical, schema.PressureCritical},  // at threshold, stays
		{70, schema.PressureCritical, schema.PressureRecovering}, // below
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Recovering(t *testing.T) {
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{60, schema.PressureRecovering, schema.PressureHealthy}, // recovered
		{70, schema.PressureRecovering, schema.PressureRecovering}, // still recovering
		{80, schema.PressureRecovering, schema.PressureWarn},   // re-warned
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Unknown(t *testing.T) {
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{50, schema.PressureUnknown, schema.PressureHealthy},
		{75, schema.PressureUnknown, schema.PressureWatch},
		{85, schema.PressureUnknown, schema.PressureWarn},
		{95, schema.PressureUnknown, schema.PressureCritical},
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestEstimateProjectedContext(t *testing.T) {
	// 100K current, 200K limit, 10K prompt → should project ~132K
	proj := EstimateProjectedContext(100000, 200000, 10000, 16000)
	if proj.ProjectedTotal <= 100000 {
		t.Errorf("projected total %d should exceed current %d", proj.ProjectedTotal, 100000)
	}
	if proj.Confidence != ProjectionConfidenceHigh {
		t.Errorf("confidence should be high when current > 0")
	}
	if proj.ProjectedPercent > 100 {
		t.Errorf("projected percent %.0f should not exceed 100", proj.ProjectedPercent)
	}
}

func TestEstimateProjectedContext_Overflow(t *testing.T) {
	// 180K current, 200K limit, 10K prompt → should project overflow
	proj := EstimateProjectedContext(180000, 200000, 10000, 16000)
	if proj.ProjectedPercent <= 90 {
		t.Errorf("projected percent %.0f should be critical", proj.ProjectedPercent)
	}
}

func TestEstimateProjectedContext_LowConfidence(t *testing.T) {
	// 0 current → low confidence
	proj := EstimateProjectedContext(0, 200000, 1000, 16000)
	if proj.Confidence != ProjectionConfidenceLow {
		t.Errorf("confidence should be low when current is 0")
	}
}

func TestAnalyzeCacheWindow_InsufficientData(t *testing.T) {
	samples := []CacheSample{
		{FreshInputTokens: 1000, CacheReadInputTokens: 0},
	}
	result := AnalyzeCacheWindow(samples, schema.TrendInsufficientData)
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.Trend != schema.TrendInsufficientData {
		t.Errorf("trend should be insufficient_data, got %s", result.Trend)
	}
}

func TestAnalyzeCacheWindow_Valid(t *testing.T) {
	samples := []CacheSample{
		{FreshInputTokens: 10000, CacheReadInputTokens: 90000, CacheCreationInputTokens: 0},
		{FreshInputTokens: 20000, CacheReadInputTokens: 80000, CacheCreationInputTokens: 0},
		{FreshInputTokens: 15000, CacheReadInputTokens: 85000, CacheCreationInputTokens: 0},
	}
	result := AnalyzeCacheWindow(samples, schema.TrendStable)
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.RequestSamples != 3 {
		t.Errorf("expected 3 samples, got %d", result.RequestSamples)
	}
	// Total: 1000+2000+1500 = 4500 fresh, 9000+8000+8500 = 25500 read → 30000 total
	// Read share: 25500/30000 = 0.85
	if result.CacheReadShare < 0.80 || result.CacheReadShare > 0.90 {
		t.Errorf("read share %.2f should be ~0.85", result.CacheReadShare)
	}
}

func TestShouldWarnCache(t *testing.T) {
	// High cache reuse → no warning
	good := &CacheWindowResult{
		RequestSamples:     5,
		TotalObservedInput: 100000,
		CacheReadShare:     0.85,
	}
	if ShouldWarnCache(good) {
		t.Error("should not warn on high cache reuse")
	}

	// Low cache reuse → warning
	bad := &CacheWindowResult{
		RequestSamples:     5,
		TotalObservedInput: 100000,
		CacheReadShare:     0.10,
	}
	if !ShouldWarnCache(bad) {
		t.Error("should warn on low cache reuse")
	}

	// Insufficient data → no warning
	short := &CacheWindowResult{
		RequestSamples:     1,
		TotalObservedInput: 1000,
		CacheReadShare:     0.10,
	}
	if ShouldWarnCache(short) {
		t.Error("should not warn on insufficient data")
	}
}

func TestComputeCompactionReduction(t *testing.T) {
	pct := ComputeCompactionReduction(200000, 50000)
	if pct != 75.0 {
		t.Errorf("expected 75%% reduction, got %.1f%%", pct)
	}

	pct = ComputeCompactionReduction(100000, 100000)
	if pct != 0 {
		t.Errorf("expected 0%% reduction, got %.1f%%", pct)
	}

	pct = ComputeCompactionReduction(0, 0)
	if pct != 0 {
		t.Errorf("expected 0%% reduction when no pre tokens, got %.1f%%", pct)
	}
}

func TestWarningDedup(t *testing.T) {
	wd := &WarningDedup{}
	now := time.Now()

	// First warning should show
	if !wd.ShouldShowContextWarning(now) {
		t.Error("first context warning should show")
	}
	wd.MarkContextWarningShown(now)

	// Shortly after should NOT show
	if wd.ShouldShowContextWarning(now.Add(1 * time.Minute)) {
		t.Error("context warning should be suppressed during cooldown")
	}

	// After cooldown should show again
	if !wd.ShouldShowContextWarning(now.Add(20 * time.Minute)) {
		t.Error("context warning should show after cooldown")
	}
}