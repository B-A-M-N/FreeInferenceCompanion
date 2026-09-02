package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type publicStatusRoundTripper func(*http.Request) (*http.Response, error)

func (f publicStatusRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFetchPublicStatusUsesUnauthenticatedGET(t *testing.T) {
	client := &http.Client{Transport: publicStatusRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.String() != PublicStatusURL {
			t.Errorf("URL = %s, want %s", r.URL, PublicStatusURL)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("public status request carried authorization header %q", got)
		}
		if got := r.Header.Get("X-Session-ID"); got != "" {
			t.Errorf("public status request carried trace header %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
					"models": [{"modelId":"m1","latest":{"ok":true,"checkedAt":"2026-09-01T12:00:00Z","latencyMs":42,"ttftMs":7,"completionTokens":12,"throughputTps":3.5,"error":null},"uptimeRatio":0.99}],
  "total": 1,
  "healthy": 1,
  "unhealthy": 0,
  "cycle": {"ok": true, "checkedAt":"2026-09-01T12:00:00Z", "error":null}
}`)),
		}, nil
	})}

	status, err := FetchPublicStatusWithClient(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if status.Total != 1 || status.Healthy != 1 || len(status.Models) != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Models[0].Latest == nil || status.Models[0].Latest.LatencyMs == nil || *status.Models[0].Latest.LatencyMs != 42 {
		t.Fatalf("latency = %v", status.Models[0].Latest)
	}
	if status.Models[0].UptimeRatio == nil || *status.Models[0].UptimeRatio != 0.99 {
		t.Fatalf("uptime ratio = %v", status.Models[0].UptimeRatio)
	}
}

func TestPublicStatusValidationKeepsMalformedModelsLocal(t *testing.T) {
	good := true
	badMetric := int64(-1)
	status := PublicStatusResponse{
		Total: 2, Healthy: 2,
		Cycle: PublicStatusCycle{OK: &good, CheckedAt: "2026-09-01T12:00:00Z"},
		Models: []PublicStatusModel{
			{ModelID: "good", Latest: &PublicStatusSample{OK: &good, CheckedAt: "2026-09-01T12:00:00Z"}},
			{ModelID: "bad", Latest: &PublicStatusSample{OK: &good, CheckedAt: "2026-09-01T12:00:00Z", LatencyMs: &badMetric}},
		},
	}
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	if status.Models[0].ValidationError != "" || status.Models[1].ValidationError == "" {
		t.Fatalf("model validation annotations = %+v", status.Models)
	}
}

func TestPublicStatusValidationRejectsDocumentCorruption(t *testing.T) {
	ok := true
	status := PublicStatusResponse{
		Total: 1, Healthy: 1, Unhealthy: 1,
		Cycle: PublicStatusCycle{OK: &ok, CheckedAt: "2026-09-01T12:00:00Z"},
		Models: []PublicStatusModel{
			{ModelID: "same", Latest: &PublicStatusSample{OK: &ok, CheckedAt: "2026-09-01T12:00:00Z"}},
			{ModelID: "same", Latest: &PublicStatusSample{OK: &ok, CheckedAt: "2026-09-01T12:00:00Z"}},
		},
	}
	if err := status.Validate(); err == nil {
		t.Fatal("duplicate IDs and inconsistent counts must reject the document")
	}
}

func TestPublicStatusNullableStatusDoesNotBecomeFalse(t *testing.T) {
	status := PublicStatusResponse{
		Total: 1, Healthy: 0, Unhealthy: 0,
		Models: []PublicStatusModel{{ModelID: "unknown", Latest: &PublicStatusSample{CheckedAt: "2026-09-01T12:00:00Z"}}},
	}
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	if status.Models[0].Latest.OK != nil {
		t.Fatal("missing ok must remain nil")
	}
}

func TestFetchPublicStatusRejectsOversizedResponse(t *testing.T) {
	client := &http.Client{Transport: publicStatusRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", maxPublicStatusBody+1))),
		}, nil
	})}
	if _, err := FetchPublicStatusWithClient(context.Background(), client); err == nil {
		t.Fatal("oversized public status response must fail")
	}
}
