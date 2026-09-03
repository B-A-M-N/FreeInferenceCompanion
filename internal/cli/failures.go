package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/incidents"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

const defaultFailureLookback = 24 * time.Hour

// cmdFailures implements `freeinference failures` and its `incidents` alias.
// It is deliberately local-only: no provider request or credential is needed.
func cmdFailures(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	filter := incidents.Filter{}
	jsonOut := false
	sinceValue := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--client":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --client requires a value")
				return 2
			}
			i++
			filter.Client = args[i]
			if filter.Client != schema.ClientClaudeCode && filter.Client != schema.ClientCodex {
				fmt.Fprintf(stderr, "usage error: unknown client %q\n", filter.Client)
				return 2
			}
		case "--session":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --session requires a value")
				return 2
			}
			i++
			filter.SessionID = args[i]
		case "--model":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --model requires a value")
				return 2
			}
			i++
			filter.Model = secure.SafeIdentifier(args[i])
		case "--since":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --since requires a value")
				return 2
			}
			i++
			sinceValue = args[i]
		default:
			if strings.HasPrefix(args[i], "--") {
				fmt.Fprintf(stderr, "usage error: unknown flag %q\n", args[i])
				return 2
			}
			fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", args[i])
			return 2
		}
	}

	now := time.Now().UTC()
	if sinceValue == "" {
		filter.Since = now.Add(-defaultFailureLookback)
	} else {
		since, err := parseFailureSince(sinceValue, now)
		if err != nil {
			fmt.Fprintf(stderr, "usage error: %v\n", err)
			return 2
		}
		filter.Since = since
	}

	report, err := incidents.Collect(paths, filter, now)
	if err != nil {
		fmt.Fprintf(stderr, "error: unable to read incident history: %v\n", err)
		return 1
	}
	if jsonOut {
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			fmt.Fprintf(stderr, "error: %v\n", marshalErr)
			return 1
		}
		fmt.Fprintln(stdout, secure.Redact(string(data)))
		return 0
	}
	printFailureReport(stdout, report)
	return 0
}

func parseFailureSince(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("--since cannot be empty")
	}
	if strings.HasSuffix(value, "d") {
		days, err := time.ParseDuration(strings.TrimSuffix(value, "d") + "h")
		if err == nil {
			days *= 24
			if days > 0 && days <= 30*24*time.Hour {
				return now.Add(-days), nil
			}
		}
		return time.Time{}, fmt.Errorf("invalid --since %q (use a duration up to 30d or RFC3339)", value)
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 && duration <= 30*24*time.Hour {
		return now.Add(-duration), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.After(now) {
		return time.Time{}, fmt.Errorf("invalid --since %q (use a duration up to 30d or RFC3339)", value)
	}
	return parsed.UTC(), nil
}

func printFailureReport(w io.Writer, report *incidents.Report) {
	if report == nil {
		fmt.Fprintln(w, "No retained failures.")
		return
	}
	if report.Since != nil {
		fmt.Fprintf(w, "Failure incidents since %s: %d\n", report.Since.UTC().Format(time.RFC3339), report.Total)
	} else {
		fmt.Fprintf(w, "Failure incidents: %d\n", report.Total)
	}
	printIncidentCounts(w, "By category", report.ByCategory)
	printIncidentCounts(w, "By HTTP status", report.ByStatus)
	printIncidentCounts(w, "By model", report.ByModel)
	printIncidentCounts(w, "By client", report.ByClient)
	if len(report.Warnings) > 0 {
		fmt.Fprintln(w, "Warnings:")
		for _, warning := range report.Warnings {
			fmt.Fprintf(w, "  %s\n", secure.SanitizeField(warning))
		}
	}
	if len(report.Recent) > 0 {
		fmt.Fprintln(w, "Recent:")
		for _, incident := range report.Recent {
			fmt.Fprintf(w, "  %s %-12s %-18s %s", incident.At.Format(time.RFC3339), incident.Client, incident.Category, incident.SessionID)
			if incident.Model != "" {
				fmt.Fprintf(w, " model=%s", secure.Redact(secure.SanitizeField(incident.Model)))
			}
			if incident.HTTPStatus != nil {
				fmt.Fprintf(w, " http=%d", *incident.HTTPStatus)
			}
			if incident.PublicMonitor != nil {
				fmt.Fprintf(w, " monitor=%s", incident.PublicMonitor.Status)
			}
			fmt.Fprintln(w)
		}
	}
}

func printIncidentCounts(w io.Writer, label string, counts []incidents.Count) {
	if len(counts) == 0 {
		return
	}
	fmt.Fprintf(w, "%s:\n", label)
	for _, count := range counts {
		name := secure.Redact(secure.SanitizeField(count.Name))
		fmt.Fprintf(w, "  %-28s %d\n", name, count.Count)
	}
}
