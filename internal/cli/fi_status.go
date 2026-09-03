package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/render"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
)

const (
	fiStatusSchemaVersion = 1

	fiOverallOperational = "operational"
	fiOverallDegraded    = "degraded"
	fiOverallUnknown     = "unknown"

	fiModelUp      = "up"
	fiModelDown    = "down"
	fiModelUnknown = "unknown"

	fiMonitorHealthy     = "healthy"
	fiMonitorPartial     = "partial"
	fiMonitorStale       = "stale"
	fiMonitorUnavailable = "unavailable"
	fiMonitorUnknown     = "unknown"
)

type fiStatusModel struct {
	ID               string   `json:"id"`
	Status           string   `json:"status"`
	CheckedAt        string   `json:"checked_at,omitempty"`
	CheckAgeSeconds  int64    `json:"check_age_seconds,omitempty"`
	LatencyMs        *int64   `json:"latency_ms,omitempty"`
	TTFTMs           *int64   `json:"ttft_ms,omitempty"`
	CompletionTokens *int64   `json:"completion_tokens,omitempty"`
	Throughput       *float64 `json:"throughput_tps,omitempty"`
	UptimePct        *float64 `json:"uptime_pct,omitempty"`
	// CurrentStateFor is retained as an in-process compatibility alias. The
	// serialized contract uses observed_state_for_seconds to make clear that
	// duration is bounded by monitor samples, not by fetch time.
	CurrentStateFor         *int64 `json:"-"`
	ObservedStateFor        *int64 `json:"observed_state_for_seconds,omitempty"`
	StateDurationAtLeast    bool   `json:"state_duration_at_least,omitempty"`
	StateTransitionInterval *int64 `json:"state_transition_interval_seconds,omitempty"`
	LatencyMinMs            *int64 `json:"latency_min_ms,omitempty"`
	LatencyMaxMs            *int64 `json:"latency_max_ms,omitempty"`
	TTFTMinMs               *int64 `json:"ttft_min_ms,omitempty"`
	TTFTMaxMs               *int64 `json:"ttft_max_ms,omitempty"`
	Error                   string `json:"error,omitempty"`
}

type fiMonitorOutput struct {
	Status      string `json:"status"`
	CheckedAt   string `json:"checked_at,omitempty"`
	AgeSeconds  int64  `json:"age_seconds,omitempty"`
	Error       string `json:"error,omitempty"`
	exitFailure bool   `json:"-"`
}

type fiStatusOutput struct {
	SchemaVersion    int             `json:"schema_version"`
	Overall          string          `json:"overall"`
	ModelsUp         int             `json:"models_up"`
	ModelsDown       int             `json:"models_down"`
	ModelsUnknown    int             `json:"models_unknown"`
	ModelsTotal      int             `json:"models_total"`
	SourceCheckedAt  string          `json:"source_checked_at,omitempty"`
	RequestStartedAt string          `json:"request_started_at,omitempty"`
	FetchedAt        string          `json:"fetched_at"`
	Source           string          `json:"source"`
	Error            string          `json:"error,omitempty"`
	Monitor          fiMonitorOutput `json:"monitor"`
	Models           []fiStatusModel `json:"models"`
}

// cmdFIStatus implements the deliberately stateless public health check.
// --refresh and --all remain accepted as compatibility aliases; each
// invocation already performs one direct GET and displays all models.
func cmdFIStatus(args []string, stdout, stderr io.Writer) int {
	return cmdFIStatusWithClient(args, stdout, stderr, nil)
}

func cmdFIStatusWithClient(args []string, stdout, stderr io.Writer, client *http.Client) int {
	jsonOut := false
	problemsOnly := false
	details := false
	failDegraded := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "--refresh", "--all":
			// Compatibility aliases. The command is stateless and already
			// displays every model by default.
		case "--problems", "--down":
			problemsOnly = true
		case "--details":
			details = true
		case "--fail-degraded":
			failDegraded = true
		default:
			fmt.Fprintf(stderr, "usage error: unknown flag %q\n", arg)
			return 2
		}
	}

	requestStartedAt := time.Now().UTC()
	status, err := api.FetchPublicStatusWithClient(context.Background(), client)
	fetchedAt := time.Now().UTC()
	if err != nil {
		out := unknownFIStatusOutput(requestStartedAt, fetchedAt, "status endpoint unavailable")
		writeFIStatusOutput(stdout, out, jsonOut, details)
		return 1
	}

	out := normalizeFIStatusAt(*status, requestStartedAt, fetchedAt, problemsOnly)
	writeFIStatusOutput(stdout, out, jsonOut, details)
	if out.Monitor.exitFailure {
		return 1
	}
	if failDegraded && out.ModelsDown > 0 {
		return 1
	}
	return 0
}

