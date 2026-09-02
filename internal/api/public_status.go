package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

// PublicStatusURL is the unauthenticated service-status endpoint. It is kept
// separate from Client because status checks must never carry a provider key.
const PublicStatusURL = "https://status.freeinference.org/api/status"

// PublicStatusSource is the stable source label exposed by the CLI contract.
const PublicStatusSource = "https://status.freeinference.org"

const maxPublicStatusBody = 2 << 20

// PublicStatusExpectedInterval is the monitor's documented probe cadence.
const PublicStatusExpectedInterval = 20 * time.Minute

// PublicStatusStaleAfter is the maximum age accepted for a monitor cycle.
// It allows one missed probe plus network/scheduling grace without presenting
// an old snapshot as the current service state.
const PublicStatusStaleAfter = 45 * time.Minute

// PublicStatusResponse is the current public status payload shape.
type PublicStatusResponse struct {
	Models    []PublicStatusModel `json:"models"`
	Total     int                 `json:"total"`
	Healthy   int                 `json:"healthy"`
	Unhealthy int                 `json:"unhealthy"`
	Cycle     PublicStatusCycle   `json:"cycle"`
}

type PublicStatusModel struct {
	ModelID     string               `json:"modelId"`
	Latest      *PublicStatusSample  `json:"latest"`
	History     []PublicStatusSample `json:"history"`
	Spark       []PublicStatusSample `json:"spark"`
	UptimeRatio *float64             `json:"uptimeRatio"`

	// ValidationError is populated for a malformed model record after the
	// document has otherwise passed validation. It is intentionally excluded
	// from JSON and lets the CLI report that model as unknown while retaining
	// useful data for other models.
	ValidationError string `json:"-"`
}

type PublicStatusSample struct {
	OK               *bool    `json:"ok"`
	CheckedAt        string   `json:"checkedAt"`
	LatencyMs        *int64   `json:"latencyMs"`
	TTFTMs           *int64   `json:"ttftMs"`
	CompletionTokens *int64   `json:"completionTokens"`
	ThroughputTps    *float64 `json:"throughputTps"`
	Error            string   `json:"error"`
}

type PublicStatusCycle struct {
	OK        *bool  `json:"ok"`
	CheckedAt string `json:"checkedAt"`
	Error     string `json:"error"`

	ValidationError string `json:"-"`
}

// FetchPublicStatus fetches the unauthenticated public status endpoint.
func FetchPublicStatus(ctx context.Context) (*PublicStatusResponse, error) {
	return FetchPublicStatusWithClient(ctx, nil)
}

// FetchPublicStatusWithClient is the injectable form used by tests and
// embedders. The request is always a GET to PublicStatusURL and never sets an
// Authorization header.
func FetchPublicStatusWithClient(ctx context.Context, client *http.Client) (*PublicStatusResponse, error) {
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PublicStatusURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build public status request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "FreeInference-Companion/"+version.Version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("public status request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public status endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPublicStatusBody+1))
	if err != nil {
		return nil, fmt.Errorf("read public status response: %w", err)
	}
	if len(body) > maxPublicStatusBody {
		return nil, errors.New("public status response exceeds the supported size limit")
	}
	var status PublicStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("decode public status response: %w", err)
	}
	if err := status.Validate(); err != nil {
		return nil, err
	}
	return &status, nil
}

// Validate checks document-level invariants and annotates malformed model
// records without discarding valid records from the same response.
func (s *PublicStatusResponse) Validate() error {
	if s == nil {
		return errors.New("public status response is nil")
	}
	s.Cycle.ValidationError = ""
	if s.Total < 0 || s.Healthy < 0 || s.Unhealthy < 0 {
		return errors.New("public status response contains negative model counts")
	}
	if s.Total > 0 && (s.Healthy > s.Total || s.Unhealthy > s.Total || s.Healthy+s.Unhealthy > s.Total) {
		return errors.New("public status response contains inconsistent model counts")
	}
	if s.Total == 0 && len(s.Models) == 0 {
		return errors.New("public status response contains no model data")
	}

	seen := make(map[string]struct{}, len(s.Models))
	for i := range s.Models {
		model := &s.Models[i]
		model.ValidationError = ""
		id := strings.TrimSpace(model.ModelID)
		if id == "" {
			model.ValidationError = "model id is empty"
			continue
		}
		if _, exists := seen[id]; exists {
			return errors.New("public status response contains duplicate model ids")
		}
		seen[id] = struct{}{}
		model.ModelID = id
		model.ValidationError = validatePublicStatusModel(*model)
	}
	if s.Cycle.CheckedAt != "" {
		if _, err := parsePublicStatusTime(s.Cycle.CheckedAt); err != nil {
			s.Cycle.ValidationError = "cycle checked_at is invalid"
		}
	} else {
		s.Cycle.ValidationError = "cycle checked_at is missing"
	}
	return nil
}

func validatePublicStatusModel(model PublicStatusModel) string {
	if model.Latest == nil {
		return "latest sample is missing"
	}
	if err := validatePublicStatusSample(*model.Latest, true); err != nil {
		return err.Error()
	}
	for _, sample := range model.History {
		if err := validatePublicStatusSample(sample, true); err != nil {
			return "history sample is invalid: " + err.Error()
		}
	}
	for _, sample := range model.Spark {
		if err := validatePublicStatusSample(sample, true); err != nil {
			return "spark sample is invalid: " + err.Error()
		}
	}
	if model.UptimeRatio != nil && (!finite(*model.UptimeRatio) || *model.UptimeRatio < 0 || *model.UptimeRatio > 1) {
		return "uptime ratio is outside [0,1]"
	}
	return ""
}

func validatePublicStatusSample(sample PublicStatusSample, requireTimestamp bool) error {
	if requireTimestamp {
		if _, err := parsePublicStatusTime(sample.CheckedAt); err != nil {
			return errors.New("checked_at is invalid")
		}
	}
	if sample.LatencyMs != nil && *sample.LatencyMs < 0 {
		return errors.New("latency is negative")
	}
	if sample.TTFTMs != nil && *sample.TTFTMs < 0 {
		return errors.New("ttft is negative")
	}
	if sample.CompletionTokens != nil && *sample.CompletionTokens < 0 {
		return errors.New("completion tokens are negative")
	}
	if sample.ThroughputTps != nil && (!finite(*sample.ThroughputTps) || *sample.ThroughputTps < 0) {
		return errors.New("throughput is invalid")
	}
	return nil
}

func parsePublicStatusTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("timestamp is missing")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
