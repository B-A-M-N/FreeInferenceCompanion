package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// cmdStatus implements `fi status`.
func cmdStatus(paths state.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	compact := false
	var flagArgs []string
	for _, a := range args {
		if a == "--compact" {
			compact = true
		} else {
			flagArgs = append(flagArgs, a)
		}
	}
	clientType, sessionID, _, reveal, err := parseClientSessionFlags(flagArgs)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}

	// Status-line mode: a Claude status payload arrives on stdin.
	if sessionID == "" && stdinHasData(stdin) {
		var statusInput schema.ClaudeStatusLineInput
		if err := json.NewDecoder(stdin).Decode(&statusInput); err == nil && statusInput.SessionID != "" {
			sessionID = statusInput.SessionID
			if clientType == "" {
				clientType = schema.ClientClaudeCode
			}
			adapter := adapters.NewClaudeAdapter(paths)
			_ = adapter.HandleStatusLineUpdate(&statusInput, sessionID)

			snap, err := state.LoadSnapshot(paths, schema.ClientClaudeCode, sessionID)
			if err != nil || snap == nil {
				fmt.Fprintln(stdout, "FI: no data")
				return 0
			}
			gs := loadGlobal(paths)
			vm := buildView(snap, gs)
			rc := renderConfig()
			if compact {
				fmt.Fprintln(stdout, vm.Line(rc))
			} else {
				fmt.Fprintln(stdout, vm.Expanded(rc))
			}
			return 0
		}
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

	gs := loadGlobal(paths)
	vm := buildView(resolved.Snap, gs)
	rc := renderConfig()

	if compact {
		fmt.Fprintln(stdout, vm.Line(rc))
		return 0
	}

	printFullStatus(stdout, resolved.Snap, gs, reveal)
	return 0
}

func printFullStatus(stdout io.Writer, snap *schema.Snapshot, gs *schema.GlobalState, reveal bool) {
	fmt.Fprintf(stdout, "FreeInference Companion %s\n", Version)
	fmt.Fprintf(stdout, "Session:  %s (%s)\n", displaySessionID(snap.Session.ID, reveal), snap.Session.Status)
	fmt.Fprintf(stdout, "Client:   %s\n", snap.Client.Type)
	provider := snap.Provider.Name
	if !snap.Provider.Confirmed {
		provider = "unknown (unconfirmed)"
	}
	fmt.Fprintf(stdout, "Provider: %s (source: %s)\n", provider, snap.Provider.Source)
	if snap.Model.ContextLength != nil {
		fmt.Fprintf(stdout, "Model:    %s (%s context)\n", snap.Model.ID, formatTokenCount(*snap.Model.ContextLength))
	} else {
		fmt.Fprintf(stdout, "Model:    %s (context unknown)\n", snap.Model.ID)
	}
	fmt.Fprintln(stdout)

	if snap.LiveContext != nil {
		lc := snap.LiveContext
		fmt.Fprintf(stdout, "Live Context (from %s at %s):\n", lc.Source, lc.ObservedAt.Format(time.RFC3339))
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

	if snap.CacheAnalysis != nil && snap.CacheAnalysis.RequestSamples > 0 {
		fmt.Fprintf(stdout, "Cache Analysis (%d unique samples):\n", snap.CacheAnalysis.RequestSamples)
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
		fmt.Fprintf(stdout, "Provider Health:\n")
		fmt.Fprintf(stdout, "  Status:  %s\n", gs.Health.Status)
		if gs.Health.HealthyCount != nil && gs.Health.UnhealthyCount != nil {
			fmt.Fprintf(stdout, "  Models:  %d healthy, %d unhealthy\n", *gs.Health.HealthyCount, *gs.Health.UnhealthyCount)
		}
		fmt.Fprintf(stdout, "  Checked: %s\n", gs.Health.FetchedAt.Format(time.RFC3339))
		fmt.Fprintln(stdout)
	}

	if snap.LastFailure != nil {
		fmt.Fprintf(stdout, "Last Failure: %s (at %s)\n", snap.LastFailure.Category, snap.LastFailure.ObservedAt.Format(time.RFC3339))
	}
}

// cmdContext implements `fi context`. Missing metrics render as "unknown",
// never as zero.
func cmdContext(paths state.Paths, args []string, _ io.Reader, stdout, stderr io.Writer) int {
	clientType, sessionID, _, _, err := parseClientSessionFlags(args)
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

// stdinHasData reports whether stdin looks like a pipe with data.
func stdinHasData(stdin io.Reader) bool {
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
