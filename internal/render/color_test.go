package render

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func colorFixture() *schema.Snapshot {
	return &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: "s1", Status: schema.SessionActive, LastEventAt: time.Now()},
		Provider:      schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true, Source: "FREEINFERENCE_API_KEY"},
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
			CacheReadShare: f64p(0.93),
			Trend:          schema.TrendStable,
		},
	}
}

// --- ColorMode parsing ---

func TestParseColorMode_Auto(t *testing.T) {
	if got := ParseColorMode("auto"); got != ColorAuto {
		t.Errorf("ParseColorMode(\"auto\") = %d, want %d", got, ColorAuto)
	}
}

func TestParseColorMode_Always(t *testing.T) {
	for _, s := range []string{"always", "ALWAYS", "yes", "on", "true"} {
		if got := ParseColorMode(s); got != ColorAlways {
			t.Errorf("ParseColorMode(%q) = %d, want ColorAlways", s, got)
		}
	}
}

func TestParseColorMode_Never(t *testing.T) {
	for _, s := range []string{"never", "NEVER", "no", "off", "false", "none"} {
		if got := ParseColorMode(s); got != ColorNever {
			t.Errorf("ParseColorMode(%q) = %d, want ColorNever", s, got)
		}
	}
}

func TestParseColorMode_Unknown(t *testing.T) {
	if got := ParseColorMode("bogus"); got != ColorAuto {
		t.Errorf("ParseColorMode(\"bogus\") = %d, want ColorAuto", got)
	}
}

// --- NO_COLOR / FORCE_COLOR ---

func TestApplyEnv_RespectsExplicitFlag(t *testing.T) {
	// Even with NO_COLOR set, an explicit --color flag should win.
	os.Unsetenv("NO_COLOR")
	os.Unsetenv("FORCE_COLOR")
	if got := ApplyEnv(ColorNever); got != ColorNever {
		t.Errorf("ApplyEnv(ColorNever) = %d, want ColorNever", got)
	}
}

func TestApplyEnv_NOColorDisables(t *testing.T) {
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")
	if got := ApplyEnv(ColorAuto); got != ColorNever {
		t.Errorf("ApplyEnv(ColorAuto) with NO_COLOR = %d, want ColorNever", got)
	}
}

func TestApplyEnv_FORCEColorEnables(t *testing.T) {
	os.Unsetenv("NO_COLOR")
	os.Setenv("FORCE_COLOR", "1")
	defer os.Unsetenv("FORCE_COLOR")
	if got := ApplyEnv(ColorAuto); got != ColorAlways {
		t.Errorf("ApplyEnv(ColorAuto) with FORCE_COLOR = %d, want ColorAlways", got)
	}
}

// --- Color vs Monochrome output ---

func TestColorize_NeverProducesNoANSI(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorNever}
	colored := rc.colorize("hello", ColorRed)
	if colored != "hello" {
		t.Errorf("ColorNever should produce no ANSI: %q", colored)
	}
}

func TestColorize_AutoProducesNoANSI(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorAuto}
	colored := rc.colorize("hello", ColorRed)
	if colored != "hello" {
		t.Errorf("ColorAuto should produce no ANSI in render config: %q", colored)
	}
}

func TestColorAlwaysEmitsANSI(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorAlways}
	colored := rc.colorize("hello", ColorRed)
	if !strings.Contains(colored, "\033[91m") {
		t.Errorf("ColorAlways should emit ANSI codes: %q", colored)
	}
	if !strings.Contains(colored, ColorReset) {
		t.Errorf("ColorAlways should emit reset: %q", colored)
	}
	if colored != "\033[91mhello\033[0m" {
		t.Errorf("ColorAlways = %q, want %q", colored, "\033[91mhello\033[0m")
	}
}

// --- Symbol sets: ASCII vs Unicode ---

func TestSymbolSet_ASCII(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorNever, UseASCII: true}
	s := rc.syms()
	if s.Shield != "[+]" {
		t.Errorf("ASCII shield = %q, want [/+]", s.Shield)
	}
	if s.TurnActive != "*" {
		t.Errorf("ASCII turn active = %q, want *", s.TurnActive)
	}
	if s.Arrow != "->" {
		t.Errorf("ASCII arrow = %q, want ->", s.Arrow)
	}
}

func TestSymbolSet_Unicode(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorNever, UseASCII: false}
	s := rc.syms()
	if s.Shield != "\U0001F6E1\uFE0E" {
		t.Errorf("Unicode shield = %q, want shield", s.Shield)
	}
	if s.TurnActive != "\u25CF" {
		t.Errorf("Unicode turn active = %q, want circle", s.TurnActive)
	}
	if s.Arrow != "\u2192" {
		t.Errorf("Unicode arrow = %q, want right arrow", s.Arrow)
	}
}

// --- Line output: monochrome vs colored ---

