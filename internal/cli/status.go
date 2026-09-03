package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/internal/engine"
	"github.com/b-a-m-n/freeinference-companion/internal/render"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// cmdStatus implements `freeinference status`.
func cmdStatus(paths state.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	compact := false
	jsonOut := false

	// Extract --color flag.
	colorMode, remainingArgs, err := parseColorFlag(args)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	_ = colorMode // used below via renderConfigWith

	// Extract --compact and --json from remainingArgs, and filter them out
	// so parseClientSessionFlags doesn't reject them as unknown.
	level := ""
	levelSpecified := false
	var passthroughArgs []string
	for i := 0; i < len(remainingArgs); i++ {
		a := remainingArgs[i]
		switch a {
		case "--compact":
			compact = true
		case "--json":
			jsonOut = true
		case "--level":
			if i+1 >= len(remainingArgs) {
				fmt.Fprintln(stderr, "usage error: --level requires a value (summary, standard, or detailed)")
				return 2
			}
			i++
			levelSpecified = true
			level = strings.ToLower(strings.TrimSpace(remainingArgs[i]))
		default:
			if strings.HasPrefix(a, "--level=") {
				levelSpecified = true
				level = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(a, "--level=")))
				continue
			}
			passthroughArgs = append(passthroughArgs, a)
		}
	}
	if levelSpecified && !config.ValidReportingLevel(level) {
		fmt.Fprintf(stderr, "usage error: unknown reporting level %q (want summary, standard, or detailed)\n", level)
		return 2
	}
	if compact && levelSpecified {
		fmt.Fprintln(stderr, "usage error: --compact and --level cannot be used together")
		return 2
	}
	if jsonOut && levelSpecified {
		fmt.Fprintln(stderr, "usage error: --json and --level cannot be used together")
		return 2
	}
	if !compact && !jsonOut && !levelSpecified {
		level = configuredReportingLevel()
	}
	clientType, sessionID, _, reveal, _, err := parseClientSessionFlags(passthroughArgs)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	historical := explicitSessionRequested(passthroughArgs)

	// P0-2/P0-3: activation gate — must be evaluated before any IO work.
	// For status-line mode (stdin has data), inactive means zero output.
	// For interactive mode, resolve the explicitly requested client so Codex
	// provider configuration is not lost by a second generic evaluation.
	activation := activationForCLICommand("status", passthroughArgs)
	if stdinHasData(stdin) {
		activation = runtime.EvaluateForClient(runtime.ClientClaudeCode)
	}

	// Status-line mode: a Claude status payload arrives on stdin. We MUST use
	// the session ID from that payload — never fall through to
	// resolveSession (which picks the most-recent active session and can show
	// a stale session from a different context). When inactive, output nothing.
	// Identity failure also means zero output (fail-closed).
	if stdinHasData(stdin) {
		var statusInput schema.ClaudeStatusLineInput
		if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&statusInput); err == nil && statusInput.SessionID != "" {
			// A status-line payload is Claude telemetry, so its gate must be
			// Claude-specific even when a generic FreeInference environment is
			// present for another client.
			activation = runtime.EvaluateForClient(runtime.ClientClaudeCode)
			// P0-3: inactive runtime → zero bytes in status-line mode
			if !activation.Active {
				return 0
			}
			// Identity failure → zero output (fail-closed for security)
			aid, err := activationID(activation)
			if err != nil {
				return 0
			}
			sessionID = statusInput.SessionID
			if clientType == "" {
				clientType = schema.ClientClaudeCode
			}
			_ = adapters.NewClaudeAdapter(paths).HandleStatusLineUpdateWithTrace(&statusInput, sessionID, activation, environmentTraceInfo(schema.ClientClaudeCode, activation))

			snap, err := state.LoadSnapshot(paths, schema.ClientClaudeCode, sessionID)
			if err != nil || snap == nil {
				// P0-3: missing snapshot → zero bytes, not "FI: no data"
				return 0
			}
			gs := loadGlobal(paths)
			vm := buildView(snap, gs, aid, activation.Active, clientType, sessionID)
			rc := renderConfigWith(args)
			if jsonOut {
				statusJSON(stdout, snap, gs, reveal, aid, &activation.Active,
					clientType, sessionID, snap.Model.ID, snap.Provider.Name)
				return 0
			}
			if compact {
				// Zero-output contract: write nothing when ineligible.
				// Never write a newline for an empty line.
				if line := vm.Line(rc); line != "" {
					fmt.Fprintln(stdout, line)
				}
			} else {
				if rendered := renderStatusLevel(vm, rc, level); rendered != "" {
					fmt.Fprintln(stdout, rendered)
				}
			}
			return 0
		}
	}

	// No usable stdin payload — in compact/status-line mode, output zero
	// bytes. An empty status line is correct when there is nothing to say.
	// In interactive mode (no --compact), fall through to resolveSession.
	if compact && !historical {
		return 0
	}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resolved == nil {
		if jsonOut {
			statusJSON(stdout, nil, loadGlobal(paths), reveal, "", nil, "", "", "", "")
			return 0
		}
		fmt.Fprintln(stdout, "FI: no session")
		return 0
	}

	gs := loadGlobal(paths)
	aid, err := activationID(activation)
	if err != nil {
		if historical && !activation.Disabled {
			if jsonOut {
				statusJSON(stdout, resolved.Snap, gs, reveal, "", &activation.Active,
					resolved.Client, resolved.Snap.Session.ID, resolved.Snap.Model.ID,
					resolved.Snap.Provider.Name, true)
				return 0
			}
			fmt.Fprintln(stdout, "Historical session — FreeInference is not currently active.")
			printFullStatus(stdout, resolved.Snap, gs, reveal, false)
			return 0
		}
		// Identity failure in interactive mode: report sanitized error
		fmt.Fprintf(stderr, "error: %s\n", secure.SanitizeField(err.Error()))
		return 1
	}
	vm := buildView(resolved.Snap, gs, aid, activation.Active, clientType, resolved.Snap.Session.ID)
	if historical && !vm.Eligible {
		if jsonOut {
			statusJSON(stdout, resolved.Snap, gs, reveal, aid, &activation.Active,
				resolved.Client, resolved.Snap.Session.ID, resolved.Snap.Model.ID,
				resolved.Snap.Provider.Name, true)
			return 0
		}
		fmt.Fprintln(stdout, "Historical session — not a current live surface.")
		printFullStatus(stdout, resolved.Snap, gs, reveal, false)
		return 0
	}
	rc := renderConfigWith(args)

	if compact {
		if line := vm.Line(rc); line != "" {
			fmt.Fprintln(stdout, line)
		}
		return 0
	}

	if jsonOut {
		statusJSON(stdout, resolved.Snap, gs, reveal, aid, &activation.Active,
			resolved.Client, resolved.Snap.Session.ID, resolved.Snap.Model.ID,
			resolved.Snap.Provider.Name)
		return 0
	}

	if rendered := renderStatusLevel(vm, rc, level); rendered != "" {
		fmt.Fprintln(stdout, rendered)
	}
	return 0
}

