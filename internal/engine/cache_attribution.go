package engine

import (
	"fmt"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ============================================================
// Cache miss pattern attribution
// ============================================================

const (
	// Attribution thresholds. These classify WHY cache performance is poor
	// by examining the ratio of cache_creation vs fresh_input vs cache_read
	// across the observation window.

	// CacheCreationHighThreshold: when cache_creation share exceeds this,
	// the prefix is being rewritten frequently (thrashing).
	CacheCreationHighThreshold = 0.30

	// FreshInputDominantThreshold: when fresh_input share exceeds this AND
	// both cache_read and cache_creation are low, no caching is established.
	FreshInputDominantThreshold = 0.70

	// IntermittentMissVariance: when the read share swings wildly between
	// observations (high variance), the cache hits/misses are sporadic.
	// Variance on a 0-1 share scale: 0.06 means typical swings of ±0.25
	// around the mean, which is a clear good/bad alternation pattern.
	IntermittentMissVariance = 0.06
)

// CacheAttribution is the root-cause diagnosis for poor cache performance.
// Instead of saying "your read share is 15%", it says WHY and what to do.
type CacheAttribution struct {
	// Pattern is the classified cache miss pattern.
	Pattern CachePattern `json:"pattern"`
	// Diagnosis is a human-readable explanation of the likely cause.
	Diagnosis string `json:"diagnosis"`
	// Recommendation is the specific action the user should take.
	Recommendation string `json:"recommendation"`
	// Confidence is "low", "medium", or "high" based on sample size.
	Confidence string `json:"confidence"`
}

// CachePattern classifies the observed cache miss pattern.
type CachePattern string

const (
	// PatternNone: cache performance is acceptable; no attribution needed.
	PatternNone CachePattern = "none"
	// PatternThrashing: high cache_creation, low cache_read. The prefix
	// keeps being rewritten instead of reused.
	PatternThrashing CachePattern = "thrashing"
	// PatternNoCaching: high fresh_input, near-zero creation and read.
	// The client isn't benefiting from prompt caching at all.
	PatternNoCaching CachePattern = "no_caching"
	// PatternDecay: read share was good but is declining over time.
	// The growing conversation is pushing past the cached prefix.
	PatternDecay CachePattern = "decay"
	// PatternIntermittent: alternating good/bad observations. Some turns
	// hit the cache, others miss — likely tool calls breaking the prefix.
	PatternIntermittent CachePattern = "intermittent"
	// PatternInsufficientData: not enough observations to classify.
	PatternInsufficientData CachePattern = "insufficient_data"
)

// AttributeCacheMisses examines the observation sequence and classifies the
// cache miss pattern, producing a specific diagnosis and recommendation.
//
// This is the actionable layer on top of AnalyzeCache: AnalyzeCache computes
// the shares; this function explains what they mean and what to do.
func AttributeCacheMisses(snap *schema.Snapshot) CacheAttribution {
	if snap == nil || snap.CacheAnalysis == nil || len(snap.UsageObservations) < MinObservationsForWarning {
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
	// The prefix keeps being rewritten. Each request pays the creation
	// premium but never reuses it.
	if creationShare >= CacheCreationHighThreshold && readShare < CacheReadLowThreshold {
		return CacheAttribution{
			Pattern:        PatternThrashing,
			Confidence:     confidence,
			Diagnosis:      fmt.Sprintf("Cache is being rebuilt every request (creation %.0f%%, read %.0f%%). The prefix is unstable — something early in the context keeps changing.", creationShare*100, readShare*100),
			Recommendation: "Check for dynamic content at the start of your system prompt (timestamps, random IDs, changing file contents). Ensure tool definitions and system prompt are stable across turns. If you recently compacted, the cache will rebuild over the next few turns.",
		}
	}

	// Pattern 2: No caching established — fresh input dominates, both
	// creation and read are minimal. The client may not be using
	// cache_control breakpoints at all.
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
	// Some turns hit, some miss. Likely tool calls or variable content.
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
	return CacheAttribution{
		Pattern:        PatternThrashing,
		Confidence:     confidence,
		Diagnosis:      fmt.Sprintf("Cache read share is low (%.0f%%). Context is not being reused efficiently.", readShare*100),
		Recommendation: "Ensure your system prompt and early conversation turns are stable. Use `fi cache` for detailed metrics.",
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