func writeFIStatusOutput(stdout io.Writer, out fiStatusOutput, jsonOut, details bool) {
	if jsonOut {
		writeFIStatusJSON(stdout, out)
		return
	}
	renderFIStatusHuman(stdout, out, details, render.DetectWidth())
}

func unknownFIStatusOutput(requestStartedAt, fetchedAt time.Time, reason string) fiStatusOutput {
	return fiStatusOutput{
		SchemaVersion:    fiStatusSchemaVersion,
		Overall:          fiOverallUnknown,
		RequestStartedAt: requestStartedAt.Format(time.RFC3339Nano),
		FetchedAt:        fetchedAt.Format(time.RFC3339Nano),
		Source:           api.PublicStatusSource,
		Error:            reason,
		Monitor: fiMonitorOutput{
			Status:      fiMonitorUnavailable,
			Error:       reason,
			exitFailure: true,
		},
		Models: []fiStatusModel{},
	}
}

// normalizeFIStatus retains the old test/helper shape. A true showAll value
// means all models; new callers use normalizeFIStatusAt with explicit times
// and a problems-only filter.
func normalizeFIStatus(status api.PublicStatusResponse, fetchedAt time.Time, showAll bool) fiStatusOutput {
	return normalizeFIStatusAt(status, fetchedAt, fetchedAt, !showAll)
}

func normalizeFIStatusAt(status api.PublicStatusResponse, requestStartedAt, fetchedAt time.Time, problemsOnly bool) fiStatusOutput {
	if err := status.Validate(); err != nil {
		return unknownFIStatusOutput(requestStartedAt, fetchedAt, "invalid status response")
	}

	monitor := normalizeMonitor(status, fetchedAt)
	models := make([]fiStatusModel, 0, len(status.Models))
	unknownCount := 0
	for _, model := range status.Models {
		normalized := normalizeFIStatusModel(model, fetchedAt)
		if normalized.Status == fiModelUnknown {
			unknownCount++
		}
		if !problemsOnly || normalized.Status != fiModelUp {
			models = append(models, normalized)
		}
	}
	sortFIStatusModels(models)

	up, down := status.Healthy, status.Unhealthy
	total := status.Total
	// The aggregate counts are authoritative when present. Only derive them
	// from the display list when both health counts are absent.
	if up+down == 0 && len(status.Models) > 0 {
		up, down = 0, 0
		for _, model := range status.Models {
			normalized := normalizeFIStatusModel(model, fetchedAt)
			switch normalized.Status {
			case fiModelUp:
				up++
			case fiModelDown:
				down++
			}
		}
	}
	if total == 0 && up+down > 0 {
		total = up + down
	}
	if unknownCount > 0 && monitor.Status == fiMonitorHealthy {
		monitor.Status = fiMonitorPartial
	}
	if monitor.Status == fiMonitorPartial && down == 0 && unknownCount == 0 {
		monitor.Status = fiMonitorUnknown
		monitor.exitFailure = true
	}

	overall := fiOverallOperational
	switch {
	case monitor.Status == fiMonitorStale || monitor.Status == fiMonitorUnknown || monitor.Status == fiMonitorUnavailable:
		overall = fiOverallUnknown
	case down > 0 || up < total || unknownCount > 0:
		overall = fiOverallDegraded
	case monitor.Status == fiMonitorPartial:
		overall = fiOverallUnknown
	case total == 0:
		overall = fiOverallUnknown
	}

	return fiStatusOutput{
		SchemaVersion:    fiStatusSchemaVersion,
		Overall:          overall,
		ModelsUp:         up,
		ModelsDown:       down,
		ModelsUnknown:    unknownCount,
		ModelsTotal:      total,
		SourceCheckedAt:  monitor.CheckedAt,
		RequestStartedAt: requestStartedAt.Format(time.RFC3339Nano),
		FetchedAt:        fetchedAt.Format(time.RFC3339Nano),
		Source:           api.PublicStatusSource,
		Monitor:          monitor,
		Models:           models,
	}
}