// configuredReportingLevel returns the saved default. A malformed external
// configuration never blocks a status check; it falls back to the established
// detailed output, while `freeinference config show` exposes the bad value.
func configuredReportingLevel() string {
	mgr, err := config.NewManager()
	if err != nil {
		return "detailed"
	}
	eff, err := mgr.Resolve()
	if err != nil || !eff.Reporting.Level.Valid || !config.ValidReportingLevel(eff.Reporting.Level.Value) {
		return "detailed"
	}
	return eff.Reporting.Level.Value
}

func renderStatusLevel(vm interface {
	Line(render.RenderConfig) string
	Standard(render.RenderConfig) string
	Expanded(render.RenderConfig) string
}, rc render.RenderConfig, level string) string {
	switch level {
	case "summary":
		return vm.Line(rc)
	case "standard":
		return vm.Standard(rc)
	default:
		return vm.Expanded(rc)
	}
}

// statusJSON emits a JSON representation of status to stdout.
func statusJSON(stdout io.Writer, snap *schema.Snapshot, gs *schema.GlobalState, reveal bool,
	activationID string, active *bool, client, sessionID, model, providerName string, historical ...bool) {
	var ctx map[string]any
	if client == string(schema.ClientCodex) || (snap != nil && snap.Client.Type == schema.ClientCodex) {
		ctx = map[string]any{
			"availability": "unavailable",
			"reason":       "client_telemetry_unavailable",
		}
	} else if snap != nil && snap.LiveContext != nil {
		lc := snap.LiveContext
		ctx = map[string]any{
			"used_pct":              lc.UsedPercentage,
			"source":                lc.Source,
			"total_token_semantics": lc.TotalTokenSemantics,
		}
		if lc.ContextWindowSize != nil {
			ctx["window_size"] = *lc.ContextWindowSize
		}
		if lc.TotalInputTokens != nil {
			ctx["total_input_tokens"] = *lc.TotalInputTokens
		}
		if lc.TotalOutputTokens != nil {
			ctx["total_output_tokens"] = *lc.TotalOutputTokens
		}
	}

	cacheObj := map[string]any{}
	if client == string(schema.ClientCodex) || (snap != nil && snap.Client.Type == schema.ClientCodex) {
		cacheObj["availability"] = "unavailable"
		cacheObj["reason"] = "client_telemetry_unavailable"
	} else if snap != nil && snap.CacheAnalysis != nil {
		ca := snap.CacheAnalysis
		cacheObj["observed_samples"] = ca.ObservationCount
		cacheObj["analyzed_samples"] = ca.AnalysisWindowCount
		cacheObj["usable_samples"] = ca.UsableSampleCount
		cacheObj["availability"] = ca.Availability
		if ca.ObservationCount == 0 {
			cacheObj["observed_samples"] = ca.RequestSamples
		}
		cacheObj["trend"] = ca.Trend
		if ca.CacheReadShare != nil {
			cacheObj["read_share"] = *ca.CacheReadShare
		}
		if ca.CacheCreationShare != nil {
			cacheObj["creation_share"] = *ca.CacheCreationShare
		}
		if ca.FreshInputShare != nil {
			cacheObj["fresh_share"] = *ca.FreshInputShare
		}
	}

	pressure := ""
	if snap != nil {
		pressure = snap.Pressure.State
	}

	var modelID string
	if model != "" {
		modelID = secure.SanitizeField(model)
	}
	var provName string
	if providerName != "" {
		provName = secure.SanitizeField(providerName)
	}
	if snap != nil && !snap.Provider.Confirmed {
		provName = "unknown (unconfirmed)"
	}

	sessionDisplay := ""
	if snap != nil {
		sessionDisplay = displaySessionID(snap.Session.ID, reveal)
	}

	obj := map[string]any{
		"session_id": sessionDisplay,
		"model":      modelID,
		"provider":   provName,
		"client":     client,
		"context":    ctx,
		"cache":      cacheObj,
		"pressure":   pressure,
	}
	if activationID != "" {
		obj["activation_id"] = activationID
	}
	if active != nil {
		obj["active"] = *active
	}
	activationMatches := snap != nil && (activationID == "" || snap.ActivationID == activationID)
	if active != nil && *active && activationMatches && snap.Provider.Confirmed && snap.Provider.Name == schema.ProviderFreeInference && gs != nil {
		now := time.Now().UTC()
		if gs.PublicStatus != nil {
			for _, metric := range gs.PublicStatus.Models {
				if metric.ModelID != snap.Model.ID {
					continue
				}
				monitor := map[string]any{
					"model":        secure.SanitizeField(metric.ModelID),
					"uptime_ratio": metric.UptimeRatio,
				}
				if metric.Latest != nil {
					age := now.Sub(metric.Latest.CheckedAt)
					monitor["ok"] = metric.Latest.OK
					monitor["checked_at"] = metric.Latest.CheckedAt.UTC().Format(time.RFC3339)
					monitor["age_seconds"] = max(0, int64(age.Seconds()))
					monitor["stale"] = age < 0 || age > 45*time.Minute
					monitor["latency_ms"] = metric.Latest.LatencyMs
					monitor["ttft_ms"] = metric.Latest.TTFTMs
					monitor["throughput_tps"] = metric.Latest.ThroughputTps
					if metric.Latest.Error != "" {
						monitor["error"] = secure.SanitizeField(metric.Latest.Error)
					}
				}
				obj["model_monitor"] = monitor
				break
			}
		}
		if gs.Health != nil {
			age := now.Sub(gs.Health.FetchedAt)
			obj["provider_health"] = map[string]any{
				"status":      secure.SanitizeField(gs.Health.Status),
				"checked_at":  gs.Health.FetchedAt.UTC().Format(time.RFC3339),
				"age_seconds": max(0, int64(age.Seconds())),
				"stale":       age < 0 || age > schema.DefaultHealthMaxAge,
			}
		}
		if gs.AccountUsage != nil && gs.AccountUsageCapability != nil &&
			gs.AccountUsageCapability.State == schema.CapabilitySupported && schema.ValidateAccountUsage(gs.AccountUsage) == nil {
			age := now.Sub(gs.AccountUsage.FetchedAt)
			usage := map[string]any{
				"fetched_at":  gs.AccountUsage.FetchedAt.UTC().Format(time.RFC3339),
				"age_seconds": max(0, int64(age.Seconds())),
				"stale":       age < 0 || age > schema.DefaultAccountUsageMaxAge,
			}
			if !usage["stale"].(bool) {
				usage["requests_used"] = gs.AccountUsage.RequestsUsed
				usage["requests_limit"] = gs.AccountUsage.RequestsLimit
				usage["tokens_used"] = gs.AccountUsage.TokensUsed
				usage["tokens_limit"] = gs.AccountUsage.TokensLimit
			}
			obj["account_usage"] = usage
		}
	}
	if len(historical) > 0 {
		reason := "historical_snapshot"
		if active != nil && !*active {
			reason = "runtime_not_active"
		}
		obj["historical"] = true
		obj["reason"] = reason
	}
	if active != nil && *active && snap != nil && snap.Trace != nil && snap.Trace.Enabled && snap.Trace.Verified && snap.Provider.Confirmed && snap.Provider.Name == schema.ProviderFreeInference &&
		snap.Trace.Provider == schema.ProviderFreeInference && (snap.Trace.Client == "" || snap.Trace.Client == snap.Client.Type) &&
		snap.Trace.Header == tracing.SessionHeader && snap.Trace.Source != schema.TraceSourceNone && tracing.ValidateTraceID(snap.Trace.SessionID) {
		obj["trace"] = map[string]any{
			"enabled":  true,
			"active":   true,
			"client":   snap.Trace.Client,
			"trace_id": secure.MaskSessionID(snap.Trace.SessionID),
			"header":   snap.Trace.Header,
			"provider": snap.Trace.Provider,
			"source":   snap.Trace.Source,
			"started_at": func() string {
				if snap.Trace.StartedAt.IsZero() {
					return ""
				}
				return snap.Trace.StartedAt.UTC().Format(time.RFC3339)
			}(),
		}
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.Encode(obj)
}

func printFullStatus(stdout io.Writer, snap *schema.Snapshot, gs *schema.GlobalState, reveal bool, showTrace ...bool) {
	fmt.Fprintf(stdout, "FreeInference Companion %s\n", Version)
	fmt.Fprintf(stdout, "Session:  %s (%s)\n", displaySessionID(snap.Session.ID, reveal), snap.Session.Status)
	fmt.Fprintf(stdout, "Client:   %s\n", snap.Client.Type)
	provider := snap.Provider.Name
	if !snap.Provider.Confirmed {
		provider = "unknown (unconfirmed)"
	}
	fmt.Fprintf(stdout, "Provider: %s (source: %s)\n", provider, snap.Provider.Source)
	traceVisible := len(showTrace) == 0 || showTrace[0]
	if traceVisible && snap.Trace != nil && snap.Trace.Enabled && snap.Trace.Verified && snap.Provider.Confirmed && snap.Provider.Name == schema.ProviderFreeInference &&
		snap.Trace.Provider == schema.ProviderFreeInference && (snap.Trace.Client == "" || snap.Trace.Client == snap.Client.Type) &&
		snap.Trace.Header == tracing.SessionHeader && snap.Trace.Source != schema.TraceSourceNone && tracing.ValidateTraceID(snap.Trace.SessionID) {
		fmt.Fprintln(stdout, "Tracing:  active (X-Session-ID)")
	}
	if snap.Model.ContextLength != nil {
		fmt.Fprintf(stdout, "Model:    %s (%s context)\n", snap.Model.ID, formatTokenCount(*snap.Model.ContextLength))
	} else {
		fmt.Fprintf(stdout, "Model:    %s (context unknown)\n", snap.Model.ID)
	}
	fmt.Fprintln(stdout)

	if snap.Client.Type == schema.ClientCodex {
		fmt.Fprintln(stdout, "Live Context: unavailable (Codex does not expose live token telemetry)")
	} else if snap.LiveContext != nil {
		lc := snap.LiveContext
		fmt.Fprintf(stdout, "Live Context (from %s at %s):\n", lc.Source, lc.ObservedAt.Format(time.RFC3339))
		if lc.TotalTokenSemantics != "" {
			fmt.Fprintf(stdout, "  Token semantics: %s\n", lc.TotalTokenSemantics)
		}
		if lc.TotalInputTokens != nil {
			fmt.Fprintf(stdout, "  Total input:  %s", formatTokenPtr(lc.TotalInputTokens))
			if lc.TotalOutputTokens != nil {
				fmt.Fprintf(stdout, "\n  Total output: %s", formatTokenPtr(lc.TotalOutputTokens))
				total := *lc.TotalInputTokens + *lc.TotalOutputTokens
				fmt.Fprintf(stdout, "\n  Combined:     %s", formatTokenCount(total))
			}
			if lc.ContextWindowSize != nil {
				fmt.Fprintf(stdout, "\n  Window:       %s", formatTokenCount(*lc.ContextWindowSize))
			}
			if lc.UsedPercentage != nil {
				fmt.Fprintf(stdout, "\n  Used:         %.1f%%", *lc.UsedPercentage)
			}
			fmt.Fprintln(stdout)
		} else if lc.UsedPercentage != nil {
			fmt.Fprintf(stdout, "  Used:         %.1f%%\n", *lc.UsedPercentage)
		}
		if lc.LatestRequest != nil {
			fmt.Fprintln(stdout, "  Latest request:")
			fmt.Fprintf(stdout, "    Fresh:      %s\n", formatTokenPtr(lc.LatestRequest.FreshInputTokens))
			fmt.Fprintf(stdout, "    Cache read: %s\n", formatTokenPtr(lc.LatestRequest.CacheReadInputTokens))
			fmt.Fprintf(stdout, "    Cache new:  %s\n", formatTokenPtr(lc.LatestRequest.CacheCreationInputTokens))
			fmt.Fprintf(stdout, "    Output:     %s\n", formatTokenPtr(lc.LatestRequest.OutputTokens))
		}
		fmt.Fprintln(stdout)
	}

	fmt.Fprintf(stdout, "Pressure: %s\n", snap.Pressure.State)
	if snap.Pressure.Reason != nil {
		fmt.Fprintf(stdout, "  Reason: %s\n", *snap.Pressure.Reason)
	}

	if snap.Activity.TurnActive != nil {
		if *snap.Activity.TurnActive {
			fmt.Fprintln(stdout, "Turn:     active")
		} else {
			fmt.Fprintln(stdout, "Turn:     inactive")
		}
	} else {
		fmt.Fprintln(stdout, "Turn:     unknown")
	}
	fmt.Fprintln(stdout)

	if snap.Client.Type == schema.ClientCodex {
		fmt.Fprintln(stdout, "Cache Analysis: unavailable (Codex does not expose cache telemetry)")
	} else if snap.CacheAnalysis != nil && (snap.CacheAnalysis.ObservationCount > 0 || snap.CacheAnalysis.RequestSamples > 0) {
		fmt.Fprintf(stdout, "Cache Analysis (%d observed, %d analyzed, %d usable):\n",
			snap.CacheAnalysis.ObservationCount, snap.CacheAnalysis.AnalysisWindowCount, snap.CacheAnalysis.UsableSampleCount)
		fmt.Fprintf(stdout, "  Availability: %s\n", snap.CacheAnalysis.Availability)
		fmt.Fprintf(stdout, "  Read share:  %s\n", formatPctPtr(snap.CacheAnalysis.CacheReadShare))
		fmt.Fprintf(stdout, "  New share:   %s\n", formatPctPtr(snap.CacheAnalysis.CacheCreationShare))
		fmt.Fprintf(stdout, "  Fresh share: %s\n", formatPctPtr(snap.CacheAnalysis.FreshInputShare))
		fmt.Fprintf(stdout, "  Trend:       %s\n", snap.CacheAnalysis.Trend)
		fmt.Fprintln(stdout)
	}

	if snap.Compaction.LastResult != nil {
		r := snap.Compaction.LastResult
		fmt.Fprintf(stdout, "Last Compaction (%s", r.At.Format(time.RFC3339))
		if r.Trigger != "" {
			fmt.Fprintf(stdout, ", %s", r.Trigger)
		}
		fmt.Fprintln(stdout, "):")
		fmt.Fprintf(stdout, "  Before:    %s\n", formatTokenPtr(r.PreTokens))
		fmt.Fprintf(stdout, "  After:     %s\n", formatTokenPtr(r.PostTokens))
		if r.ReductionPct != nil {
			fmt.Fprintf(stdout, "  Reduction: %.1f%%\n", *r.ReductionPct)
		} else {
			fmt.Fprintln(stdout, "  Reduction: unknown")
		}
		fmt.Fprintln(stdout)
	}

	if gs != nil && gs.Health != nil && adapters.IsConfirmedFreeInference(snap.Provider) {
		healthAge := time.Since(gs.Health.FetchedAt)
		healthStale := healthAge < 0 || healthAge > schema.DefaultHealthMaxAge
		fmt.Fprintf(stdout, "Provider Health:\n")
		status := gs.Health.Status
		if healthStale {
			status += " (stale)"
		}
		fmt.Fprintf(stdout, "  Status:  %s\n", status)
		if gs.Health.HealthyCount != nil && gs.Health.UnhealthyCount != nil {
			fmt.Fprintf(stdout, "  Models:  %d healthy, %d unhealthy\n", *gs.Health.HealthyCount, *gs.Health.UnhealthyCount)
		}
		fmt.Fprintf(stdout, "  Checked: %s\n", gs.Health.FetchedAt.Format(time.RFC3339))
		fmt.Fprintln(stdout)
	}

	accountUsable := gs != nil && gs.AccountUsage != nil && gs.AccountUsageCapability != nil &&
		gs.AccountUsageCapability.State == schema.CapabilitySupported && schema.ValidateAccountUsage(gs.AccountUsage) == nil
	if accountUsable && adapters.IsConfirmedFreeInference(snap.Provider) {
		au := gs.AccountUsage
		age := time.Since(au.FetchedAt)
		stale := age < 0 || age > schema.DefaultAccountUsageMaxAge
		fmt.Fprintf(stdout, "Account Usage:\n")
		if stale {
			fmt.Fprintln(stdout, "  Status:  stale (not used for budget calculations)")
		}
		fmt.Fprintf(stdout, "  Updated: %s\n", au.FetchedAt.Format(time.RFC3339))
		if !stale && (au.RequestsUsed != nil || au.RequestsLimit != nil) {
			fmt.Fprintf(stdout, "  Requests: %s\n", formatQuotaPair(au.RequestsUsed, au.RequestsLimit))
		}
		if !stale && (au.TokensUsed != nil || au.TokensLimit != nil) {
			fmt.Fprintf(stdout, "  Tokens:   %s\n", formatQuotaPair(au.TokensUsed, au.TokensLimit))
		}

		// Token budget projection — estimates quota exhaustion timeline.
		proj := engine.BudgetProjection{Status: engine.BudgetUnknown}
		if !stale {
			proj = engine.ProjectBudget(au, snap, time.Now().UTC(), gs.CircuitBreakers)
		}
		if proj.Status != engine.BudgetUnknown {
			fmt.Fprintf(stdout, "  Budget:   %s %s\n",
				budgetIcon(proj.Status), strings.ToLower(string(proj.Status)))
			if proj.EstimatedExhaustion != nil {
				fmt.Fprintf(stdout, "  ETA:      %s\n", proj.EstimatedExhaustion.Format("Jan 2 15:04 MST"))
			}
			if proj.Detail != "" {
				fmt.Fprintf(stdout, "           %s\n", proj.Detail)
			}
		}
		fmt.Fprintln(stdout)
	}

	if snap.LastFailure != nil {
		fmt.Fprintf(stdout, "Last Failure: %s (at %s)\n", snap.LastFailure.Category, snap.LastFailure.ObservedAt.Format(time.RFC3339))
	}
}

// cmdContext implements `freeinference context`. Missing Claude metrics render
// as "unknown"; unsupported Codex metrics render as "unavailable".
func cmdContext(paths state.Paths, args []string, _ io.Reader, stdout, stderr io.Writer) int {
	clientType, sessionID, _, _, _, err := parseClientSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	if clientType == schema.ClientCodex {
		return printCodexContextUnavailable(stdout)
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
	if resolved.Client == schema.ClientCodex {
		return printCodexContextUnavailable(stdout)
	}

	var usedPct *float64
	if snap.LiveContext != nil {
		usedPct = snap.LiveContext.UsedPercentage
	}

	if usedPct == nil {
		fmt.Fprintln(stdout, "Context:    unknown")
		fmt.Fprintln(stdout, "Limit:      unknown")
		fmt.Fprintf(stdout, "State:      %s\n", snap.Pressure.State)
		fmt.Fprintln(stdout, "Suggestion: insufficient telemetry")
		return 0
	}

	fmt.Fprintf(stdout, "Context:    %.1f%%\n", *usedPct)
	if snap.Model.ContextLength != nil {
		fmt.Fprintf(stdout, "Limit:      %s\n", formatTokenCount(*snap.Model.ContextLength))
	} else {
		fmt.Fprintln(stdout, "Limit:      unknown")
	}
	fmt.Fprintf(stdout, "State:      %s\n", snap.Pressure.State)

	switch snap.Pressure.State {
	case schema.PressureWatch:
		fmt.Fprintln(stdout, "Suggestion: Monitor context growth.")
	case schema.PressureWarn:
		fmt.Fprintln(stdout, "Suggestion: Consider compacting soon.")
	case schema.PressureCritical:
		fmt.Fprintln(stdout, "Suggestion: Compact or start a fresh session.")
	default:
		fmt.Fprintln(stdout, "Suggestion: No action needed.")
	}
	return 0
}

func printCodexContextUnavailable(stdout io.Writer) int {
	fmt.Fprintln(stdout, "Context:    unavailable")
	fmt.Fprintln(stdout, "Limit:      unavailable")
	fmt.Fprintln(stdout, "State:      unavailable")
	fmt.Fprintln(stdout, "Suggestion: Codex does not expose live token or context telemetry.")
	return 0
}

// stdinHasData reports whether stdin looks like a pipe with data.
func stdinHasData(stdin io.Reader) bool {
	if stdin == nil {
		return false
	}
	// Test and embedding callers commonly provide a buffered reader. Honor
	// its length when available so an empty in-memory reader is interactive
	// input, not a phantom automatic status-line payload.
	if sized, ok := stdin.(interface{ Len() int }); ok {
		return sized.Len() > 0
	}
	f, ok := stdin.(*os.File)
	if !ok {
		return true // non-file reader (tests): assume data
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) == 0
}

// activationID returns the filesystem-safe identity for the given activation.
// Returns an error when the runtime is not active or when identity derivation fails.
// This is used to gate health and circuit-breaker data in BuildViewModel.
func activationID(activation runtime.Activation) (string, error) {
	if !activation.Active {
		return "", errors.New("runtime not active")
	}
	id, err := activation.Identity(runtime.DefaultSaltLoader())
	if err != nil {
		return "", fmt.Errorf("failed to derive identity: %w", err)
	}
	dirName := id.DirName()
	if dirName == "" {
		return "", errors.New("identity dir name is empty")
	}
	return dirName, nil
}
