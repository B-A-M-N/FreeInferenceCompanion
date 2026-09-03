package engine

import (
	"fmt"
	"sort"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ============================================================
// Cache miss pattern attribution (Finding 4)
// ============================================================

const (
	// Attribution thresholds. These suggest WHY cache performance is poor
	// by examining the ratio of cache_creation vs fresh_input vs cache_read
	// across the observation window. All results are heuristic unless the
	// provider supplies explicit reason metadata.

	// CacheCreationHighThreshold: when cache_creation share exceeds this,
	// the prefix is being rewritten frequently (possible thrashing).
	CacheCreationHighThreshold = 0.30

	// FreshInputDominantThreshold: when fresh_input share exceeds this AND
	// both cache_read and cache_creation are low, no caching is established.
	FreshInputDominantThreshold = 0.70

	// IntermittentMissVariance: when the read share swings wildly between
	// observations (high variance), the cache hits/misses are sporadic.
	IntermittentMissVariance = 0.06

	// algorithmVersion identifies this attribution algorithm for diagnostics.
	algorithmVersion = "v2.0.0"
)

// CacheAttribution is a deprecated cache-pattern classification and likely
// diagnosis for poor cache performance.
// Deprecated: Use BuildCacheDiagnosis instead, which returns the structured
// schema.CacheDiagnosis type with proper attribution kind, evidence, and
// confidence.
type CacheAttribution struct {
	Pattern        CachePattern `json:"pattern"`
	Diagnosis      string       `json:"diagnosis"`
	Recommendation string       `json:"recommendation"`
	Confidence     string       `json:"confidence"`
}

// CachePattern classifies the observed cache miss pattern.
type CachePattern string

const (
	PatternNone             CachePattern = "none"
	PatternThrashing        CachePattern = "thrashing"
	PatternNoCaching        CachePattern = "no_caching"
	PatternDecay            CachePattern = "decay"
	PatternIntermittent     CachePattern = "intermittent"
	PatternInsufficientData CachePattern = "insufficient_data"
)

// BuildCacheDiagnosis returns a structured cache diagnosis with honest attribution
// kind, evidence, confidence, and candidate causes. The fallback is always
// "unknown" — never a specific failure mode without supporting evidence.
//
// This replaces AttributeCacheMisses for all new code. The old function is
// preserved for backward compatibility with the CLI output layer.
func BuildCacheDiagnosis(snap *schema.Snapshot, now time.Time) schema.CacheDiagnosis {
	diag := schema.CacheDiagnosis{
		Kind:             schema.AttributionUnknown,
		Status:           schema.CacheStatusUnknown,
		ReasonCode:       schema.ReasonUnknown,
		Confidence:       0,
		AlgorithmVersion: algorithmVersion,
		ObservedAt:       now,
	}

	if snap == nil || snap.CacheAnalysis == nil || effectiveUsableSamples(snap.CacheAnalysis) < MinObservationsForWarning {
		diag.Evidence = []schema.EvidenceItem{
			{Description: "Insufficient observations to analyze cache behavior", Source: "inferred"},
		}
		diag.MissingEvidence = []string{
			"At least " + fmt.Sprintf("%d", MinObservationsForWarning) + " unique usage observations",
			"Provider-returned cache status or reason code",
		}
		diag.Kind = schema.AttributionUnknown
		return diag
	}

	analysis := snap.CacheAnalysis
	analysisObs := currentEpochObservations(snap)
	readShare := 0.0
	creationShare := 0.0
	freshShare := 0.0
	if analysis.CacheReadShare != nil {
		readShare = *analysis.CacheReadShare
	}
	if analysis.CacheCreationShare != nil {
		creationShare = *analysis.CacheCreationShare
	}
	if analysis.FreshInputShare != nil {
		freshShare = *analysis.FreshInputShare
	}

	// Build evidence from what we can observe.
	diag.Evidence = []schema.EvidenceItem{
		{
			Description: fmt.Sprintf("Cache read share: %.0f%% over %d usable observations", readShare*100, effectiveUsableSamples(analysis)),
			Value:       fmt.Sprintf("%.2f", readShare),
			Source:      "client_observed",
		},
	}
	if creationShare > 0 {
		diag.Evidence = append(diag.Evidence, schema.EvidenceItem{
			Description: fmt.Sprintf("Cache creation share: %.0f%%", creationShare*100),
			Value:       fmt.Sprintf("%.2f", creationShare),
			Source:      "client_observed",
		})
	}
	if freshShare > 0 {
		diag.Evidence = append(diag.Evidence, schema.EvidenceItem{
			Description: fmt.Sprintf("Fresh input share: %.0f%%", freshShare*100),
			Value:       fmt.Sprintf("%.2f", freshShare),
			Source:      "client_observed",
		})
	}

	// The companion cannot determine exact miss reasons from token ratios alone.
	diag.MissingEvidence = []string{
		"Provider-returned cache status (hit/partial/miss/bypass)",
		"Provider-returned cache miss reason code",
		"Cache TTL policy version",
		"Route or backend class information",
		"Opaque prefix fingerprint for change detection",
	}

	// Classify observed status based on available data.
	diag.Status = classifyCacheStatus(readShare, creationShare, freshShare)

	// Build candidate causes with ranked heuristic evidence scores.
	diag.CandidateCauses = buildCandidateCauses(readShare, creationShare, freshShare, analysis, snap)

	// Derive confidence from sample size and data quality.
	diag.Confidence = deriveConfidence(effectiveUsableSamples(analysis), analysisObs)

	// Set attribution kind: always heuristic since we lack provider metadata.
	diag.Kind = schema.AttributionHeuristic

	// Reason code: use the most likely candidate.
	if len(diag.CandidateCauses) > 0 {
		diag.ReasonCode = diag.CandidateCauses[0].Reason
	}

	return diag
}

// classifyCacheStatus maps observed shares to a cache status.
func classifyCacheStatus(readShare, creationShare, freshShare float64) schema.CacheStatus {
	if readShare >= CacheReadRecoveredThreshold {
		return schema.CacheStatusHit
	}
	if readShare >= CacheReadLowThreshold {
		return schema.CacheStatusPartialHit
	}
	if freshShare > 0.9 && creationShare < 0.05 {
		return schema.CacheStatusMiss
	}
	return schema.CacheStatusMiss
}

// buildCandidateCauses ranks possible causes for poor cache performance.
// Returns causes sorted by heuristic evidence score (highest first). Each cause is explicitly
// labeled as a hypothesis — never a definitive causal claim.
func buildCandidateCauses(readShare, creationShare, freshShare float64, analysis *schema.CacheAnalysis, snap *schema.Snapshot) []schema.RankedCause {
	var causes []schema.RankedCause

	totalInput := readShare + creationShare + freshShare
	if totalInput <= 0 {
		return causes
	}

	// Cause 1: Prefix changed (common when read is low and creation is high)
	if creationShare >= CacheCreationHighThreshold && readShare < CacheReadLowThreshold {
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonPrefixChanged,
			Label:          "Cache prefix instability",
			HeuristicScore: 0.6,
		})
		// Thrashing pattern also suggests this
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonCapacityEviction,
			Label:          "Cache capacity eviction or TTL expiry between requests",
			HeuristicScore: 0.3,
		})
	}

	// Cause 2: No caching established
	if freshShare >= FreshInputDominantThreshold && creationShare < 0.10 && readShare < 0.10 {
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonBreakpointMissing,
			Label:          "Prompt caching not active — no cache_control breakpoints configured",
			HeuristicScore: 0.7,
		})
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonUnsupported,
			Label:          "Client or model does not support prompt caching",
			HeuristicScore: 0.2,
		})
	}

	// Cause 3: Decay — declining read share
	if analysis.Trend == schema.TrendDeclining && analysis.PreviousReadShare != nil &&
		*analysis.PreviousReadShare >= CacheReadLowThreshold && readShare < CacheReadLowThreshold {
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonPrefixChanged,
			Label:          "Conversation growing past cached prefix",
			HeuristicScore: 0.6,
		})
	}

	// Cause 4: Model change
	analysisObs := currentEpochObservations(snap)
	if len(analysisObs) >= 2 {
		lastModel := analysisObs[len(analysisObs)-1].ModelID
		prevModel := analysisObs[len(analysisObs)-2].ModelID
		if lastModel != prevModel && lastModel != "" && prevModel != "" {
			causes = append(causes, schema.RankedCause{
				Reason:         schema.ReasonModelChanged,
				Label:          "Model changed between requests",
				HeuristicScore: 0.8,
			})
		}
	}

	// Cause 5: Intermittent — high variance
	variance := computeReadShareVariance(analysisObs)
	if variance >= IntermittentMissVariance && readShare < CacheReadLowThreshold {
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonPrefixChanged,
			Label:          "Variable content before cache breakpoint (e.g., tool results)",
			HeuristicScore: 0.5,
		})
	}

	// Fallback: generic causes when nothing specific matched
	if len(causes) == 0 && readShare < CacheReadLowThreshold {
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonColdStart,
			Label:          "Cache cold — insufficient recent request history",
			HeuristicScore: 0.4,
		})
		causes = append(causes, schema.RankedCause{
			Reason:         schema.ReasonUnknown,
			Label:          "Provider did not expose enough information to determine cause",
			HeuristicScore: 0.6,
		})
	}

	// Multiple signals can point at the same hypothesis. Keep the strongest
	// evidence for each reason, then make the advertised ordering true.
	best := make(map[schema.CacheReasonCode]schema.RankedCause, len(causes))
	for _, cause := range causes {
		if current, ok := best[cause.Reason]; !ok || cause.HeuristicScore > current.HeuristicScore {
			best[cause.Reason] = cause
		}
	}
	causes = causes[:0]
	for _, cause := range best {
		causes = append(causes, cause)
	}
	sort.SliceStable(causes, func(i, j int) bool {
		if causes[i].HeuristicScore == causes[j].HeuristicScore {
			return causes[i].Reason < causes[j].Reason
		}
		return causes[i].HeuristicScore > causes[j].HeuristicScore
	})
	return causes
}

