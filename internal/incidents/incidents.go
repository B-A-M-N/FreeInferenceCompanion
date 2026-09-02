// Package incidents aggregates bounded turn-failure events for local
// diagnostics. It never reads or returns raw error bodies.
package incidents

import (
	"sort"
	"time"

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
	At                time.Time `json:"at"`
	Client            string    `json:"client"`
	SessionID         string    `json:"session_id"`
	Model             string    `json:"model,omitempty"`
	Provider          string    `json:"provider,omitempty"`
	Category          string    `json:"category"`
	HTTPStatus        *int      `json:"http_status,omitempty"`
	Retryable         *bool     `json:"retryable,omitempty"`
	TransportClass    string    `json:"transport_class,omitempty"`
	ProviderErrorType string    `json:"provider_error_type,omitempty"`
	ErrorOrigin       string    `json:"error_origin,omitempty"`
	RetryAfterSeconds *int64    `json:"retry_after_seconds,omitempty"`
	RequestReference  string    `json:"request_reference,omitempty"`
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
	modelCounts := make(map[string]int)
	clientCounts := make(map[string]int)
	for _, entry := range index.Sessions {
		if filter.Client != "" && entry.Client != filter.Client {
			continue
		}
		if filter.SessionID != "" && entry.SessionID != filter.SessionID {
			continue
		}
		if filter.Model != "" && entry.ModelID != filter.Model {
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
			incident := Incident{
				At:                event.At.UTC(),
				Client:            secure.SanitizeField(entry.Client),
				SessionID:         secure.MaskSessionID(entry.SessionID),
				Model:             secure.SanitizeField(event.Model),
				Provider:          secure.SanitizeField(event.Provider),
				Category:          secure.SanitizeField(event.Detail),
				HTTPStatus:        event.HTTPStatus,
				Retryable:         event.Retryable,
				TransportClass:    secure.SanitizeField(event.TransportClass),
				ProviderErrorType: secure.SanitizeField(event.ProviderErrorType),
				ErrorOrigin:       secure.SanitizeField(event.ErrorOrigin),
				RetryAfterSeconds: event.RetryAfterSeconds,
				RequestReference:  secure.SanitizeField(event.RequestReference),
			}
			if incident.Model == "" {
				incident.Model = secure.SanitizeField(entry.ModelID)
			}
			if incident.Category == "" {
				incident.Category = "unknown"
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
	report.ByModel = sortedCounts(modelCounts)
	report.ByClient = sortedCounts(clientCounts)
	return report, nil
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
	return secure.SanitizeField(snap.LastFailure.Category)
}
