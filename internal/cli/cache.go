package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/engine"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// cmdCache implements `freeinference cache` — analyzes cache efficiency and gives
// actionable, zero-risk recommendations to improve cache hit rates.
func cmdCache(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	clientType, sessionID, _, reveal, jsonOut, err := parseClientSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	if clientType == schema.ClientCodex {
		return printCodexCacheUnavailable(stdout, jsonOut)
	}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	snap := (*schema.Snapshot)(nil)
	ca := (*schema.CacheAnalysis)(nil)
	lc := (*schema.LiveContext)(nil)
	if resolved != nil {
		snap = resolved.Snap
		if resolved.Client == schema.ClientCodex {
			return printCodexCacheUnavailable(stdout, jsonOut)
		}
		ca = snap.CacheAnalysis
		lc = snap.LiveContext
	}

	if jsonOut {
		cacheJSON(stdout, snap, ca, lc, resolved,
			func() string {
				if snap != nil {
					return displaySessionID(snap.Session.ID, reveal)
				}
				return "<none>"
			}(),
			reveal,
		)
		return 0
	}

	fmt.Fprintf(stdout, "Cache Analysis for %s (%s)\n",
		func() string {
			if snap != nil {
				return displaySessionID(snap.Session.ID, reveal)
			}
			return "<none>"
		}(),
		func() string {
			if resolved != nil {
				return resolved.Client
			}
			return "unknown"
		}(),
	)
	fmt.Fprintln(stdout, strings.Repeat("-", 60))

	if ca == nil || ca.ObservationCount == 0 && ca.RequestSamples == 0 {
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

	fmt.Fprintf(stdout, "Observed:    %d retained requests\n", ca.ObservationCount)
	fmt.Fprintf(stdout, "Analyzed:    %d recent requests\n", ca.AnalysisWindowCount)
	fmt.Fprintf(stdout, "Usable:      %d requests\n", ca.UsableSampleCount)
	fmt.Fprintf(stdout, "Availability: %s\n", ca.Availability)
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

	// Structured diagnosis using BuildCacheDiagnosis (Finding 4).
	diag := engine.BuildCacheDiagnosis(snap, time.Now())

	fmt.Fprintln(stdout, "Diagnosis:")
	if diag.Kind == schema.AttributionUnknown {
		fmt.Fprintf(stdout, "  info: %s\n", "Not enough cache observations yet to diagnose patterns.")
		if len(diag.Evidence) > 0 {
			for _, e := range diag.Evidence {
				fmt.Fprintf(stdout, "     (%s)\n", e.Description)
			}
		}
	} else {
		switch diag.Kind {
		case schema.AttributionHeuristic:
			fmt.Fprintf(stdout, "  Likely diagnosis (heuristic):\n")
		case schema.AttributionClientObserved:
			fmt.Fprintf(stdout, "  Client-observed:\n")
		case schema.AttributionProviderConfirmed:
			fmt.Fprintf(stdout, "  Provider-confirmed:\n")
		}
		fmt.Fprintf(stdout, "     Cache read: %.0f%% over %d usable samples\n", readPct, ca.UsableSampleCount)

		if len(diag.CandidateCauses) > 0 {
			top := diag.CandidateCauses[0]
			fmt.Fprintf(stdout, "     Possible cause: %s (heuristic score: %.0f%%)\n", top.Label, top.HeuristicScore*100)
			if len(diag.CandidateCauses) > 1 {
				for _, cc := range diag.CandidateCauses[1:] {
					fmt.Fprintf(stdout, "     Also possible: %s (heuristic score: %.0f%%)\n", cc.Label, cc.HeuristicScore*100)
				}
			}
		}

		if len(diag.Evidence) > 0 {
			fmt.Fprintln(stdout, "     Evidence:")
			for _, e := range diag.Evidence {
				src := "observed"
				if e.Source == "inferred" {
					src = "inferred"
				}
				if e.Value != "" {
					fmt.Fprintf(stdout, "       - %s (%s: %s)\n", e.Description, src, e.Value)
				} else {
					fmt.Fprintf(stdout, "       - %s (%s)\n", e.Description, src)
				}
			}
		}

		if len(diag.MissingEvidence) > 0 {
			fmt.Fprintln(stdout, "     Missing evidence (provider data):")
			for _, m := range diag.MissingEvidence {
				fmt.Fprintf(stdout, "       - %s\n", m)
			}
		}

		fmt.Fprintf(stdout, "     Confidence: %.0f%%\n", diag.Confidence*100)
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

func printCodexCacheUnavailable(stdout io.Writer, jsonOut bool) int {
	if jsonOut {
		fmt.Fprintln(stdout, `{"client":"codex","availability":"unavailable","reason":"client_telemetry_unavailable","cache":null}`)
		return 0
	}
	fmt.Fprintln(stdout, "Cache telemetry: unavailable")
	fmt.Fprintln(stdout, "Reason: Codex does not expose per-request cache usage to FreeInference Companion.")
	return 0
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

// cacheJSON emits a JSON representation of cache analysis to stdout.
func cacheJSON(stdout io.Writer, snap *schema.Snapshot, ca *schema.CacheAnalysis,
	lc *schema.LiveContext, resolved *resolvedSession, sessionID string, reveal bool) {
	obj := map[string]any{
		"session": sessionID,
	}
	if resolved != nil {
		obj["client"] = resolved.Client
	}

	if ca != nil {
		cacheObj := map[string]any{
			"observed_samples": ca.ObservationCount,
			"analyzed_samples": ca.AnalysisWindowCount,
			"usable_samples":   ca.UsableSampleCount,
			"availability":     ca.Availability,
			"trend":            ca.Trend,
		}
		if ca.ObservationCount == 0 {
			cacheObj["observed_samples"] = ca.RequestSamples
		}
		if ca.CacheReadShare != nil {
			cacheObj["read_share"] = *ca.CacheReadShare
		}
		if ca.CacheCreationShare != nil {
			cacheObj["creation_share"] = *ca.CacheCreationShare
		}
		if ca.FreshInputShare != nil {
			cacheObj["fresh_share"] = *ca.FreshInputShare
		}
		obj["cache"] = cacheObj
	}

	if lc != nil && lc.LatestRequest != nil {
		lr := lc.LatestRequest
		req := map[string]any{}
		if lr.FreshInputTokens != nil {
			req["fresh_input_tokens"] = *lr.FreshInputTokens
		}
		if lr.CacheReadInputTokens != nil {
			req["cache_read_input_tokens"] = *lr.CacheReadInputTokens
		}
		if lr.CacheCreationInputTokens != nil {
			req["cache_creation_input_tokens"] = *lr.CacheCreationInputTokens
		}
		if lr.OutputTokens != nil {
			req["output_tokens"] = *lr.OutputTokens
		}
		obj["latest_request"] = req
	}

	if lc != nil && lc.UsedPercentage != nil {
		obj["context_used_pct"] = *lc.UsedPercentage
	}

	if lc != nil {
		dd := engine.BuildCacheDiagnosis(snap, time.Now())
		diagObj := map[string]any{
			"kind":              string(dd.Kind),
			"reason_code":       string(dd.ReasonCode),
			"confidence":        dd.Confidence,
			"algorithm_version": dd.AlgorithmVersion,
		}
		if len(dd.CandidateCauses) > 0 {
			causes := make([]map[string]any, len(dd.CandidateCauses))
			for i, cc := range dd.CandidateCauses {
				causes[i] = map[string]any{
					"reason":          string(cc.Reason),
					"label":           cc.Label,
					"heuristic_score": cc.HeuristicScore,
				}
			}
			diagObj["candidate_causes"] = causes
		}
		if len(dd.Evidence) > 0 {
			evidence := make([]map[string]any, len(dd.Evidence))
			for i, e := range dd.Evidence {
				evidence[i] = map[string]any{
					"description": e.Description,
					"value":       e.Value,
					"source":      e.Source,
				}
			}
			diagObj["evidence"] = evidence
		}
		if len(dd.MissingEvidence) > 0 {
			diagObj["missing_evidence"] = dd.MissingEvidence
		}
		obj["diagnosis"] = diagObj
	}

	if snap != nil && snap.Pressure.State != "" {
		obj["pressure"] = snap.Pressure.State
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.Encode(obj)
}