func normalizeMonitor(status api.PublicStatusResponse, fetchedAt time.Time) fiMonitorOutput {
	monitor := fiMonitorOutput{Status: fiMonitorUnknown}
	checkedAt, ok := parseStatusTime(status.Cycle.CheckedAt)
	if !ok || status.Cycle.ValidationError != "" {
		monitor.Error = "monitor cycle timestamp unavailable"
		monitor.exitFailure = true
		return monitor
	}
	if checkedAt.After(time.Now().UTC()) {
		monitor.Error = "monitor cycle timestamp is in the future"
		monitor.exitFailure = true
		return monitor
	}
	monitor.CheckedAt = checkedAt.Format(time.RFC3339Nano)
	monitor.AgeSeconds = statusAgeSeconds(fetchedAt, checkedAt)
	if status.Cycle.Error != "" {
		monitor.Error = sanitizeStatusError(status.Cycle.Error)
	}
	if status.Cycle.OK == nil {
		monitor.Error = "monitor cycle status unavailable"
		monitor.exitFailure = true
		return monitor
	}
	if monitor.AgeSeconds > int64(api.PublicStatusStaleAfter/time.Second) {
		monitor.Status = fiMonitorStale
		monitor.Error = "monitor cycle is stale"
		monitor.exitFailure = true
		return monitor
	}
	if !*status.Cycle.OK {
		monitor.Status = fiMonitorPartial
		return monitor
	}
	monitor.Status = fiMonitorHealthy
	return monitor
}

func normalizeFIStatusModel(model api.PublicStatusModel, fetchedAt time.Time) fiStatusModel {
	id := secure.SanitizeField(strings.TrimSpace(model.ModelID))
	if id == "" {
		id = "unknown"
	}
	result := fiStatusModel{ID: id, Status: fiModelUnknown}
	if model.Latest == nil || model.ValidationError != "" || model.Latest.OK == nil {
		if model.Latest != nil {
			result.Error = sanitizeStatusError(model.Latest.Error)
		}
		return result
	}
	checkedAt, ok := parseStatusTime(model.Latest.CheckedAt)
	if !ok {
		result.Error = "invalid check timestamp"
		return result
	}
	if checkedAt.After(time.Now().UTC()) {
		result.Error = "model check timestamp is in the future"
		return result
	}
	result.CheckedAt = checkedAt.Format(time.RFC3339Nano)
	result.CheckAgeSeconds = statusAgeSeconds(fetchedAt, checkedAt)
	if result.CheckAgeSeconds > int64(api.PublicStatusStaleAfter/time.Second) {
		result.Error = "model check is stale"
		return result
	}
	if *model.Latest.OK {
		result.Status = fiModelUp
	} else {
		result.Status = fiModelDown
	}
	result.LatencyMs = model.Latest.LatencyMs
	result.TTFTMs = model.Latest.TTFTMs
	result.CompletionTokens = model.Latest.CompletionTokens
	result.Throughput = model.Latest.ThroughputTps
	result.Error = sanitizeStatusError(model.Latest.Error)
	if model.UptimeRatio != nil {
		value := *model.UptimeRatio * 100
		result.UptimePct = &value
	}
	addHistoryMetrics(&result, model, checkedAt, fetchedAt, *model.Latest.OK)
	return result
}

type fiHistoryPoint struct {
	at      time.Time
	state   string
	latency *int64
	ttft    *int64
}

func addHistoryMetrics(result *fiStatusModel, model api.PublicStatusModel, latestAt, now time.Time, latestOK bool) {
	points := make([]fiHistoryPoint, 0, len(model.History)+1)
	historyPoints := 0
	for _, sample := range model.History {
		at, ok := parseStatusTime(sample.CheckedAt)
		if !ok || sample.OK == nil || at.After(time.Now().UTC()) {
			continue
		}
		state := fiModelDown
		if *sample.OK {
			state = fiModelUp
		}
		points = append(points, fiHistoryPoint{at: at, state: state, latency: sample.LatencyMs, ttft: sample.TTFTMs})
		historyPoints++
	}
	latestState := fiModelDown
	if latestOK {
		latestState = fiModelUp
	}
	points = append(points, fiHistoryPoint{at: latestAt, state: latestState, latency: result.LatencyMs, ttft: result.TTFTMs})
	sort.SliceStable(points, func(i, j int) bool { return points[i].at.Before(points[j].at) })

	if len(points) > 0 {
		var minLatency, maxLatency, minTTFT, maxTTFT *int64
		for _, point := range points {
			minLatency, maxLatency = minMaxInt64(minLatency, maxLatency, point.latency)
			minTTFT, maxTTFT = minMaxInt64(minTTFT, maxTTFT, point.ttft)
		}
		result.LatencyMinMs, result.LatencyMaxMs = minLatency, maxLatency
		result.TTFTMinMs, result.TTFTMaxMs = minTTFT, maxTTFT
	}
	if historyPoints == 0 || len(points) == 0 {
		return
	}
	last := len(points) - 1
	if points[last].state != latestState {
		return
	}
	oldest := points[last].at
	index := last - 1
	for index >= 0 && points[index].state == latestState {
		oldest = points[index].at
		index--
	}
	// `latestAt` is the observation boundary. Using fetch time would silently
	// add network/scheduling delay to the reported state duration.
	_ = now
	duration := int64(latestAt.Sub(oldest) / time.Second)
	if duration < 0 {
		duration = 0
	}
	result.CurrentStateFor = &duration
	result.ObservedStateFor = &duration
	result.StateDurationAtLeast = index < 0
	if index >= 0 {
		transition := int64(latestAt.Sub(points[index].at) / time.Second)
		if transition < 0 {
			transition = 0
		}
		result.StateTransitionInterval = &transition
	}
}

