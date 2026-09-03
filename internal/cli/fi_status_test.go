package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	checkedAt := time.Now().UTC().Add(-4 * time.Minute).Format(time.RFC3339Nano)
	client := &http.Client{Transport: cliStatusRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{
  "models": [
    {"modelId":"healthy-model","latest":{"ok":true,"checkedAt":"%s","latencyMs":20}},
    {"modelId":"down-model","latest":{"ok":false,"checkedAt":"%s","latencyMs":900,"error":"backend unavailable"}}
  ],
  "total": 2,
  "healthy": 1,
  "unhealthy": 1,
  "cycle": {"ok":false,"checkedAt":"%s","error":"one model failed"}
}`, checkedAt, checkedAt, checkedAt))),
		}, nil
	})}
	var out, errOut strings.Builder
	if code := cmdFIStatusWithClient([]string{"--json", "--refresh"}, &out, &errOut, client); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	for _, want := range []string{`"schema_version": 1`, `"overall": "degraded"`, `"models_up": 1`, `"models_down": 1`, `"models_unknown": 0`, `"models_total": 2`, `"monitor":`, `"fetched_at":`, `"source": "https://status.freeinference.org"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("JSON output missing %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(out.String(), "healthy-model") {
		t.Error("healthy models must be shown by default")
	}
}

func TestFIStatusUnavailableIsUnknown(t *testing.T) {
	client := &http.Client{Transport: cliStatusRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}
	var out, errOut strings.Builder
	if code := cmdFIStatusWithClient(nil, &out, &errOut, client); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "FreeInference Status — UNKNOWN") || strings.Contains(out.String(), "DOWN") {
		t.Errorf("unavailable status output = %q", out.String())
	}
}

func TestFIStatusBoundsUntrustedDisplayFields(t *testing.T) {
	ok := false
	checkedAt := "2026-09-02T06:00:00Z"
	status := api.PublicStatusResponse{
		Total: 1, Healthy: 0, Unhealthy: 1,
		Cycle: api.PublicStatusCycle{OK: &ok, CheckedAt: checkedAt},
		Models: []api.PublicStatusModel{{
			ModelID: "model\033[2J",
			Latest:  &api.PublicStatusSample{OK: &ok, CheckedAt: checkedAt, Error: "\033[31mbackend unavailable"},
		}},
	}
	out := normalizeFIStatus(status, time.Unix(0, 0).UTC(), true)
	if strings.ContainsAny(out.SourceCheckedAt, "\033") || len(out.SourceCheckedAt) == 0 {
		t.Fatalf("source timestamp was not normalized: %q", out.SourceCheckedAt)
	}
	if len(out.Models) != 1 || strings.ContainsAny(out.Models[0].ID+out.Models[0].CheckedAt+out.Models[0].Error, "\033") {
		t.Fatalf("model fields were not sanitized: %#v", out.Models)
	}
	if len(out.Models[0].CheckedAt) > 80 {
		t.Fatalf("model timestamp was not bounded: %q", out.Models[0].CheckedAt)
	}
}

func TestFIStatusUsesAggregateCountsOverDisplaySubset(t *testing.T) {
	ok := true
	checkedAt := "2026-09-02T06:00:00Z"
	status := api.PublicStatusResponse{
		Total: 10, Healthy: 9, Unhealthy: 1,
		Cycle: api.PublicStatusCycle{OK: &ok, CheckedAt: checkedAt},
		Models: []api.PublicStatusModel{{
			ModelID: "displayed-model",
			Latest:  &api.PublicStatusSample{OK: &ok, CheckedAt: checkedAt},
		}},
	}
	out := normalizeFIStatus(status, time.Unix(0, 0).UTC(), false)
	if out.ModelsUp != 9 || out.ModelsDown != 1 || out.ModelsTotal != 10 {
		t.Fatalf("aggregate counts were overridden by display subset: %+v", out)
	}
}

func TestFIStatusMissingTelemetryIsUnknownNotDown(t *testing.T) {
	ok := true
	checkedAt := "2026-09-02T06:00:00Z"
	status := api.PublicStatusResponse{
		Total: 4, Healthy: 1, Unhealthy: 0,
		Cycle: api.PublicStatusCycle{OK: &ok, CheckedAt: checkedAt},
		Models: []api.PublicStatusModel{
			{ModelID: "missing-ok", Latest: &api.PublicStatusSample{CheckedAt: checkedAt}},
			{ModelID: "null-ok", Latest: &api.PublicStatusSample{OK: nil, CheckedAt: checkedAt}},
			{ModelID: "missing-latest", Latest: nil},
			{ModelID: "good", Latest: &api.PublicStatusSample{OK: &ok, CheckedAt: checkedAt}},
		},
	}
	out := normalizeFIStatusAt(status, time.Unix(0, 0).UTC(), time.Unix(0, 0).UTC(), false)
	if out.ModelsUnknown != 3 || out.ModelsDown != 0 {
		t.Fatalf("missing telemetry became an outage: %+v", out)
	}
	for _, model := range out.Models {
		if model.Status == fiModelDown && model.ID != "" {
			t.Fatalf("unknown model rendered down: %+v", model)
		}
	}
}

