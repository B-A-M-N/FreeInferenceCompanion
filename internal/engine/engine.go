package engine

import (
	"fmt"
	"math"
	"time"

	"github.com/bamn/freeinference-companion/pkg/schema"
)

// ============================================================
// Pressure thresholds
// ============================================================

const (
	// Thresholds for entering states (higher bar)
	WatchEnterThreshold   = 70.0
	WarnEnterThreshold    = 80.0
	CriticalEnterThreshold = 90.0

	// Thresholds for leaving states (lower bar = hysteresis)
	WatchLeaveThreshold    = 60.0
	WarnLeaveThreshold     = 65.0
	CriticalLeaveThreshold = 75.0

	// Default output reserve in tokens
	DefaultOutputReserve = 16000

	// Rolling window requirements
	MinCacheAnalysisRequests = 3
	MinCacheAnalysisContext  = 50000

	// Cache thresholds
	LowCacheReuseThreshold    = 0.20
	CacheRecoveryThreshold    = 0.40
	CacheRecoveryRequests     = 3

	// Warning cooldowns
	ContextWarningCooldown = 15 * time.Minute
	CacheWarningCooldown   = 30 * time.Minute

	// Projection confidence labels
	ProjectionConfidenceLow  = "low"
	ProjectionConfidenceHigh = "high"
)

// ============================================================
// Pressure state machine
// ============================================================

// ComputePressure determines the next pressure state based on used percentage and current state.
// Implements hysteresis: entering a state requires a higher threshold than leaving it.
func ComputePressure(usedPct float64, currentState string) (string, string) {
	switch currentState {
	case schema.PressureUnknown:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureUnknown
		}
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureUnknown
		}
		if usedPct >= WatchEnterThreshold {
			return schema.PressureWatch, schema.PressureUnknown
		}
		return schema.PressureHealthy, schema.PressureUnknown

	case schema.PressureHealthy:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureHealthy
		}
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureHealthy
		}
		if usedPct >= WatchEnterThreshold {
			return schema.PressureWatch, schema.PressureHealthy
		}
		return schema.PressureHealthy, schema.PressureHealthy

	case schema.PressureWatch:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureWatch
		}
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureWatch
		}
		if usedPct < WatchLeaveThreshold {
			return schema.PressureHealthy, schema.PressureWatch
		}
		return schema.PressureWatch, schema.PressureWatch

	case schema.PressureWarn:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureWarn
		}
		if usedPct < WarnLeaveThreshold {
			return schema.PressureRecovering, schema.PressureWarn
		}
		return schema.PressureWarn, schema.PressureWarn

	case schema.PressureCritical:
		if usedPct < CriticalLeaveThreshold {
			return schema.PressureRecovering, schema.PressureCritical
		}
		return schema.PressureCritical, schema.PressureCritical

	case schema.PressureRecovering:
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureRecovering
		}
		if usedPct < WarnLeaveThreshold {
			return schema.PressureHealthy, schema.PressureRecovering
		}
		return schema.PressureRecovering, schema.PressureRecovering

	default:
		return schema.PressureUnknown, schema.PressureUnknown
	}
}

// ClassifyPressure determines the state and reason for a given context percentage.
func ClassifyPressure(usedPct float64, prevState string) (newState string, reason *string) {
	newState, _ = ComputePressure(usedPct, prevState)

	var r *string
	switch newState {
	case schema.PressureWatch:
		s := "context approaching watch threshold"
		r = &s
	case schema.PressureWarn:
		s := "context above warn threshold"
		r = &s
	case schema.PressureCritical:
		s := "context at critical level"
		r = &s
	case schema.PressureRecovering:
		s := "context recovering after pressure reduction"
		r = &s
	}
	return newState, r
}

// ============================================================
// Projected context estimation
// ============================================================

