package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/engine"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// cmdCache implements `fi cache` — analyzes cache efficiency and gives
// actionable, zero-risk recommendations to improve cache hit rates.
func cmdCache(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	clientType, sessionID, _, reveal, err := parseClientSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resolved == nil {
		fmt.Fprintln(stdout, "FI: no session")
		return 0
	}

	snap := resolved.Snap
	ca := snap.CacheAnalysis
	lc := snap.LiveContext

	fmt.Fprintf(stdout, "Cache Analysis for %s (%s)\n", displaySessionID(snap.Session.ID, reveal), snap.Client.Type)
	fmt.Fprintln(stdout, strings.Repeat("-", 60))

	if ca == nil || ca.RequestSamples == 0 {
		fmt.Fprintln(stdout, "No cache data yet. Send a few requests first.")
		fmt.Fprintln(stdout)
		printCacheBasics(stdout, lc)
		return 0
	}

	// Current metrics
	readPct := 0.0
	createPct := 0.0
	freshPct := 0.0
	if ca.CacheReadShare != nil {
		readPct = *ca.CacheReadShare * 100
	}
	if ca.CacheCreationShare != nil {
		createPct = *ca.CacheCreationShare * 100
	}
	if ca.FreshInputShare != nil {
		freshPct = *ca.FreshInputShare * 100
	}

	fmt.Fprintf(stdout, "Samples:     %d unique requests\n", ca.RequestSamples)
	fmt.Fprintf(stdout, "Cache Read:  %.1f%%\n", readPct)
	fmt.Fprintf(stdout, "Cache New:   %.1f%%\n", createPct)
	fmt.Fprintf(stdout, "Fresh Input: %.1f%%\n", freshPct)
	fmt.Fprintf(stdout, "Trend:       %s\n", ca.Trend)
	fmt.Fprintln(stdout)

	// Latest request breakdown
	if lc != nil && lc.LatestRequest != nil {
		lr := lc.LatestRequest
		fmt.Fprintln(stdout, "Latest Request:")
		fmt.Fprintf(stdout, "  Fresh:      %s\n", formatTokenPtr(lr.FreshInputTokens))
		fmt.Fprintf(stdout, "  Cache Read: %s\n", formatTokenPtr(lr.CacheReadInputTokens))
		fmt.Fprintf(stdout, "  Cache New:  %s\n", formatTokenPtr(lr.CacheCreationInputTokens))
		fmt.Fprintf(stdout, "  Output:     %s\n", formatTokenPtr(lr.OutputTokens))
		fmt.Fprintln(stdout)
	}

	// Pattern attribution — root-cause diagnosis instead of generic heuristics.
	attribution := engine.AttributeCacheMisses(snap)

	fmt.Fprintln(stdout, "Diagnosis:")
	switch attribution.Pattern {
	case engine.PatternNone:
		fmt.Fprintln(stdout, "  ✅ Cache efficiency looks good. Keep current patterns.")
	case engine.PatternInsufficientData:
		fmt.Fprintf(stdout, "  ℹ️  %s\n", attribution.Diagnosis)
	default:
		patternLabel := patternLabel(attribution.Pattern)
		fmt.Fprintf(stdout, "  %s %s\n", patternLabel, attribution.Diagnosis)
		if attribution.Recommendation != "" {
			fmt.Fprintf(stdout, "     → %s\n", attribution.Recommendation)
		}
		fmt.Fprintf(stdout, "     (confidence: %s)\n", attribution.Confidence)
	}

	// Context pressure advisory (supplements the cache diagnosis).
	if lc != nil && lc.UsedPercentage != nil && *lc.UsedPercentage > 70 {
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "  🟡 CONTEXT PRESSURE: %.0f%% used\n", *lc.UsedPercentage)
		fmt.Fprintln(stdout, "     → Compact earlier — for Claude Code use /compact; for other clients use their compaction command")
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Zero-risk tactics:")
	fmt.Fprintln(stdout, "  1. Fixed prefix: Put system prompt + few-shots + static docs FIRST")
	fmt.Fprintln(stdout, "  2. Session reuse: Continue same session (don't 'new chat' for related tasks)")
	fmt.Fprintln(stdout, "  3. Batch related: Group similar queries in one session")
	fmt.Fprintln(stdout, "  4. Compact early: Use your client's compaction command at 60-70%, not 90%")

	return 0
}

// patternLabel returns the icon+label for a cache pattern in the diagnosis.
func patternLabel(p engine.CachePattern) string {
	switch p {
	case engine.PatternThrashing:
		return "🔴 THRASHING:"
	case engine.PatternNoCaching:
		return "🔴 NO CACHING:"
	case engine.PatternDecay:
		return "🟡 DECAYING:"
	case engine.PatternIntermittent:
		return "🟡 INTERMITTENT:"
	default:
		return "🟡"
	}
}

func printCacheBasics(stdout io.Writer, lc *schema.LiveContext) {
	if lc == nil || lc.LatestRequest == nil {
		return
	}
	lr := lc.LatestRequest
	fmt.Fprintln(stdout, "Latest Request (single sample):")
	fmt.Fprintf(stdout, "  Fresh:      %s\n", formatTokenPtr(lr.FreshInputTokens))
	fmt.Fprintf(stdout, "  Cache Read: %s\n", formatTokenPtr(lr.CacheReadInputTokens))
	fmt.Fprintf(stdout, "  Cache New:  %s\n", formatTokenPtr(lr.CacheCreationInputTokens))
	fmt.Fprintf(stdout, "  Output:     %s\n", formatTokenPtr(lr.OutputTokens))
}
