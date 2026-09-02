package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
)

const (
	fiStatusOperational = "operational"
	fiStatusDegraded    = "degraded"
	fiStatusUnknown     = "unknown"
)

type fiStatusModel struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	CheckedAt  string   `json:"checked_at,omitempty"`
	LatencyMs  *int64   `json:"latency_ms,omitempty"`
	TTFTMs     *int64   `json:"ttft_ms,omitempty"`
	Throughput *float64 `json:"throughput_tps,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type fiStatusOutput struct {
	Overall         string          `json:"overall"`
	ModelsUp        int             `json:"models_up"`
	ModelsDown      int             `json:"models_down"`
	ModelsTotal     int             `json:"models_total"`
	SourceCheckedAt string          `json:"source_checked_at,omitempty"`
	FetchedAt       string          `json:"fetched_at"`
	Source          string          `json:"source"`
	Error           string          `json:"error,omitempty"`
	Models          []fiStatusModel `json:"models"`
}

// cmdFIStatus implements the deliberately stateless public health check.
// --refresh is accepted for scripting symmetry but has no local cache to
// invalidate: every invocation performs one direct GET request.
func cmdFIStatus(args []string, stdout, stderr io.Writer) int {
	return cmdFIStatusWithClient(args, stdout, stderr, nil)
}

func cmdFIStatusWithClient(args []string, stdout, stderr io.Writer, client *http.Client) int {
	jsonOut := false
	showAll := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "--refresh":
			// Direct fetch; intentionally no cache.
		case "--all":
			showAll = true
		default:
			fmt.Fprintf(stderr, "usage error: unknown flag %q\n", arg)
			return 2
		}
	}

	fetchedAt := time.Now().UTC()
	status, err := api.FetchPublicStatusWithClient(context.Background(), client)
	if err != nil {
		out := fiStatusOutput{
			Overall:   fiStatusUnknown,
			FetchedAt: fetchedAt.Format(time.RFC3339),
			Source:    api.PublicStatusSource,
			Error:     "status endpoint unavailable",
			Models:    []fiStatusModel{},
		}
		if jsonOut {
			writeFIStatusJSON(stdout, out)
		} else {
			fmt.Fprintln(stdout, "FreeInference Status")
			fmt.Fprintln(stdout, "Overall: unknown")
			fmt.Fprintln(stdout, "Status endpoint unavailable.")
		}
		return 0
	}

	out := normalizeFIStatus(*status, fetchedAt, showAll)
	if jsonOut {
		writeFIStatusJSON(stdout, out)
		return 0
	}
	fmt.Fprintln(stdout, "FreeInference Status")
	fmt.Fprintf(stdout, "Overall: %s\n", out.Overall)
	fmt.Fprintf(stdout, "Models: %d up, %d down, %d total\n", out.ModelsUp, out.ModelsDown, out.ModelsTotal)
	if out.SourceCheckedAt != "" {
		fmt.Fprintf(stdout, "Checked: %s\n", out.SourceCheckedAt)
	}
	for _, model := range out.Models {
		line := fmt.Sprintf("  %-24s %s", model.ID, model.Status)
		if model.LatencyMs != nil {
			line += fmt.Sprintf(" (%d ms)", *model.LatencyMs)
		}
		if model.Error != "" {
			line += ": " + model.Error
		}
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "Source: %s\n", out.Source)
	return 0
}

func normalizeFIStatus(status api.PublicStatusResponse, fetchedAt time.Time, showAll bool) fiStatusOutput {
	models := make([]fiStatusModel, 0, len(status.Models))
	// The top-level counts describe the monitor's complete population. The
	// model list is a display subset and may be omitted or extended by the
	// service independently of those aggregate counts.
	up, down := status.Healthy, status.Unhealthy
	for _, model := range status.Models {
		latest := model.Latest
		modelStatus := fiStatusOperational
		if !latest.OK {
			modelStatus = "down"
		}
		if showAll || modelStatus != fiStatusOperational {
			models = append(models, fiStatusModel{
				ID:         secure.SanitizeField(model.ModelID),
				Status:     modelStatus,
				CheckedAt:  sanitizeStatusTimestamp(latest.CheckedAt),
				LatencyMs:  latest.LatencyMs,
				TTFTMs:     latest.TTFTMs,
				Throughput: latest.ThroughputTps,
				Error:      sanitizeStatusError(latest.Error),
			})
		}
	}
	total := status.Total
	if up+down == 0 && len(status.Models) > 0 {
		for _, model := range status.Models {
			if model.Latest.OK {
				up++
			} else {
				down++
			}
		}
	}
	// If the service omits total but supplies aggregate health counts, use
	// those authoritative top-level counts. Never inflate them from the
	// model display list, which may be only a subset of the population.
	if total == 0 && up+down > 0 {
		total = up + down
	}
	overall := fiStatusOperational
	if total == 0 {
		overall = fiStatusUnknown
	} else if down > 0 || up < total {
		overall = fiStatusDegraded
	}
	return fiStatusOutput{
		Overall:         overall,
		ModelsUp:        up,
		ModelsDown:      down,
		ModelsTotal:     total,
		SourceCheckedAt: sanitizeStatusTimestamp(status.Cycle.CheckedAt),
		FetchedAt:       fetchedAt.Format(time.RFC3339),
		Source:          api.PublicStatusSource,
		Models:          models,
	}
}

func sanitizeStatusError(value string) string {
	value = secure.SanitizeField(value)
	value = strings.TrimSpace(value)
	if len(value) > 160 {
		return value[:160]
	}
	return value
}

func sanitizeStatusTimestamp(value string) string {
	value = strings.TrimSpace(secure.SanitizeField(value))
	if len(value) > 80 {
		return value[:80]
	}
	return value
}

func writeFIStatusJSON(w io.Writer, output fiStatusOutput) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(output)
}
