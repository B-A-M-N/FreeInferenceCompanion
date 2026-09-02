package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
)

type cliStatusRoundTripper func(*http.Request) (*http.Response, error)

func (f cliStatusRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFIStatusJSONIsStableAndSeparatesFetchTime(t *testing.T) {
	client := &http.Client{Transport: cliStatusRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
  "models": [
    {"modelId":"healthy-model","latest":{"ok":true,"checkedAt":"2026-09-01T12:00:00Z","latencyMs":20}},
    {"modelId":"down-model","latest":{"ok":false,"checkedAt":"2026-09-01T12:00:00Z","latencyMs":900,"error":"backend unavailable"}}
  ],
  "total": 2,
  "healthy": 1,
  "unhealthy": 1,
  "cycle": {"ok":false,"checkedAt":"2026-09-01T12:00:00Z","error":"one model failed"}
}`)),
		}, nil
	})}
	var out, errOut strings.Builder
	if code := cmdFIStatusWithClient([]string{"--json", "--refresh"}, &out, &errOut, client); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	for _, want := range []string{`"overall": "degraded"`, `"models_up": 1`, `"models_down": 1`, `"models_total": 2`, `"source_checked_at": "2026-09-01T12:00:00Z"`, `"fetched_at":`, `"source": "https://status.freeinference.org"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("JSON output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "healthy-model") {
		t.Error("healthy models should be omitted unless --all is requested")
	}
}

func TestFIStatusUnavailableIsUnknown(t *testing.T) {
	client := &http.Client{Transport: cliStatusRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}
	var out, errOut strings.Builder
	if code := cmdFIStatusWithClient(nil, &out, &errOut, client); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "Overall: unknown") || strings.Contains(out.String(), "DOWN") {
		t.Errorf("unavailable status output = %q", out.String())
	}
}

func TestFIStatusBoundsUntrustedDisplayFields(t *testing.T) {
	status := api.PublicStatusResponse{
		Total: 1, Healthy: 0, Unhealthy: 1,
		Cycle: api.PublicStatusCycle{CheckedAt: "\033[31m" + strings.Repeat("x", 120)},
		Models: []api.PublicStatusModel{{
			ModelID: "model\033[2J",
			Latest:  api.PublicStatusSample{OK: false, CheckedAt: strings.Repeat("y", 120), Error: "\033[31mbackend unavailable"},
		}},
	}
	out := normalizeFIStatus(status, time.Unix(0, 0).UTC(), true)
	if strings.ContainsAny(out.SourceCheckedAt, "\033") || len(out.SourceCheckedAt) > 80 {
		t.Fatalf("source timestamp was not sanitized/bounded: %q", out.SourceCheckedAt)
	}
	if len(out.Models) != 1 || strings.ContainsAny(out.Models[0].ID+out.Models[0].CheckedAt+out.Models[0].Error, "\033") {
		t.Fatalf("model fields were not sanitized: %#v", out.Models)
	}
	if len(out.Models[0].CheckedAt) > 80 {
		t.Fatalf("model timestamp was not bounded: %q", out.Models[0].CheckedAt)
	}
}

func TestFIStatusUsesAggregateCountsOverDisplaySubset(t *testing.T) {
	status := api.PublicStatusResponse{
		Total: 10, Healthy: 9, Unhealthy: 1,
		Models: []api.PublicStatusModel{{
			ModelID: "displayed-model",
			Latest:  api.PublicStatusSample{OK: true},
		}},
	}
	out := normalizeFIStatus(status, time.Unix(0, 0).UTC(), false)
	if out.ModelsUp != 9 || out.ModelsDown != 1 || out.ModelsTotal != 10 {
		t.Fatalf("aggregate counts were overridden by display subset: %+v", out)
	}
}