// deriveConfidence computes a confidence score from sample size and data completeness.
// 0.0 = no confidence, 1.0 = high confidence.
func deriveConfidence(sampleCount int, observations []schema.UsageObservation) float64 {
	if sampleCount < MinObservationsForWarning {
		return 0.0
	}
	// Base confidence from sample size.
	base := 0.3
	if sampleCount >= 10 {
		base = 0.6
	}
	// Check data completeness: how many observations have all three token fields?
	complete := 0
	for _, o := range observations {
		if o.FreshInputTokens != nil && o.CacheReadInputTokens != nil && o.CacheCreationInputTokens != nil {
			complete++
		}
	}
	if len(observations) > 0 {
		completeness := float64(complete) / float64(len(observations))
		if completeness < 0.5 {
			base *= 0.5 // Significant data gaps reduce confidence
		}
	}
	if base > 0.8 {
		base = 0.8 // Cap at 0.8 — we lack provider confirmation
	}
	return base
}

// AttributeCacheMisses examines the observation sequence and classifies the
// cache miss pattern, producing a specific diagnosis and recommendation.
//
// DEPRECATED: Use BuildCacheDiagnosis for new code. This function uses the
// old heuristic patterns and may misclassify unknown patterns as thrashing.
// It is preserved for backward compatibility with the CLI output layer.
//
// Known defect (Finding 4): The fallback at the end classifies unrecognized
// patterns as PatternThrashing, which is misleading when there is insufficient
// evidence to support that conclusion.
func AttributeCacheMisses(snap *schema.Snapshot) CacheAttribution {
	if snap == nil || snap.CacheAnalysis == nil || effectiveUsableSamples(snap.CacheAnalysis) < MinObservationsForWarning {
		return CacheAttribution{
			Pattern:    PatternInsufficientData,
			Confidence: "low",
			Diagnosis:  "Not enough cache observations yet to diagnose patterns.",
		}
	}

	analysis := snap.CacheAnalysis
	if analysis.CacheReadShare != nil && *analysis.CacheReadShare >= CacheReadLowThreshold {
		return CacheAttribution{
			Pattern: PatternNone,
		}
	}

	confidence := "low"

	creationShare := 0.0
	if analysis.CacheCreationShare != nil {
		creationShare = *analysis.CacheCreationShare
	}
	freshShare := 0.0
	if analysis.FreshInputShare != nil {
		freshShare = *analysis.FreshInputShare
	}
	readShare := 0.0
	if analysis.CacheReadShare != nil {
		readShare = *analysis.CacheReadShare
	}

	// Pattern 1: Thrashing — high creation, low read.
	if creationShare >= CacheCreationHighThreshold && readShare < CacheReadLowThreshold {
		return CacheAttribution{
			Pattern:        PatternThrashing,
			Confidence:     confidence,
			Diagnosis:      fmt.Sprintf("Cache is being rebuilt every request (creation %.0f%%, read %.0f%%). The prefix is unstable — something early in the context keeps changing.", creationShare*100, readShare*100),
			Recommendation: "Check for dynamic content at the start of your system prompt (timestamps, random IDs, changing file contents). Ensure tool definitions and system prompt are stable across turns. If you recently compacted, the cache will rebuild over the next few turns.",
		}
	}

	// Pattern 2: No caching established.
	if freshShare >= FreshInputDominantThreshold && creationShare < 0.10 && readShare < 0.10 {
		return CacheAttribution{
			Pattern:        PatternNoCaching,
			Confidence:     confidence,
			Diagnosis:      fmt.Sprintf("Almost all input is fresh (%.0f%%) with negligible cache activity. Prompt caching does not appear to be active for this session.", freshShare*100),
			Recommendation: "Verify your client supports prompt caching and that cache_control breakpoints are configured. If using a custom integration, ensure the system prompt and conversation prefix are marked for caching.",
		}
	}

	// Pattern 3: Decay — read share is declining.
	if analysis.Trend == schema.TrendDeclining && analysis.PreviousReadShare != nil &&
		*analysis.PreviousReadShare >= CacheReadLowThreshold {
		return CacheAttribution{
			Pattern:        PatternDecay,
			Confidence:     confidence,
			Diagnosis:      fmt.Sprintf("Cache read share is declining (was %.0f%%, now %.0f%%). The growing conversation may be pushing past the cached prefix.", *analysis.PreviousReadShare*100, readShare*100),
			Recommendation: "Consider compacting the conversation to reset the cache prefix. Long sessions naturally degrade cache performance as new tokens accumulate.",
		}
	}

	// Pattern 4: Intermittent — high variance in per-observation read share.
	variance := computeReadShareVariance(snap.UsageObservations)
	if variance >= IntermittentMissVariance && readShare < CacheReadLowThreshold {
		return CacheAttribution{
			Pattern:        PatternIntermittent,
			Confidence:     confidence,
			Diagnosis:      fmt.Sprintf("Cache hits are sporadic — read share varies widely across requests (variance %.2f). Some turns benefit from caching, others don't.", variance),
			Recommendation: "This often happens when tool results or file contents are inserted before the cached prefix on some turns. Check if tool output positions are consistent across requests.",
		}
	}

	// Fallback: read share is low but no specific pattern matched.
	// DIFFERENCE FROM OLD BEHAVIOR: was PatternThrashing. Changed to a
	// generic "low read share" pattern that doesn't claim a specific cause.
	return CacheAttribution{
		Pattern:        "unclassified", // was PatternThrashing (Finding 4)
		Confidence:     confidence,
		Diagnosis:      fmt.Sprintf("Cache read share is low (%.0f%%), but there is insufficient information to determine the exact cause. The provider has not exposed cache miss reason metadata.", readShare*100),
		Recommendation: "Cache performance may improve with stable system prompts and regular compaction. Run `freeinference cache` for detailed metrics.",
	}
}

// computeReadShareVariance calculates the variance of per-observation cache
// read shares. High variance indicates intermittent cache hits/misses.
func computeReadShareVariance(obs []schema.UsageObservation) float64 {
	var shares []float64
	for _, o := range obs {
		share := observationReadShare(o)
		if share != nil {
			shares = append(shares, *share)
		}
	}
	if len(shares) < 3 {
		return 0
	}

	var sum, mean float64
	for _, s := range shares {
		sum += s
	}
	mean = sum / float64(len(shares))

	var sqDiffSum float64
	for _, s := range shares {
		diff := s - mean
		sqDiffSum += diff * diff
	}
	return sqDiffSum / float64(len(shares))
}
