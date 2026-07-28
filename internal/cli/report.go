package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// reportData is the sanitized, machine-readable report. It never contains
// raw environment values, transcript paths, prompt text, responses,
// working-directory paths, or error bodies.
type reportData struct {
	Tool        string         `json:"tool"`
	Version     string         `json:"version"`
	GeneratedAt string         `json:"generated_at"`
	Client      string         `json:"client,omitempty"`
	Session     *reportSession `json:"session,omitempty"`
	Health      *reportHealth  `json:"health,omitempty"`
	Note        string         `json:"note"`
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
	PressureState     string            `json:"pressure_state"`
	CacheReadShare    *float64          `json:"cache_read_share,omitempty"`
	CacheTrend        string            `json:"cache_trend,omitempty"`
	CacheSamples      int               `json:"cache_samples,omitempty"`
	LastFailure       string            `json:"last_failure,omitempty"`
	LastCompaction    *reportCompaction `json:"last_compaction,omitempty"`
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

const reportNote = "This report is designed to exclude known sensitive fields. Review it before sharing."

// cmdReport implements `fi report`.
func cmdReport(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	clientType, sessionID, format, reveal, err := parseClientSessionFlags(args)
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
	if gs.Health != nil {
		report.Health = &reportHealth{
			Status:    gs.Health.Status,
			Checked:   gs.Health.FetchedAt.UTC().Format(time.RFC3339),
			Healthy:   gs.Health.HealthyCount,
			Unhealthy: gs.Health.UnhealthyCount,
		}
	}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resolved != nil {
		report.Client = resolved.Client
		report.Session = buildReportSession(resolved.Snap, reveal)
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
	}
	if snap.LiveContext != nil {
		rs.ContextUsedPct = snap.LiveContext.UsedPercentage
	}
	if snap.CacheAnalysis != nil {
		rs.CacheReadShare = snap.CacheAnalysis.CacheReadShare
		rs.CacheTrend = snap.CacheAnalysis.Trend
		rs.CacheSamples = snap.CacheAnalysis.RequestSamples
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

	if report.Session == nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "No session resolved. Use --session <id> or see `fi sessions`.")
	} else {
		s := report.Session
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "--- Session ---")
		fmt.Fprintf(stdout, "Client:   %s\n", report.Client)
		fmt.Fprintf(stdout, "Session:  %s (%s)\n", s.ID, s.Status)
		fmt.Fprintf(stdout, "Started:  %s\n", s.StartedAt)
		fmt.Fprintf(stdout, "Model:    %s\n", s.Model)
		fmt.Fprintf(stdout, "Provider: %s (confirmed: %t)\n", s.Provider, s.ProviderConfirmed)
		if s.ContextUsedPct != nil {
			fmt.Fprintf(stdout, "Context:  %.1f%% used\n", *s.ContextUsedPct)
		} else {
			fmt.Fprintln(stdout, "Context:  unknown")
		}
		if s.ContextLimit != nil {
			fmt.Fprintf(stdout, "Limit:    %s\n", formatTokenCount(*s.ContextLimit))
		}
		fmt.Fprintf(stdout, "Pressure: %s\n", s.PressureState)
		if s.CacheSamples > 0 {
			fmt.Fprintf(stdout, "Cache:    %s read share over %d samples (trend: %s)\n",
				formatPctPtr(s.CacheReadShare), s.CacheSamples, s.CacheTrend)
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

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "--- Note ---")
	fmt.Fprintln(stdout, report.Note)
	if !reveal {
		fmt.Fprintln(stdout, "Session identifiers are masked. Pass --include-identifiers to reveal full IDs.")
	}
}