func minMaxInt64(minimum, maximum, value *int64) (*int64, *int64) {
	if value == nil || *value < 0 {
		return minimum, maximum
	}
	if minimum == nil || *value < *minimum {
		v := *value
		minimum = &v
	}
	if maximum == nil || *value > *maximum {
		v := *value
		maximum = &v
	}
	return minimum, maximum
}

func sortFIStatusModels(models []fiStatusModel) {
	statusRank := map[string]int{fiModelDown: 0, fiModelUnknown: 1, fiModelUp: 2}
	sort.SliceStable(models, func(i, j int) bool {
		left, right := statusRank[models[i].Status], statusRank[models[j].Status]
		if left != right {
			return left < right
		}
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})
}

func statusAgeSeconds(now, checkedAt time.Time) int64 {
	age := now.Sub(checkedAt)
	if age < 0 {
		return 0
	}
	return int64(age / time.Second)
}

func parseStatusTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	return parsed.UTC(), err == nil
}

func renderFIStatusHuman(stdout io.Writer, out fiStatusOutput, details bool, width int) {
	title := strings.ToUpper(out.Overall)
	fmt.Fprintf(stdout, "FreeInference Status — %s\n", title)
	if out.ModelsTotal > 0 {
		fmt.Fprintf(stdout, "%d/%d models up", out.ModelsUp, out.ModelsTotal)
	} else {
		fmt.Fprint(stdout, "No model count available")
	}
	if out.Monitor.CheckedAt != "" {
		fmt.Fprintf(stdout, " · monitor checked %s", formatAge(out.Monitor.AgeSeconds))
	}
	fmt.Fprintln(stdout)
	if out.Monitor.Status != fiMonitorHealthy {
		fmt.Fprintf(stdout, "Monitor: %s", strings.ToUpper(out.Monitor.Status))
		if out.Monitor.Error != "" {
			fmt.Fprintf(stdout, " — %s", out.Monitor.Error)
		}
		fmt.Fprintln(stdout)
	}

	if len(out.Models) > 0 {
		fmt.Fprintln(stdout)
		switch {
		case width > 0 && width < 75:
			renderFIStatusNarrow(stdout, out.Models)
		case width > 0 && width < 110:
			renderFIStatusMedium(stdout, out.Models)
		default:
			renderFIStatusWide(stdout, out.Models)
		}
	} else if out.ModelsTotal > 0 {
		fmt.Fprintln(stdout, "No current problems.")
	}

	if details && len(out.Models) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Details:")
		for _, model := range out.Models {
			renderFIStatusDetails(stdout, model)
		}
	} else {
		var problems []fiStatusModel
		for _, model := range out.Models {
			if model.Status != fiModelUp && model.Error != "" {
				problems = append(problems, model)
			}
		}
		if len(problems) > 0 {
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Problems:")
			for _, model := range problems {
				fmt.Fprintf(stdout, "  %s — %s\n", model.ID, model.Error)
			}
		}
	}
	fmt.Fprintf(stdout, "\nSource: %s\n", out.Source)
	if out.FetchedAt != "" {
		fmt.Fprintf(stdout, "Fetched: %s\n", out.FetchedAt)
	}
}

func renderFIStatusWide(stdout io.Writer, models []fiStatusModel) {
	fmt.Fprintln(stdout, "MODEL                    STATUS  LATENCY  TTFT     THROUGHPUT  MONITOR UPTIME  CHECKED")
	fmt.Fprintln(stdout, "──────────────────────────────────────────────────────────────────────────────")
	for _, model := range models {
		fmt.Fprintf(stdout, "%-24s %-7s %-8s %-8s %-11s %-7s %s\n",
			fitStatusModel(model.ID, 24), strings.ToUpper(model.Status), formatMilliseconds(model.LatencyMs),
			formatMilliseconds(model.TTFTMs), formatThroughput(model.Throughput), formatUptime(model.UptimePct), formatAge(model.CheckAgeSeconds))
	}
}