func TestLine_MonochromeHasNoANSI(t *testing.T) {
	snap := colorFixture()
	vm := BuildViewModel("0.1.0", snap, nil, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever
	rc.Width = 120
	line := vm.Line(rc)
	if strings.Contains(line, "\033[") {
		t.Errorf("monochrome line should not contain ANSI codes: %q", line)
	}
	if !strings.Contains(line, "FI glm-5.1") {
		t.Errorf("monochrome line missing model: %q", line)
	}
	if !strings.Contains(line, "ctx 80%") {
		t.Errorf("monochrome line missing context: %q", line)
	}
}

func TestLine_ColoredHasANSI(t *testing.T) {
	snap := colorFixture()
	vm := BuildViewModel("0.1.0", snap, nil, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorAlways
	rc.Width = 120
	line := vm.Line(rc)
	if !strings.Contains(line, "\033[") {
		t.Errorf("colored line should contain ANSI codes: %q", line)
	}
	if !strings.Contains(line, "FI glm-5.1") {
		t.Errorf("colored line missing model: %q", line)
	}
}

// --- Expanded output: monochrome vs colored ---

func TestExpanded_MonochromeHasNoANSI(t *testing.T) {
	snap := colorFixture()
	vm := BuildViewModel("0.1.0", snap, nil, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorNever
	out := vm.Expanded(rc)
	if strings.Contains(out, "\033[") {
		t.Errorf("monochrome expanded should not contain ANSI codes: %q", out)
	}
}

func TestExpanded_ColoredHasANSI(t *testing.T) {
	snap := colorFixture()
	vm := BuildViewModel("0.1.0", snap, nil, "", time.Now(), true, "", "")
	rc := DefaultRenderConfig()
	rc.ColorMode = ColorAlways
	out := vm.Expanded(rc)
	if !strings.Contains(out, "\033[") {
		t.Errorf("colored expanded should contain ANSI codes: %q", out)
	}
}

// --- PressureSymbol colors ---

func TestPressureSymbol_Colors(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorAlways}

	tests := []struct {
		state    string
		hasColor bool
	}{
		{schema.PressureHealthy, true},
		{schema.PressureWatch, true},
		{schema.PressureWarn, true},
		{schema.PressureCritical, true},
		{schema.PressureRecovering, true},
		{schema.PressureUnknown, true},
	}

	for _, tt := range tests {
		sym := rc.PressureSymbol(tt.state, false)
		if tt.hasColor && !strings.Contains(sym, "\033[") {
			t.Errorf("PressureSymbol(%q) should contain ANSI: %q", tt.state, sym)
		}
	}
}

func TestPressureSymbol_Monochrome(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorNever}

	for _, state := range []string{
		schema.PressureHealthy,
		schema.PressureWatch,
		schema.PressureWarn,
		schema.PressureCritical,
		schema.PressureRecovering,
		schema.PressureUnknown,
	} {
		sym := rc.PressureSymbol(state, false)
		if strings.Contains(sym, "\033[") {
			t.Errorf("PressureSymbol(%q) monochrome should not contain ANSI: %q", state, sym)
		}
	}
}

// --- ContextShieldSymbol colors ---

func TestContextShieldSymbol_Colors(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorAlways}

	tests := []struct {
		pct       float64
		hasEscape bool
	}{
		{50, true}, // white
		{70, true}, // orange
		{90, true}, // red
	}

	for _, tt := range tests {
		sym := rc.ContextShieldSymbol(f64p(tt.pct))
		if tt.hasEscape && !strings.Contains(sym, "\033[") {
			t.Errorf("ContextShieldSymbol(%.0f) should contain ANSI: %q", tt.pct, sym)
		}
	}

	// nil → no color coding for value, but still has gray
	sym := rc.ContextShieldSymbol(nil)
	if !strings.Contains(sym, "\033[") {
		t.Errorf("ContextShieldSymbol(nil) should still be colorized (gray): %q", sym)
	}
}

// --- TrendSymbol colors ---

func TestTrendSymbol_Colors(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorAlways}

	for _, trend := range []string{"rising", "declining", "stable", "unknown"} {
		sym := rc.TrendSymbol(trend)
		if !strings.Contains(sym, "\033[") {
			t.Errorf("TrendSymbol(%q) should contain ANSI: %q", trend, sym)
		}
	}
}

func TestTrendSymbol_Monochrome(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorNever}

	for _, trend := range []string{"rising", "declining", "stable", "unknown"} {
		sym := rc.TrendSymbol(trend)
		if strings.Contains(sym, "\033[") {
			t.Errorf("TrendSymbol(%q) monochrome should not contain ANSI: %q", trend, sym)
		}
	}
}

// --- ASCII trend symbols ---

func TestTrendSymbol_ASCII(t *testing.T) {
	rc := RenderConfig{ColorMode: ColorNever, UseASCII: true}

	tests := []struct {
		trend string
		want  string
	}{
		{"rising", "+"},
		{"declining", "-"},
		{"stable", "="},
		{"unknown", "?"},
	}

	for _, tt := range tests {
		got := rc.TrendSymbol(tt.trend)
		if got != tt.want {
			t.Errorf("TrendSymbol(%q) ASCII = %q, want %q", tt.trend, got, tt.want)
		}
	}
}

// --- displayWidth ignores ANSI ---

func TestDisplayWidth_IgnoresANSI(t *testing.T) {
	if got := displayWidth("hello"); got != 5 {
		t.Errorf("plain: got %d, want 5", got)
	}

	colored := "\033[31mhello\033[0m"
	if got := displayWidth(colored); got != 5 {
		t.Errorf("colored: got %d, want 5", got)
	}
}
