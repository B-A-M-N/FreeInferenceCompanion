package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PublicStatusURL is the unauthenticated service-status endpoint. It is kept
// separate from Client because status checks must never carry a provider key.
const PublicStatusURL = "https://status.freeinference.org/api/status"

// PublicStatusSource is the stable source label exposed by the CLI contract.
const PublicStatusSource = "https://status.freeinference.org"

const maxPublicStatusBody = 2 << 20

// PublicStatusResponse is the current public status payload shape.
type PublicStatusResponse struct {
	Models    []PublicStatusModel `json:"models"`
	Total     int                 `json:"total"`
	Healthy   int                 `json:"healthy"`
	Unhealthy int                 `json:"unhealthy"`
	Cycle     PublicStatusCycle   `json:"cycle"`
}

type PublicStatusModel struct {
	ModelID string             `json:"modelId"`
	Latest  PublicStatusSample `json:"latest"`
}

type PublicStatusSample struct {
	OK               bool     `json:"ok"`
	CheckedAt        string   `json:"checkedAt"`
	LatencyMs        *int64   `json:"latencyMs"`
	TTFTMs           *int64   `json:"ttftMs"`
	CompletionTokens *int64   `json:"completionTokens"`
	ThroughputTps    *float64 `json:"throughputTps"`
	Error            string   `json:"error"`
}

type PublicStatusCycle struct {
	OK        bool   `json:"ok"`
	CheckedAt string `json:"checkedAt"`
	Error     string `json:"error"`
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
	req.Header.Set("User-Agent", "freeinference-companion")

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
	if status.Total < 0 || status.Healthy < 0 || status.Unhealthy < 0 {
		return nil, errors.New("public status response contains negative model counts")
	}
	if status.Total == 0 && len(status.Models) == 0 {
		return nil, errors.New("public status response contains no model data")
	}
	return &status, nil
}
