package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/engine"
	"github.com/b-a-m-n/freeinference-companion/internal/incidents"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// reportData is the sanitized, machine-readable report. It never contains
// raw environment values, transcript paths, prompt text, responses,
// working-directory paths, or error bodies.
type reportData struct {
	Tool             string              `json:"tool"`
	Version          string              `json:"version"`
	GeneratedAt      string              `json:"generated_at"`
	Client           string              `json:"client,omitempty"`
	Historical       bool                `json:"historical,omitempty"`
	RuntimeActive    bool                `json:"runtime_active"`
	Trace            *reportTrace        `json:"trace,omitempty"`
	Session          *reportSession      `json:"session,omitempty"`
	Health           *reportHealth       `json:"health,omitempty"`
	ModelMonitor     *reportModelMonitor `json:"model_monitor,omitempty"`
	Incidents        *incidents.Report   `json:"incidents,omitempty"`
	AccountUsage     *reportAccountUsage `json:"account_usage,omitempty"`
	BudgetProjection string              `json:"budget_projection,omitempty"`
	Note             string              `json:"note"`
}

type reportSession struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	StartedAt         string            `json:"started_at"`
	Model             string            `json:"model"`
	Provider          string            `json:"provider"`
	ProviderConfirmed bool              `json:"provider_confirmed"`
	ContextUsedPct    *float64          `json:"context_used_pct,omitempty"`
	ContextLimit      *int64            `json:"context_limit,omitempty"`
	ContextTelemetry  string            `json:"context_telemetry"`
	PressureState     string            `json:"pressure_state"`
	CacheReadShare    *float64          `json:"cache_read_share,omitempty"`
	CacheTrend        string            `json:"cache_trend,omitempty"`
	CacheObserved     int               `json:"cache_observed_samples,omitempty"`
	CacheAnalyzed     int               `json:"cache_analyzed_samples,omitempty"`
	CacheUsable       int               `json:"cache_usable_samples,omitempty"`
	CacheTelemetry    string            `json:"cache_telemetry"`
	LastFailure       string            `json:"last_failure,omitempty"`
	LastCompaction    *reportCompaction `json:"last_compaction,omitempty"`
}

type reportTrace struct {
	Enabled        bool   `json:"enabled"`
	TraceID        string `json:"trace_id"`
	Client         string `json:"client"`
	Provider       string `json:"provider"`
	Source         string `json:"source"`
	Header         string `json:"header"`
	StartedAt      string `json:"started_at"`
	EndpointOrigin string `json:"endpoint_origin,omitempty"`
}

type reportCompaction struct {
	At           string   `json:"at"`
	Trigger      string   `json:"trigger,omitempty"`
	PreTokens    *int64   `json:"pre_tokens,omitempty"`
	PostTokens   *int64   `json:"post_tokens,omitempty"`
	ReductionPct *float64 `json:"reduction_pct,omitempty"`
}

type reportHealth struct {
	Status    string `json:"status"`
	Checked   string `json:"checked"`
	Healthy   *int   `json:"healthy_count,omitempty"`
	Unhealthy *int   `json:"unhealthy_count,omitempty"`
}

