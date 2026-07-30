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
		Session:       schema.SessionInfo{ID: "s1", Status: schema.SessionActive, LastEventAt: time.Now()},
		Provider:      provider,
		Model:         schema.ModelInfo{ID: "glm-5.1", ContextLength: i64p(200000)},
		LiveContext: &schema.LiveContext{
			Source:            "claude_statusline",
			ObservedAt:        time.Now(),
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
	}, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever // Test without colors for stable string matching
	line := vm.Line(rc)

	if !strings.HasPrefix(line, "FI glm-5.1") {
		t.Errorf("line = %q", line)
	}
	if !strings.Contains(line, "ctx 80%") {
		t.Errorf("line = %q", line)
	}
	if !strings.Contains(line, "cache 93%") {
		t.Errorf("line = %q", line)
	}
	if !strings.Contains(line, "WARN") {
		t.Errorf("line = %q", line)
	}
}

func TestLineRenderUnknownProviderHollowDot(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(false), &schema.GlobalState{
		Health: &schema.HealthCache{Status: "healthy", FetchedAt: time.Now()},
	}, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever
	line := vm.Line(rc)
	// P0-3: unconfirmed provider → Eligible=false → zero bytes output
	if line != "" {
		t.Errorf("unknown provider must produce no output, got %q", line)
	}
	if vm.Eligible {
		t.Errorf("unconfirmed provider must not be eligible")
	}
}

func TestExpandedRender(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(true), &schema.GlobalState{
		Health: &schema.HealthCache{Status: "healthy", FetchedAt: time.Now().Add(-18 * time.Second)},
	}, "", time.Now(), true, "", "")
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

func TestStandardRenderOmitsHistoricalDiagnosticSections(t *testing.T) {
	before, after := int64(180000), int64(120000)
	snap := fixtureSnapshot(true)
	snap.Compaction.LastResult = &schema.CompactionResult{At: time.Now(), PreTokens: &before, PostTokens: &after}
	snap.CacheAnalysis.CacheCreationShare = f64p(0.03)
	snap.CacheAnalysis.FreshInputShare = f64p(0.04)
	vm := BuildViewModel("0.1.0", snap, &schema.GlobalState{}, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever

	standard := vm.Standard(rc)
	if !strings.Contains(standard, "Pressure  WARN") {
		t.Fatalf("standard render missing core pressure:\n%s", standard)
	}
	for _, forbidden := range []string{"Cache Analysis", "Last Compaction", "Circuit Breakers", "Account Usage"} {
		if strings.Contains(standard, forbidden) {
			t.Errorf("standard render must omit %q:\n%s", forbidden, standard)
		}
	}
	detailed := vm.Expanded(rc)
	if !strings.Contains(detailed, "Cache Analysis") || !strings.Contains(detailed, "Last Compaction") {
		t.Errorf("detailed render missing diagnostics:\n%s", detailed)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(true), nil, "", time.Now(), true, "", "")
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
	vm := BuildViewModel("0.1.0", nil, nil, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	// P0-3: nil snapshot → Eligible=false → Line/Expanded return ""
	if vm.Line(rc) != "" || vm.Expanded(rc) != "" {
		t.Error("nil snapshot must render nothing (Eligible=false)")
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
	vm := BuildViewModel("0.1.0", snap, nil, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	// P0-3: unconfirmed provider → Eligible=false → Expanded() returns ""
	if vm.Eligible {
		t.Errorf("unconfirmed codex session must not be eligible")
	}
	out := vm.Expanded(rc)
	if out != "" {
		t.Errorf("unconfirmed session must render nothing, got %q", out)
	}
}

func TestLineWidthTiers(t *testing.T) {
	vm := BuildViewModel("0.1.0", fixtureSnapshot(true), &schema.GlobalState{
		Health: &schema.HealthCache{Status: "healthy", FetchedAt: time.Now()},
	}, "", time.Now(), true, "", "")

	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever

	// Wide: model, shield, cache, fresh, ctx, pressure
	rc.Width = 120
	wide := vm.Line(rc)
	if !strings.Contains(wide, "FI glm-5.1") {
		t.Errorf("wide should have model: %q", wide)
	}
	if !strings.Contains(wide, "cache 93%") {
		t.Errorf("wide should have cache: %q", wide)
	}
	if !strings.Contains(wide, "fresh") {
		t.Errorf("wide should have fresh: %q", wide)
	}

	// Medium: model, shield, cache, fresh, ctx, pressure (same set for this fixture)
	rc.Width = 80
	medium := vm.Line(rc)
	if !strings.Contains(medium, "FI glm-5.1") {
		t.Errorf("medium should have model: %q", medium)
	}
	if !strings.Contains(medium, "cache 93%") {
		t.Errorf("medium should have cache: %q", medium)
	}

	// Narrow: shield, cache, ctx only — no model, no fresh
	rc.Width = 40
	narrow := vm.Line(rc)
	if strings.Contains(narrow, "FI glm-5.1") {
		t.Errorf("narrow must not have model: %q", narrow)
	}
	if strings.Contains(narrow, "fresh") {
		t.Errorf("narrow must not have fresh: %q", narrow)
	}
	if !strings.Contains(narrow, "cache 93%") {
		t.Errorf("narrow should have cache: %q", narrow)
	}
	if !strings.Contains(narrow, "ctx 80%") {
		t.Errorf("narrow should have ctx: %q", narrow)
	}
}

func TestLineUnknownMetricsRenderAsDash(t *testing.T) {
	snap := &schema.Snapshot{
		Client:   schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:  schema.SessionInfo{ID: "s1", Status: schema.SessionActive, LastEventAt: time.Now()},
		Provider: schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true},
		Model:    schema.ModelInfo{ID: "glm-5.1"},
		Pressure: schema.PressureState{State: schema.PressureUnknown},
	}
	vm := BuildViewModel("0.1.0", snap, nil, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever
	rc.Width = 120
	line := vm.Line(rc)
	// Unknown cache → "cache —", unknown context → "ctx —"
	if !strings.Contains(line, "cache —") {
		t.Errorf("unknown cache should be dash: %q", line)
	}
	if !strings.Contains(line, "ctx —") {
		t.Errorf("unknown ctx should be dash: %q", line)
	}
	// Never fabricate zero
	if strings.Contains(line, "0%") {
		t.Errorf("must not fabricate 0%%: %q", line)
	}
}

func TestDisplayWidthIgnoresANSI(t *testing.T) {
	// Plain text
	if dw := displayWidth("hello"); dw != 5 {
		t.Errorf("plain: got %d", dw)
	}
	// ANSI-colored text — escape sequences occupy zero cells
	colored := "\033[32mhello\033[0m"
	if dw := displayWidth(colored); dw != 5 {
		t.Errorf("colored: got %d", dw)
	}
	// Unicode shield
	if dw := displayWidth("🛡"); dw != 1 {
		t.Errorf("shield: got %d", dw)
	}
}