func renderFIStatusMedium(stdout io.Writer, models []fiStatusModel) {
	fmt.Fprintln(stdout, "MODEL                    STATUS  LATENCY  TTFT     MONITOR UPTIME")
	fmt.Fprintln(stdout, "──────────────────────────────────────────────────────────")
	for _, model := range models {
		fmt.Fprintf(stdout, "%-24s %-7s %-8s %-8s %-7s\n",
			fitStatusModel(model.ID, 24), strings.ToUpper(model.Status), formatMilliseconds(model.LatencyMs),
			formatMilliseconds(model.TTFTMs), formatUptime(model.UptimePct))
	}
}

func renderFIStatusNarrow(stdout io.Writer, models []fiStatusModel) {
	for _, model := range models {
		fmt.Fprintf(stdout, "%-20s %-7s %-7s monitor uptime %s\n",
			fitStatusModel(model.ID, 20), strings.ToUpper(model.Status), formatMilliseconds(model.LatencyMs), formatUptime(model.UptimePct))
	}
}

func renderFIStatusDetails(stdout io.Writer, model fiStatusModel) {
	fmt.Fprintf(stdout, "%s\n", model.ID)
	fmt.Fprintf(stdout, "  State:         %s\n", strings.ToUpper(model.Status))
	fmt.Fprintf(stdout, "  Latency:       %s\n", formatMilliseconds(model.LatencyMs))
	fmt.Fprintf(stdout, "  TTFT:          %s\n", formatMilliseconds(model.TTFTMs))
	fmt.Fprintf(stdout, "  Throughput:    %s\n", formatThroughput(model.Throughput))
	fmt.Fprintf(stdout, "  Monitor uptime: %s\n", formatUptime(model.UptimePct))
	if model.CurrentStateFor != nil {
		prefix := "observed "
		if model.StateDurationAtLeast {
			prefix = "observed ≥"
		}
		fmt.Fprintf(stdout, "  State for:     %s %s\n", prefix, formatDuration(*model.CurrentStateFor))
		if model.StateTransitionInterval != nil {
			fmt.Fprintf(stdout, "  Transition interval: %s\n", formatDuration(*model.StateTransitionInterval))
		}
	}
	if model.LatencyMinMs != nil && model.LatencyMaxMs != nil {
		fmt.Fprintf(stdout, "  Latency range: %s – %s\n", formatMilliseconds(model.LatencyMinMs), formatMilliseconds(model.LatencyMaxMs))
	}
	if model.TTFTMinMs != nil && model.TTFTMaxMs != nil {
		fmt.Fprintf(stdout, "  TTFT range:    %s – %s\n", formatMilliseconds(model.TTFTMinMs), formatMilliseconds(model.TTFTMaxMs))
	}
	if model.CompletionTokens != nil {
		fmt.Fprintf(stdout, "  Completion:    %d tokens\n", *model.CompletionTokens)
	}
	fmt.Fprintf(stdout, "  Checked:       %s (%s)\n", model.CheckedAt, formatAge(model.CheckAgeSeconds))
	if model.Error != "" {
		fmt.Fprintf(stdout, "  Error:         %s\n", model.Error)
	}
}

func fitStatusModel(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}

func formatMilliseconds(value *int64) string {
	if value == nil || *value < 0 {
		return "—"
	}
	ms := *value
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	seconds := float64(ms) / 1000
	if ms < 10000 {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", seconds), "0"), ".") + "s"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", seconds), "0"), ".") + "s"
}

func formatThroughput(value *float64) string {
	if value == nil || *value < 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f t/s", *value)
}

func formatUptime(value *float64) string {
	if value == nil || *value < 0 || *value > 100 {
		return "—"
	}
	if *value == float64(int64(*value)) {
		return fmt.Sprintf("%d%%", int64(*value))
	}
	return fmt.Sprintf("%.1f%%", *value)
}

func formatAge(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	return formatDuration(seconds) + " ago"
}

func formatDuration(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
	default:
		return fmt.Sprintf("%dd %dh", seconds/86400, (seconds%86400)/3600)
	}
}

func sanitizeStatusError(value string) string {
	value = strings.TrimSpace(secure.SanitizeField(value))
	if len(value) > 160 {
		return value[:160]
	}
	return value
}

func writeFIStatusJSON(w io.Writer, output fiStatusOutput) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(output)
}