type reportModelMonitor struct {
	Model       string     `json:"model"`
	OK          *bool      `json:"ok,omitempty"`
	UptimeRatio *float64   `json:"uptime_ratio,omitempty"`
	LatencyMs   *int64     `json:"latency_ms,omitempty"`
	TTFTMs      *int64     `json:"ttft_ms,omitempty"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type reportAccountUsage struct {
	FetchedAt     string `json:"fetched_at"`
	RequestsUsed  *int64 `json:"requests_used,omitempty"`
	RequestsLimit *int64 `json:"requests_limit,omitempty"`
	TokensUsed    *int64 `json:"tokens_used,omitempty"`
	TokensLimit   *int64 `json:"tokens_limit,omitempty"`
}

const reportNote = "This report is designed to exclude known sensitive fields. Review it before sharing."

// cmdReport implements `freeinference report`.
func cmdReport(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	clientType, sessionID, format, reveal, _, err := parseClientSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	if format == "" {
		format = "markdown"
	}
	if format != "markdown" && format != "json" {
		fmt.Fprintf(stderr, "unknown format: %s (want markdown|json)\n", format)
		return 2
	}

	gs := loadGlobal(paths)
	report := &reportData{
		Tool:        "freeinference-companion",
		Version:     Version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Note:        reportNote,
	}
	activation := activationForCLICommand("report", args)
	report.RuntimeActive = activation.Active
	if gs.Health != nil {
		report.Health = &reportHealth{
			Status:    gs.Health.Status,
			Checked:   gs.Health.FetchedAt.UTC().Format(time.RFC3339),
			Healthy:   gs.Health.HealthyCount,
			Unhealthy: gs.Health.UnhealthyCount,
		}
	}
	if gs.HasAuthoritativeAccountUsage() {
		report.AccountUsage = &reportAccountUsage{
			FetchedAt:     gs.AccountUsage.FetchedAt.UTC().Format(time.RFC3339),
			RequestsUsed:  gs.AccountUsage.RequestsUsed,
			RequestsLimit: gs.AccountUsage.RequestsLimit,
			TokensUsed:    gs.AccountUsage.TokensUsed,
			TokensLimit:   gs.AccountUsage.TokensLimit,
		}
	}
	incidentFilter := incidents.Filter{Since: time.Now().UTC().Add(-24 * time.Hour)}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resolved != nil {
		report.Client = resolved.Client
		report.Historical = !activation.Active && !activation.Disabled
		report.Session = buildReportSession(resolved.Snap, reveal)
		incidentFilter.Client = resolved.Client
		incidentFilter.SessionID = resolved.SessionID
		report.ModelMonitor = buildReportModelMonitor(gs, resolved.Snap.Model.ID)

		// Compute budget projection for the markdown report.
		if gs.HasAuthoritativeAccountUsage() {
			proj := engine.ProjectBudget(gs.AccountUsage, resolved.Snap, time.Now().UTC(), gs.CircuitBreakers)
			if proj.Status != engine.BudgetUnknown {
				report.BudgetProjection = engineProjectBudgetFromProj(proj)
			}
		}
	}
	if incidentReport, incidentErr := incidents.Collect(paths, incidentFilter, time.Now().UTC()); incidentErr == nil {
		report.Incidents = incidentReport
	}
	if enabled, valid, _ := effectiveTracing(); valid && enabled {
		traceActivation := activation
		if resolved != nil {
			traceActivation = runtime.EvaluateForClient(runtime.ClientKind(resolved.Client))
		} else if inherited, ok := tracing.EnvironmentTrace(); ok {
			traceActivation = runtime.EvaluateForClient(runtime.ClientKind(inherited.Client))
		}
		if traceActivation.Active && resolved != nil {
			report.Trace = buildReportTrace(resolved.Snap)
		}
	}

	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		// Defensive last-mile redaction. Structured report fields are
		// already key-free, but a future field could echo an upstream value.
		fmt.Fprintln(stdout, secure.Redact(string(data)))
		return 0
	}

	printMarkdownReport(stdout, report, reveal)
	return 0
}

func buildReportSession(snap *schema.Snapshot, reveal bool) *reportSession {
	rs := &reportSession{
		ID:                displaySessionID(snap.Session.ID, reveal),
		Status:            snap.Session.Status,
		StartedAt:         snap.Session.StartedAt.UTC().Format(time.RFC3339),
		Model:             secure.SanitizeField(snap.Model.ID),
		Provider:          secure.SanitizeField(snap.Provider.Name),
		ProviderConfirmed: snap.Provider.Confirmed,
		PressureState:     snap.Pressure.State,
		ContextLimit:      snap.Model.ContextLength,
		ContextTelemetry:  "unknown",
		CacheTelemetry:    "unknown",
	}
	if snap.Client.Type == schema.ClientCodex {
		rs.ContextTelemetry = "unavailable"
		rs.CacheTelemetry = "unavailable"
	}
	if snap.Client.Type != schema.ClientCodex && snap.LiveContext != nil {
		rs.ContextUsedPct = snap.LiveContext.UsedPercentage
		rs.ContextTelemetry = string(snap.LiveContext.TotalTokenSemantics)
		if rs.ContextTelemetry == "" {
			rs.ContextTelemetry = "available"
		}
	}
	if snap.Client.Type != schema.ClientCodex && snap.CacheAnalysis != nil {
		rs.CacheReadShare = snap.CacheAnalysis.CacheReadShare
		rs.CacheTrend = snap.CacheAnalysis.Trend
		rs.CacheObserved = snap.CacheAnalysis.ObservationCount
		if rs.CacheObserved == 0 {
			rs.CacheObserved = snap.CacheAnalysis.RequestSamples
		}
		rs.CacheAnalyzed = snap.CacheAnalysis.AnalysisWindowCount
		rs.CacheUsable = snap.CacheAnalysis.UsableSampleCount
		rs.CacheTelemetry = string(snap.CacheAnalysis.Availability)
		if rs.CacheTelemetry == "" {
			rs.CacheTelemetry = "available"
		}
	}
	if snap.LastFailure != nil {
		rs.LastFailure = snap.LastFailure.Category
	}
	if snap.Compaction.LastResult != nil {
		r := snap.Compaction.LastResult
		rs.LastCompaction = &reportCompaction{
			At:           r.At.UTC().Format(time.RFC3339),
			Trigger:      r.Trigger,
			PreTokens:    r.PreTokens,
			PostTokens:   r.PostTokens,
			ReductionPct: r.ReductionPct,
		}
	}
	return rs
}

func buildReportModelMonitor(gs *schema.GlobalState, modelID string) *reportModelMonitor {
	if gs == nil || gs.PublicStatus == nil || modelID == "" {
		return nil
	}
	for _, metric := range gs.PublicStatus.Models {
		if metric.ModelID != modelID {
			continue
		}
		monitor := &reportModelMonitor{Model: secure.SanitizeField(metric.ModelID), UptimeRatio: metric.UptimeRatio}
		if metric.Latest != nil {
			monitor.OK = metric.Latest.OK
			monitor.LatencyMs = metric.Latest.LatencyMs
			monitor.TTFTMs = metric.Latest.TTFTMs
			checked := metric.Latest.CheckedAt
			monitor.CheckedAt = &checked
			monitor.Error = secure.SanitizeField(metric.Latest.Error)
		}
		return monitor
	}
	return nil
}

func buildReportTrace(snap *schema.Snapshot) *reportTrace {
	if snap == nil || snap.Trace == nil || !snap.Trace.Verified || !snap.Provider.Confirmed || snap.Provider.Name != schema.ProviderFreeInference {
		return nil
	}
	if snap.Trace.Client != "" && snap.Trace.Client != snap.Client.Type {
		return nil
	}
	return buildReportTraceInfo(snap.Trace)
}

func buildReportTraceInfo(trace *schema.TraceInfo) *reportTrace {
	if trace == nil || !trace.Enabled || !trace.Verified || trace.Provider != schema.ProviderFreeInference ||
		trace.Header != tracing.SessionHeader || trace.Source == schema.TraceSourceNone || !tracing.ValidateTraceID(trace.SessionID) {
		return nil
	}
	result := &reportTrace{
		Enabled:        true,
		TraceID:        trace.SessionID,
		Client:         secure.SanitizeField(trace.Client),
		Provider:       secure.SanitizeField(trace.Provider),
		Source:         secure.SanitizeField(trace.Source),
		Header:         secure.SanitizeField(trace.Header),
		EndpointOrigin: secure.SanitizeField(trace.EndpointOrigin),
	}
	if !trace.StartedAt.IsZero() {
		result.StartedAt = trace.StartedAt.UTC().Format(time.RFC3339)
	}
	return result
}

func printMarkdownReport(stdout io.Writer, report *reportData, reveal bool) {
	fmt.Fprintln(stdout, "FreeInference Companion Report")
	fmt.Fprintln(stdout, repeat("=", 60))
	fmt.Fprintf(stdout, "Version:    %s\n", report.Version)
	fmt.Fprintf(stdout, "Generated:  %s\n", report.GeneratedAt)

	if report.Health != nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "--- Provider Health ---")
		fmt.Fprintf(stdout, "Status:  %s\n", report.Health.Status)
		if report.Health.Healthy != nil && report.Health.Unhealthy != nil {
			fmt.Fprintf(stdout, "Models:  %d healthy, %d unhealthy\n", *report.Health.Healthy, *report.Health.Unhealthy)
		}
		fmt.Fprintf(stdout, "Checked: %s\n", report.Health.Checked)
	}

	if report.ModelMonitor != nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "--- Model Monitor ---")
		fmt.Fprintf(stdout, "Model:   %s\n", report.ModelMonitor.Model)
		if report.ModelMonitor.OK != nil {
			fmt.Fprintf(stdout, "Status:  %s\n", map[bool]string{true: "up", false: "down"}[*report.ModelMonitor.OK])
		}
		if report.ModelMonitor.UptimeRatio != nil {
			fmt.Fprintf(stdout, "Uptime:  %.1f%%\n", *report.ModelMonitor.UptimeRatio*100)
		}
		if report.ModelMonitor.LatencyMs != nil {
			fmt.Fprintf(stdout, "Latency: %dms\n", *report.ModelMonitor.LatencyMs)
		}
		if report.ModelMonitor.CheckedAt != nil {
			fmt.Fprintf(stdout, "Checked: %s\n", report.ModelMonitor.CheckedAt.UTC().Format(time.RFC3339))
		}
		if report.ModelMonitor.Error != "" {
			fmt.Fprintf(stdout, "Error:   %s\n", report.ModelMonitor.Error)
		}
	}

	if report.AccountUsage != nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "--- Account Usage ---")
		fmt.Fprintf(stdout, "Updated: %s\n", report.AccountUsage.FetchedAt)
		if report.AccountUsage.RequestsUsed != nil || report.AccountUsage.RequestsLimit != nil {
			fmt.Fprintf(stdout, "Requests: %s\n", formatQuotaPair(report.AccountUsage.RequestsUsed, report.AccountUsage.RequestsLimit))
		}
		if report.AccountUsage.TokensUsed != nil || report.AccountUsage.TokensLimit != nil {
			fmt.Fprintf(stdout, "Tokens:   %s\n", formatQuotaPair(report.AccountUsage.TokensUsed, report.AccountUsage.TokensLimit))
		}
	}

	if report.BudgetProjection != "" {
		fmt.Fprintf(stdout, "Budget:   %s\n", report.BudgetProjection)
	}

	if report.Incidents != nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "--- Incident Summary ---")
		fmt.Fprintf(stdout, "Failures: %d\n", report.Incidents.Total)
		for _, count := range report.Incidents.ByCategory {
			fmt.Fprintf(stdout, "  %-24s %d\n", count.Name, count.Count)
		}
	}

	if report.Session == nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "No session resolved. Use --session <id> or see `freeinference sessions`.")
	} else {
		s := report.Session
		if report.Historical {
			fmt.Fprintln(stdout, "Historical session — FreeInference is not currently active.")
		}
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "--- Session ---")
		fmt.Fprintf(stdout, "Client:   %s\n", report.Client)
		fmt.Fprintf(stdout, "Session:  %s (%s)\n", s.ID, s.Status)
		fmt.Fprintf(stdout, "Started:  %s\n", s.StartedAt)
		fmt.Fprintf(stdout, "Model:    %s\n", s.Model)
		fmt.Fprintf(stdout, "Provider: %s (confirmed: %t)\n", s.Provider, s.ProviderConfirmed)
		if s.ContextUsedPct != nil {
			fmt.Fprintf(stdout, "Context:  %.1f%% used\n", *s.ContextUsedPct)
		} else if s.ContextTelemetry == "unavailable" {
			fmt.Fprintln(stdout, "Context:  unavailable (Codex does not expose live context telemetry)")
		} else {
			fmt.Fprintln(stdout, "Context:  unknown")
		}
		if s.ContextLimit != nil {
			fmt.Fprintf(stdout, "Limit:    %s\n", formatTokenCount(*s.ContextLimit))
		}
		fmt.Fprintf(stdout, "Pressure: %s\n", s.PressureState)
		if s.CacheTelemetry == "unavailable" {
			fmt.Fprintln(stdout, "Cache:    unavailable (Codex does not expose cache telemetry)")
		} else if s.CacheObserved > 0 {
			fmt.Fprintf(stdout, "Cache:    %s read share (%d usable of %d observed; trend: %s)\n",
				formatPctPtr(s.CacheReadShare), s.CacheUsable, s.CacheObserved, s.CacheTrend)
		}
		if s.LastCompaction != nil {
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "--- Last Compaction ---")
			fmt.Fprintf(stdout, "At:        %s\n", s.LastCompaction.At)
			if s.LastCompaction.Trigger != "" {
				fmt.Fprintf(stdout, "Trigger:   %s\n", s.LastCompaction.Trigger)
			}
			fmt.Fprintf(stdout, "Before:    %s\n", formatTokenPtr(s.LastCompaction.PreTokens))
			fmt.Fprintf(stdout, "After:     %s\n", formatTokenPtr(s.LastCompaction.PostTokens))
			if s.LastCompaction.ReductionPct != nil {
				fmt.Fprintf(stdout, "Reduction: %.1f%%\n", *s.LastCompaction.ReductionPct)
			} else {
				fmt.Fprintln(stdout, "Reduction: unknown")
			}
		}
		if s.LastFailure != "" {
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "--- Last Failure ---")
			fmt.Fprintf(stdout, "Category: %s\n", s.LastFailure)
		}
	}
	if report.Trace != nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "--- Trace Correlation ---")
		fmt.Fprintf(stdout, "Enabled:  %t\n", report.Trace.Enabled)
		fmt.Fprintf(stdout, "Trace ID: %s\n", report.Trace.TraceID)
		fmt.Fprintf(stdout, "Client:   %s\n", report.Trace.Client)
		fmt.Fprintf(stdout, "Provider: %s\n", report.Trace.Provider)
		fmt.Fprintf(stdout, "Header:   %s\n", report.Trace.Header)
		fmt.Fprintf(stdout, "Source:   %s\n", report.Trace.Source)
		if report.Trace.StartedAt != "" {
			fmt.Fprintf(stdout, "Started:  %s\n", report.Trace.StartedAt)
		}
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "--- Note ---")
	fmt.Fprintln(stdout, report.Note)
	if !reveal {
		fmt.Fprintln(stdout, "Session identifiers are masked. Pass --include-identifiers to reveal full IDs.")
	}
}

// engineProjectBudgetFromProj builds the projection string from an existing projection.
func engineProjectBudgetFromProj(proj engine.BudgetProjection) string {
	if proj.Status == engine.BudgetUnknown {
		return ""
	}
	parts := []string{budgetIcon(proj.Status), strings.ToLower(string(proj.Status))}
	if proj.Detail != "" {
		parts = append(parts, "—", proj.Detail)
	}
	return strings.Join(parts, " ")
}
