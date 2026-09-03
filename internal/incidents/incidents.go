// Package incidents aggregates bounded turn-failure events for local
// diagnostics. It never reads or returns raw error bodies.
package incidents

import (
	"fmt"
	"sort"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// Filter limits an incident report to the requested local dimensions.
type Filter struct {
	Client    string
	SessionID string
	Model     string
	Since     time.Time
}

// Incident is one sanitized turn failure. Session IDs are always masked in
// this package's output, including when a caller explicitly filters a session.
type Incident struct {
	At                time.Time      `json:"at"`
	Client            string         `json:"client"`
	SessionID         string         `json:"session_id"`
	Model             string         `json:"model,omitempty"`
	Provider          string         `json:"provider,omitempty"`
	Category          string         `json:"category"`
	HTTPStatus        *int           `json:"http_status,omitempty"`
	Retryable         *bool          `json:"retryable,omitempty"`
	TransportClass    string         `json:"transport_class,omitempty"`
	ProviderErrorType string         `json:"provider_error_type,omitempty"`
	ErrorOrigin       string         `json:"error_origin,omitempty"`
	RetryAfterSeconds *int64         `json:"retry_after_seconds,omitempty"`
	RequestReference  string         `json:"request_reference,omitempty"`
	PublicMonitor     *MonitorSample `json:"public_monitor,omitempty"`
}

// MonitorSample is the nearest retained synthetic public check for the same
// model. It is evidence for correlation only, not a measurement of the local
// inference request that failed.
type MonitorSample struct {
	Status          string    `json:"status"`
	SampleAt        time.Time `json:"sample_at"`
	DistanceSeconds int64     `json:"distance_seconds"`
	LatencyMs       *int64    `json:"latency_ms,omitempty"`
	TTFTMs          *int64    `json:"ttft_ms,omitempty"`
	ThroughputTps   *float64  `json:"throughput_tps,omitempty"`
	Interpretation  string    `json:"interpretation"`
}

// Count is a stable category/model/client count in a report.
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Report is the sanitized, on-demand incident summary.
type Report struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Since       *time.Time `json:"since,omitempty"`
	Total       int        `json:"total"`
	ByCategory  []Count    `json:"by_category,omitempty"`
	ByStatus    []Count    `json:"by_status,omitempty"`
	ByModel     []Count    `json:"by_model,omitempty"`
	ByClient    []Count    `json:"by_client,omitempty"`
	Recent      []Incident `json:"recent,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
}

const maxRecent = 20

// Collect reads only the existing per-session event logs and builds a report.
// The session index bounds the scan to known, retained sessions.
func Collect(paths state.Paths, filter Filter, now time.Time) (*Report, error) {
	report := &Report{GeneratedAt: now.UTC()}
	if !filter.Since.IsZero() {
		since := filter.Since.UTC()
		report.Since = &since
	}

	index, err := state.LoadSessionIndex(paths)
	if err != nil {
		return nil, err
	}

	categoryCounts := make(map[string]int)
	statusCounts := make(map[string]int)
	modelCounts := make(map[string]int)
	clientCounts := make(map[string]int)
	global, globalErr := state.LoadGlobal(paths)
	if globalErr != nil {
		report.Warnings = append(report.Warnings, "public monitor cache could not be fully read")
	}
	var monitor *schema.PublicStatusCache
	if global != nil {
		monitor = global.PublicStatus
	}
	for _, entry := range index.Sessions {
		if filter.Client != "" && entry.Client != filter.Client {
			continue
		}
		if filter.SessionID != "" && entry.SessionID != filter.SessionID {
			continue
		}
		events, readErr := state.ReadEvents(paths, entry.Client, entry.SessionID, 0)
		if readErr != nil {
			report.Warnings = append(report.Warnings, "some retained session events could not be read")
			continue
		}
		for _, event := range events {
			if event.Type != state.EventTurnFailed || (!filter.Since.IsZero() && event.At.Before(filter.Since)) {
				continue
			}
			if filter.Model != "" {
				eventModel := event.Model
				if eventModel == "" {
					eventModel = entry.ModelID
				}
				if eventModel != filter.Model {
					continue
				}
			}
			incident := Incident{
				At:                event.At.UTC(),
				Client:            secure.SafeField(entry.Client),
				SessionID:         secure.MaskSessionID(entry.SessionID),
				Model:             secure.SafeField(event.Model),
				Provider:          secure.SafeField(event.Provider),
				Category:          secure.SafeField(event.Detail),
				HTTPStatus:        event.HTTPStatus,
				Retryable:         event.Retryable,
				TransportClass:    secure.SafeField(event.TransportClass),
				ProviderErrorType: secure.SafeField(event.ProviderErrorType),
				ErrorOrigin:       secure.SafeField(event.ErrorOrigin),
				RetryAfterSeconds: event.RetryAfterSeconds,
				RequestReference:  secure.SafeField(event.RequestReference),
			}
			if incident.Model == "" {
				incident.Model = secure.SafeField(entry.ModelID)
			}
			if incident.Category == "" {
				incident.Category = "unknown"
			}
			if incident.HTTPStatus != nil {
				statusCounts[fmt.Sprintf("%d", *incident.HTTPStatus)]++
			}
			// Public monitor samples are evidence about FreeInference only. A
			// coincidentally identical model name in another provider must not
			// make a local failure look correlated with FreeInference health.
			if event.Provider == schema.ProviderFreeInference {
				incident.PublicMonitor = nearestMonitorSample(monitor, incident.Model, incident.At)
			}
			report.Total++
			categoryCounts[incident.Category]++
			if incident.Model != "" {
				modelCounts[incident.Model]++
			}
			clientCounts[incident.Client]++
			report.Recent = append(report.Recent, incident)
		}
	}

	sort.Slice(report.Recent, func(i, j int) bool { return report.Recent[i].At.After(report.Recent[j].At) })
	if len(report.Recent) > maxRecent {
		report.Recent = report.Recent[:maxRecent]
	}
	report.ByCategory = sortedCounts(categoryCounts)
	report.ByStatus = sortedCounts(statusCounts)
	report.ByModel = sortedCounts(modelCounts)
	report.ByClient = sortedCounts(clientCounts)
	return report, nil
}

const maxMonitorCorrelationDistance = api.PublicStatusStaleAfter

func nearestMonitorSample(cache *schema.PublicStatusCache, modelID string, incidentAt time.Time) *MonitorSample {
	if cache == nil || modelID == "" || incidentAt.IsZero() {
		return nil
	}
	for _, model := range cache.Models {
		if model.ModelID != modelID {
			continue
		}
		var best *schema.PublicStatusSampleCache
		bestDistance := time.Duration(1<<63 - 1)
		consider := func(sample *schema.PublicStatusSampleCache) {
			if sample == nil || sample.CheckedAt.IsZero() {
				return
			}
			distance := sample.CheckedAt.Sub(incidentAt)
			if distance < 0 {
				distance = -distance
			}
			if distance < bestDistance || (distance == bestDistance && best != nil && sample.CheckedAt.After(best.CheckedAt)) {
				copy := *sample
				best = &copy
				bestDistance = distance
			}
		}
		consider(model.Latest)
		for i := range model.History {
			consider(&model.History[i])
		}
		if best == nil || bestDistance > maxMonitorCorrelationDistance {
			return nil
		}
		status := "unknown"
		interpretation := "public monitor status was unknown near this failure"
		if best.OK != nil {
			if *best.OK {
				status = "up"
				interpretation = "public monitor reported the model up; this may be a transient request-path failure"
			} else {
				status = "down"
				interpretation = "public monitor reported the model down; a broader model incident is possible"
			}
		}
		return &MonitorSample{
			Status:          status,
			SampleAt:        best.CheckedAt.UTC(),
			DistanceSeconds: int64(bestDistance / time.Second),
			LatencyMs:       best.LatencyMs,
			TTFTMs:          best.TTFTMs,
			ThroughputTps:   best.ThroughputTps,
			Interpretation:  interpretation,
		}
	}
	return nil
}

func sortedCounts(values map[string]int) []Count {
	counts := make([]Count, 0, len(values))
	for name, count := range values {
		counts = append(counts, Count{Name: name, Count: count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count == counts[j].Count {
			return counts[i].Name < counts[j].Name
		}
		return counts[i].Count > counts[j].Count
	})
	return counts
}

// FailureCategory is kept here as a small compatibility helper for callers
// that need to recognize a persisted snapshot failure without importing the
// normalizer package.
func FailureCategory(snap *schema.Snapshot) string {
	if snap == nil || snap.LastFailure == nil || snap.LastFailure.Category == "" {
		return ""
	}
	return secure.SafeField(snap.LastFailure.Category)
}