// ProjectedContext estimates the total context usage after the next request.
type ProjectedContext struct {
	CurrentTokens     int64
	EstimatedPrompt   int64
	ToolOverhead      int64
	OutputReserve     int64
	SafetyMargin      int64
	ProjectedTotal    int64
	ContextLimit      int64
	ProjectedPercent  float64
	Confidence        string
}

// EstimateProjectedContext computes a projected context value.
// The output_reserve is subtracted from remaining space, not added to input.
func EstimateProjectedContext(currentTokens, contextLimit, estimatedPrompt int64, outputReserve int64) ProjectedContext {
	if outputReserve <= 0 {
		outputReserve = DefaultOutputReserve
	}
	if contextLimit <= 0 {
		return ProjectedContext{Confidence: ProjectionConfidenceLow}
	}

	// Estimate tool overhead (conservative: 5% of prompt or 2K, whichever is larger)
	toolOverhead := estimatedPrompt / 20
	if toolOverhead < 2000 {
		toolOverhead = 2000
	}

	// Safety margin: 5% of context limit
	safetyMargin := int64(float64(contextLimit) * 0.05)

	totalInput := estimatedPrompt + toolOverhead
	projectedTotal := currentTokens + totalInput + outputReserve + safetyMargin

	projectedPct := float64(projectedTotal) / float64(contextLimit) * 100.0

	// Confidence is high when we have current context data
	confidence := ProjectionConfidenceHigh
	if currentTokens <= 0 {
		confidence = ProjectionConfidenceLow
	}

	return ProjectedContext{
		CurrentTokens:    currentTokens,
		EstimatedPrompt:  estimatedPrompt,
		ToolOverhead:     toolOverhead,
		OutputReserve:    outputReserve,
		SafetyMargin:     safetyMargin,
		ProjectedTotal:   projectedTotal,
		ContextLimit:     contextLimit,
		ProjectedPercent: math.Round(projectedPct*10) / 10,
		Confidence:       confidence,
	}
}

// ============================================================
// Cache analysis
// ============================================================

// CacheSample is a single observation for the rolling window.
type CacheSample struct {
	FreshInputTokens         int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
}

// CacheWindowResult is the output of rolling window analysis.
type CacheWindowResult struct {
	RequestSamples     int
	TotalObservedInput int64
	CacheReadShare     float64
	CacheCreationShare float64
	FreshInputShare    float64
	Trend              string
}

// AnalyzeCacheWindow computes cache metrics from a rolling window of samples.
// Returns nil if there are insufficient samples or context.
func AnalyzeCacheWindow(samples []CacheSample, prevTrend string) *CacheWindowResult {
	if len(samples) < MinCacheAnalysisRequests {
		return &CacheWindowResult{
			RequestSamples: len(samples),
			Trend:          schema.TrendInsufficientData,
		}
	}

	var totalFresh, totalRead, totalCreate int64
	for _, s := range samples {
		totalFresh += s.FreshInputTokens
		totalRead += s.CacheReadInputTokens
		totalCreate += s.CacheCreationInputTokens
	}

	totalInput := totalFresh + totalRead + totalCreate
	if totalInput < MinCacheAnalysisContext {
		return &CacheWindowResult{
			RequestSamples:     len(samples),
			TotalObservedInput: totalInput,
			Trend:              schema.TrendInsufficientData,
		}
	}

	readShare := float64(totalRead) / float64(totalInput)
	createShare := float64(totalCreate) / float64(totalInput)
	freshShare := float64(totalFresh) / float64(totalInput)

	// Determine trend (simple: compare to previous)
	trend := prevTrend
	if prevTrend == "" || prevTrend == schema.TrendInsufficientData {
		trend = schema.TrendStable
	}

	return &CacheWindowResult{
		RequestSamples:     len(samples),
		TotalObservedInput: totalInput,
		CacheReadShare:     readShare,
		CacheCreationShare: createShare,
		FreshInputShare:    freshShare,
		Trend:              trend,
	}
}