func TestFIStatusFreshnessAndFailDegraded(t *testing.T) {
	old := time.Now().UTC().Add(-(api.PublicStatusStaleAfter + time.Minute)).Format(time.RFC3339Nano)
	body := fmt.Sprintf(`{"models":[{"modelId":"down","latest":{"ok":false,"checkedAt":"%s","error":"timeout"}}],"total":1,"healthy":0,"unhealthy":1,"cycle":{"ok":true,"checkedAt":"%s"}}`, old, old)
	client := &http.Client{Transport: cliStatusRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	var out, errOut strings.Builder
	if code := cmdFIStatusWithClient(nil, &out, &errOut, client); code != 1 || !strings.Contains(out.String(), "Monitor: STALE") {
		t.Fatalf("stale status = code %d, output %q, stderr %q", code, out.String(), errOut.String())
	}

	fresh := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	body = fmt.Sprintf(`{"models":[{"modelId":"down","latest":{"ok":false,"checkedAt":"%s","error":"timeout"}}],"total":1,"healthy":0,"unhealthy":1,"cycle":{"ok":true,"checkedAt":"%s"}}`, fresh, fresh)
	client.Transport = cliStatusRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	out.Reset()
	if code := cmdFIStatusWithClient([]string{"--fail-degraded"}, &out, &errOut, client); code != 1 {
		t.Fatalf("--fail-degraded exit = %d, output %q", code, out.String())
	}
}

func TestFIStatusHumanFormattingAndWidths(t *testing.T) {
	value := int64(21776)
	if got := formatMilliseconds(&value); got != "21.8s" {
		t.Fatalf("latency formatting = %q", got)
	}
	value = 249
	if got := formatMilliseconds(&value); got != "249ms" {
		t.Fatalf("ttft formatting = %q", got)
	}
	out := fiStatusOutput{
		Overall: fiOverallOperational, ModelsUp: 1, ModelsTotal: 1,
		Monitor: fiMonitorOutput{Status: fiMonitorHealthy, CheckedAt: "2026-09-02T06:00:00Z"},
		Source:  api.PublicStatusSource,
		Models:  []fiStatusModel{{ID: "model", Status: fiModelUp, LatencyMs: &value}},
	}
	var narrow, wide strings.Builder
	renderFIStatusHuman(&narrow, out, false, 60)
	renderFIStatusHuman(&wide, out, false, 120)
	if !strings.Contains(narrow.String(), "uptime") || !strings.Contains(wide.String(), "THROUGHPUT") {
		t.Fatalf("width-specific tables missing: narrow=%q wide=%q", narrow.String(), wide.String())
	}
}

func TestFIStatusNormalizesRealPublicFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "api", "testdata", "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var status api.PublicStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(status.Models) != 9 || status.Models[0].UptimeRatio == nil || len(status.Models[0].History) == 0 || len(status.Models[0].Spark) == 0 {
		t.Fatalf("fixture fields did not parse: %+v", status)
	}

	cycleAt, _ := time.Parse(time.RFC3339Nano, status.Cycle.CheckedAt)
	out := normalizeFIStatusAt(status, cycleAt.Add(5*time.Minute), cycleAt.Add(5*time.Minute), false)
	if out.Overall != fiOverallDegraded || out.ModelsUp != 8 || out.ModelsDown != 1 || out.ModelsUnknown != 0 || len(out.Models) != 9 {
		t.Fatalf("fixture normalization = %+v", out)
	}
	if out.Monitor.Status != fiMonitorHealthy || out.Monitor.AgeSeconds != 300 {
		t.Fatalf("fixture monitor = %+v", out.Monitor)
	}

	var bge, diffusion, qwen fiStatusModel
	for _, model := range out.Models {
		switch model.ID {
		case "bge-m3":
			bge = model
		case "diffusiongemma":
			diffusion = model
		case "qwen3.6-35b":
			qwen = model
		}
	}
	if bge.Status != fiModelUp || bge.TTFTMs != nil || bge.Throughput != nil || bge.UptimePct == nil {
		t.Fatalf("embedding telemetry = %+v", bge)
	}
	if diffusion.Status != fiModelDown || diffusion.Error != "timeout" || diffusion.CurrentStateFor == nil || *diffusion.CurrentStateFor != 0 || diffusion.StateDurationAtLeast {
		t.Fatalf("down model telemetry = %+v", diffusion)
	}
	if diffusion.ObservedStateFor == nil || *diffusion.ObservedStateFor != 0 || diffusion.StateTransitionInterval == nil || *diffusion.StateTransitionInterval < 1190 {
		t.Fatalf("down model observed duration = %+v", diffusion)
	}
	if qwen.Throughput == nil || *qwen.Throughput != 254.53 || qwen.LatencyMs == nil || *qwen.LatencyMs != 1194 {
		t.Fatalf("generation telemetry = %+v", qwen)
	}

	problems := normalizeFIStatusAt(status, cycleAt.Add(5*time.Minute), cycleAt.Add(5*time.Minute), true)
	if len(problems.Models) != 1 || problems.Models[0].ID != "diffusiongemma" {
		t.Fatalf("problems filter = %+v", problems.Models)
	}
}

func TestFIStatusAcceptsUnknownFutureFields(t *testing.T) {
	var status api.PublicStatusResponse
	if err := json.Unmarshal([]byte(`{"total":1,"healthy":1,"unhealthy":0,"future":{"ignored":true},"cycle":{"ok":true,"checkedAt":"2026-09-02T06:00:00Z"},"models":[{"modelId":"future-model","latest":{"ok":true,"checkedAt":"2026-09-02T06:00:00Z","futureMetric":123}}]}`), &status); err != nil {
		t.Fatal(err)
	}
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
}
