package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func i64p(v int64) *int64     { return &v }
func f64p(v float64) *float64 { return &v }
func boolp(v bool) *bool      { return &v }

func fixtureSnapshot(confirmed bool) *schema.Snapshot {
	provider := schema.ProviderInfo{Name: "unknown", Confirmed: false, Source: "unresolved"}
	if confirmed {
		provider = schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true, Source: "FREEINFERENCE_API_KEY"}
	}
	turnStart := time.Now().Add(-24 * time.Second)
	readShare := 0.93
	return &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: "s1", Status: schema.SessionActive},
		Provider:      provider,
		Model:         schema.ModelInfo{ID: "glm-5.1", ContextLength: i64p(200000)},
		LiveContext: &schema.LiveContext{
			Source:            "claude_statusline",
			TotalInputTokens:  i64p(158000),
			TotalOutputTokens: i64p(2000),
			ContextWindowSize: i64p(200000),
			UsedPercentage:    f64p(80),
			LatestRequest: &schema.RequestUsage{
				FreshInputTokens:         i64p(5000),
				CacheReadInputTokens:     i64p(150000),
				CacheCreationInputTokens: i64p(5000),
				OutputTokens:             i64p(2000),
			},
		},
		Pressure: schema.PressureState{State: schema.PressureWarn},
		CacheAnalysis: &schema.CacheAnalysis{
			RequestSamples: 5,
			CacheReadShare: &readShare,
			Trend:          schema.TrendStable,
		},
		Activity: schema.ActivityState{
			TurnActive:    boolp(true),
			TurnStartedAt: &turnStart,
		},
	}
}

func TestLineRender(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(true), &schema.GlobalState{
		Health: &schema.HealthCache{Status: "healthy", FetchedAt: time.Now()},
	}, time.Now())
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever // Test without colors for stable string matching
	line := vm.Line(rc)

	if !strings.HasPrefix(line, "FI glm-5.1") {
		t.Errorf("line = %q", line)
	}
	if !strings.Contains(line, "ctx 80%") {
		t.Errorf("line = %q", line)
	}
	if !strings.Contains(line, "read 93%") {
		t.Errorf("line = %q", line)
	}
	if !strings.Contains(line, "WARN") {
		t.Errorf("line = %q", line)
	}
}

func TestLineRenderUnknownProviderHollowDot(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(false), &schema.GlobalState{
		Health: &schema.HealthCache{Status: "healthy", FetchedAt: time.Now()},
	}, time.Now())
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever
	line := vm.Line(rc)
	if !strings.Contains(line, "○") {
		t.Errorf("unknown provider must not show a green health symbol: %q", line)
	}
	if strings.Contains(line, "●") {
		t.Errorf("green symbol forbidden for unconfirmed provider: %q", line)
	}
}

func TestExpandedRender(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(true), &schema.GlobalState{
		Health: &schema.HealthCache{Status: "healthy", FetchedAt: time.Now().Add(-18 * time.Second)},
	}, time.Now())
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever
	out := vm.Expanded(rc)

	for _, want := range []string{
		"FREEINFERENCE glm-5.1",
		"Provider  confirmed",
		"Health  healthy",
		"Context  160K / 200K",
		"Fresh input  5K",
		"Cache read  150K",
		"Cache new  5K",
		"Output  2K",
		"Pressure  WARN",
		"Turn  ● active",
		"Last failure  none",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded missing %q:\n%s", want, out)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(true), nil, time.Now())
	data, err := vm.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["model_id"] != "glm-5.1" {
		t.Errorf("model_id = %v", decoded["model_id"])
	}
	if decoded["pressure_state"] != "warn" {
		t.Errorf("pressure_state = %v", decoded["pressure_state"])
	}
}

func TestNilSnapshotIsSafe(t *testing.T) {
	vm := BuildViewModel("0.1.0", nil, nil, time.Now())
	rc := DefaultRenderConfig()
	if vm.Line(rc) == "" || vm.Expanded(rc) == "" {
		t.Error("nil snapshot must still render")
	}
	if _, err := vm.JSON(); err != nil {
		t.Error(err)
	}
}

func TestMissingMetricsStayUnknown(t *testing.T) {
	snap := &schema.Snapshot{
		Client:   schema.ClientInfo{Type: schema.ClientCodex},
		Session:  schema.SessionInfo{ID: "c1", Status: schema.SessionActive},
		Model:    schema.ModelInfo{ID: "glm-5.1"},
		Pressure: schema.PressureState{State: schema.PressureUnknown},
	}
	vm := BuildViewModel("0.1.0", snap, nil, time.Now())
	rc := DefaultRenderConfig()
	out := vm.Expanded(rc)
	if !strings.Contains(out, "Context  unknown") {
		t.Errorf("missing context must render unknown:\n%s", out)
	}
	if !strings.Contains(out, "Turn  ? unknown") {
		t.Errorf("missing turn state must render unknown:\n%s", out)
	}
}