// ShouldWarnCache checks if a cache warning should be issued based on the analysis.
func ShouldWarnCache(result *CacheWindowResult) bool {
	if result == nil {
		return false
	}
	if result.RequestSamples < MinCacheAnalysisRequests {
		return false
	}
	if result.TotalObservedInput < MinCacheAnalysisContext {
		return false
	}
	return result.CacheReadShare < LowCacheReuseThreshold
}

// ShouldResolveCacheWarning checks if a prior cache warning should be resolved.
func ShouldResolveCacheWarning(result *CacheWindowResult) bool {
	if result == nil {
		return false
	}
	if result.RequestSamples < CacheRecoveryRequests {
		return false
	}
	return result.CacheReadShare >= CacheRecoveryThreshold
}

// ============================================================
// Warning deduplication
// ============================================================

// WarningDedup manages cooldown-based warning suppression.
type WarningDedup struct {
	ContextWarningLastShown time.Time
	CacheWarningLastShown   time.Time
}

// ShouldShowContextWarning returns true if the context warning should be shown (respects cooldown).
func (wd *WarningDedup) ShouldShowContextWarning(now time.Time) bool {
	return now.Sub(wd.ContextWarningLastShown) >= ContextWarningCooldown
}

// ShouldShowCacheWarning returns true if the cache warning should be shown (respects cooldown).
func (wd *WarningDedup) ShouldShowCacheWarning(now time.Time) bool {
	return now.Sub(wd.CacheWarningLastShown) >= CacheWarningCooldown
}

// MarkContextWarningShown records that a context warning was shown.
func (wd *WarningDedup) MarkContextWarningShown(now time.Time) {
	wd.ContextWarningLastShown = now
}

// MarkCacheWarningShown records that a cache warning was shown.
func (wd *WarningDedup) MarkCacheWarningShown(now time.Time) {
	wd.CacheWarningLastShown = now
}

// ============================================================
// Compaction measurement
// ============================================================

// ComputeCompactionReduction calculates the reduction from compaction.
func ComputeCompactionReduction(preTokens, postTokens int64) (reductionPct float64) {
	if preTokens <= 0 {
		return 0
	}
	return math.Round(float64(preTokens-postTokens)/float64(preTokens)*100*10) / 10
}

// ============================================================
// Warning message generation
// ============================================================

// BuildContextWarning creates a user-facing context warning message.
func BuildContextWarning(usedPct, projectedPct float64, modelName string, contextLimit int64) string {
	if projectedPct > 0 && projectedPct > usedPct {
		return fmtWarning("FreeInference: projected context is %.0f%% (%.0f%% current) on %s (%s limit). Consider compacting or starting a fresh session.",
			projectedPct, usedPct, modelName, formatContextLimit(contextLimit))
	}
	return fmtWarning("FreeInference: context usage is %.0f%% on %s (%s limit). Consider compacting or starting a fresh session.",
		usedPct, modelName, formatContextLimit(contextLimit))
}

// BuildCacheWarning creates a user-facing cache warning message.
func BuildCacheWarning(readShare float64, requestCount int) string {
	pct := int(readShare * 100)
	return fmtWarning("FreeInference: only %d%% of observed input tokens were cache reads over the last %d requests. Large uncached sessions increase service load.",
		pct, requestCount)
}

// BuildProjectedOverflowWarning creates a warning for near-overflow situations.
func BuildProjectedOverflowWarning(proj ProjectedContext) string {
	return fmtWarning("FreeInference: this request is projected to use %.0f%% of the %s context window. Insufficient output reserve may remain.",
		proj.ProjectedPercent, formatContextLimit(proj.ContextLimit))
}

func fmtWarning(format string, args ...interface{}) string {
	msg := fmt.Sprintf(format, args...)
	return msg
}

// formatContextLimit formats a context limit in a human-readable way.
func formatContextLimit(limit int64) string {
	if limit >= 1000000 {
		return fmt.Sprintf("%dM", limit/1000000)
	}
	if limit >= 1000 {
		return fmt.Sprintf("%dK", limit/1000)
	}
	return fmt.Sprintf("%d", limit)
}