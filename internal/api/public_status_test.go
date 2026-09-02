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
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
  "models": [{"modelId":"m1","latest":{"ok":true,"checkedAt":"2026-09-01T12:00:00Z","latencyMs":42,"ttftMs":7,"completionTokens":12,"throughputTps":3.5,"error":null}}],
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
	if status.Models[0].Latest.LatencyMs == nil || *status.Models[0].Latest.LatencyMs != 42 {
		t.Fatalf("latency = %v", status.Models[0].Latest.LatencyMs)
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
