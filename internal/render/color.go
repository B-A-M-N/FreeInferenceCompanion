// Package render builds a normalized view model from raw state and renders
// it as a single line, an expanded panel, or JSON. The view model is the
// one normalized surface every consumer reads: the Claude Code status line
// (which renders in the client's existing footer, below the prompt bar),
// the `fi` CLI, scripts, and any external integrator such as DevDesktop.
// There is no separate TUI — we compose into the status surface the user
// already has.
package render

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ColorMode controls whether ANSI color codes are emitted.
type ColorMode int

const (
	ColorAuto   ColorMode = iota // Detect from stdout (default)
	ColorAlways                  // Always emit colors
	ColorNever                   // Never emit colors
)

// Harvard color palette (ANSI 256-color approximations).
// Harvard Crimson: #A51C30 ≈ ANSI 124 (dark red) or 91 (bright red)
// Using 16-color codes for broad compatibility.
const (
	// Foreground colors
	ColorReset   = "\033[0m"
	ColorCrimson = "\033[38;5;124m" // Harvard Crimson #A51C30
	ColorRed     = "\033[91m"       // Bright red for warnings/critical
	ColorWhite   = "\033[97m"       // Bright white
	ColorBlack   = "\033[30m"       // Black
	ColorGray    = "\033[90m"       // Bright black (gray)
	ColorGreen   = "\033[32m"       // Green for healthy
	ColorYellow  = "\033[33m"       // Yellow for watch/degraded
	ColorCyan    = "\033[36m"       // Cyan for info

	// Background colors (rarely used)
	ColorBgBlack   = "\033[40m"
	ColorBgWhite   = "\033[47m"
	ColorBgCrimson = "\033[48;5;124m"
)

// ASCII fallback symbols for environments without Unicode support.
var asciiSymbols = symbols{
	HealthUnknown:      "[?]",
	HealthHealthy:      "[+]",
	HealthDegraded:     "[~]",
	HealthUnreachable:  "[x]",
	HealthUnconfirmed:  "[o]",
	PressureWatch:      "WATCH",
	PressureWarn:       "WARN",
	PressureCritical:   "CRIT",
	PressureRecovering: "RECV",
	PressureHealthy:    "OK",
	PressureUnknown:    "?",
	TurnActive:         "*",
	TurnInactive:       "-",
	TurnUnknown:        "?",
	Arrow:              "->",
	Separator:          "|",
	Bullet:             "*",
}

var unicodeSymbols = symbols{
	HealthUnknown:      "●",
	HealthHealthy:      "●",
	HealthDegraded:     "◐",
	HealthUnreachable:  "✗",
	HealthUnconfirmed:  "○",
	PressureWatch:      "WATCH",
	PressureWarn:       "WARN",
	PressureCritical:   "CRIT",
	PressureRecovering: "RECV",
	PressureHealthy:    "OK",
	PressureUnknown:    "?",
	TurnActive:         "●",
	TurnInactive:       "○",
	TurnUnknown:        "?",
	Arrow:              "→",
	Separator:          "|",
	Bullet:             "•",
}

type symbols struct {
	HealthUnknown      string
	HealthHealthy      string
	HealthDegraded     string
	HealthUnreachable  string
	HealthUnconfirmed  string
	PressureWatch      string
	PressureWarn       string
	PressureCritical   string
	PressureRecovering string
	PressureHealthy    string
	PressureUnknown    string
	TurnActive         string
	TurnInactive       string
	TurnUnknown        string
	Arrow              string
	Separator          string
	Bullet             string
}

// RenderConfig holds rendering options.
type RenderConfig struct {
	ColorMode ColorMode
	UseASCII  bool
	Compact   bool
}

// DefaultRenderConfig returns a config with auto-detection.
func DefaultRenderConfig() RenderConfig {
	return RenderConfig{
		ColorMode: ColorAuto,
		UseASCII:  false,
		Compact:   false,
	}
}

// DetectColorMode returns the appropriate color mode.
// Respects NO_COLOR and FORCE_COLOR environment variables.
func DetectColorMode() ColorMode {
	if os.Getenv("NO_COLOR") != "" {
		return ColorNever
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return ColorAlways
	}
	// Check if stdout is a terminal
	if isTerminal(os.Stdout) {
		return ColorAlways
	}
	return ColorNever
}

// isTerminal reports whether f is a terminal (TTY). Uses the platform-native
// ioctl via x/term so ANSI never leaks into pipes or files.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// colorize wraps text with ANSI color codes if colors are enabled.
func (c RenderConfig) colorize(text, colorCode string) string {
	if c.ColorMode == ColorNever {
		return text
	}
	if c.ColorMode == ColorAuto {
		// In auto mode, only colorize if we detect a terminal
		// The CLI will set ColorAlways/ColorNever explicitly
		return text
	}
	return colorCode + text + ColorReset
}

// syms returns the appropriate symbol set.
func (c RenderConfig) syms() symbols {
	if c.UseASCII {
		return asciiSymbols
	}
	return unicodeSymbols
}

// HealthSymbol returns a colored health status symbol.
func (c RenderConfig) HealthSymbol(confirmed bool, lastFailure, healthStatus string) string {
	s := c.syms()
	var sym, color string

	if !confirmed {
		sym = s.HealthUnconfirmed
		color = ColorGray
	} else if lastFailure != "" {
		sym = s.HealthUnreachable
		color = ColorRed
	} else {
		switch healthStatus {
		case "healthy", "":
			sym = s.HealthHealthy
			color = ColorGreen
		case "degraded":
			sym = s.HealthDegraded
			color = ColorYellow
		case "unreachable":
			sym = s.HealthUnreachable
			color = ColorRed
		default:
			sym = s.HealthUnknown
			color = ColorGray
		}
	}
	return c.colorize(sym, color)
}

// PressureSymbol returns a colored pressure state symbol.
func (c RenderConfig) PressureSymbol(state string, warningActive bool) string {
	s := c.syms()
	var sym, color string

	switch state {
	case "watch":
		sym = s.PressureWatch
		color = ColorYellow
	case "warn":
		sym = s.PressureWarn
		color = ColorRed
	case "critical":
		sym = s.PressureCritical
		color = ColorCrimson
	case "recovering":
		sym = s.PressureRecovering
		color = ColorYellow
	case "healthy":
		sym = s.PressureHealthy
		color = ColorGreen
	default:
		sym = s.PressureUnknown
		color = ColorGray
	}

	if warningActive && state != "warn" && state != "critical" {
		sym = s.PressureWarn
		color = ColorRed
	}

	return c.colorize(sym, color)
}

// FormatTokenCount renders a token count compactly (160K, 2.5M).
func FormatTokenCount(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.0fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatTokenPtr(p *int64) string {
	if p == nil {
		return "unknown"
	}
	return FormatTokenCount(*p)
}

func formatPct(p *float64) string {
	if p == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d%%", int(*p*100))
}

// TrendSymbol returns a colored trend indicator.
func (c RenderConfig) TrendSymbol(trend string) string {
	var sym, color string
	switch trend {
	case "rising":
		sym = "↑"
		color = ColorGreen
	case "declining":
		sym = "↓"
		color = ColorRed
	case "stable":
		sym = "→"
		color = ColorCyan
	default:
		sym = "?"
		color = ColorGray
	}
	if c.UseASCII {
		switch trend {
		case "rising":
			sym = "+"
		case "declining":
			sym = "-"
		case "stable":
			sym = "="
		default:
			sym = "?"
		}
	}
	return c.colorize(sym, color)
}

// ViewModel is the normalized, UI-agnostic view of a session plus global
// provider state. Pointer fields are null when data is unknown.
type ViewModel struct {
	Version       string `json:"version"`
	Client        string `json:"client,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	SessionStatus string `json:"session_status,omitempty"`
	ModelID       string `json:"model_id,omitempty"`

	ProviderName      string `json:"provider_name"`
	ProviderConfirmed bool   `json:"provider_confirmed"`

	HealthStatus  string `json:"health_status,omitempty"`
	HealthAgeSecs *int64 `json:"health_age_seconds,omitempty"`

	ContextUsedTokens *int64   `json:"context_used_tokens,omitempty"`
	ContextWindowSize *int64   `json:"context_window_size,omitempty"`
	ContextUsedPct    *float64 `json:"context_used_pct,omitempty"`

	FreshInputTokens    *int64 `json:"fresh_input_tokens,omitempty"`
	CacheReadTokens     *int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens *int64 `json:"cache_creation_tokens,omitempty"`
	OutputTokens        *int64 `json:"output_tokens,omitempty"`

	CacheReadShare *float64 `json:"cache_read_share,omitempty"`
	CacheTrend     string   `json:"cache_trend,omitempty"`

	PressureState string `json:"pressure_state"`

	TurnActive       *bool  `json:"turn_active,omitempty"`
	TurnDurationSecs *int64 `json:"turn_duration_seconds,omitempty"`

	LastFailureCategory string `json:"last_failure_category,omitempty"`

	WarningActive bool `json:"warning_active"`

	// Cache analysis fields
	CacheAnalysisRequestSamples int      `json:"cache_analysis_request_samples,omitempty"`
	CacheAnalysisReadShare      *float64 `json:"cache_analysis_read_share,omitempty"`
	CacheAnalysisCreationShare  *float64 `json:"cache_analysis_creation_share,omitempty"`
	CacheAnalysisFreshShare     *float64 `json:"cache_analysis_fresh_share,omitempty"`
	CacheAnalysisTrend          string   `json:"cache_analysis_trend,omitempty"`

	// Compaction fields
	CompactionLastResultAt           *time.Time `json:"compaction_last_result_at,omitempty"`
	CompactionLastResultTrigger      string     `json:"compaction_last_result_trigger,omitempty"`
	CompactionLastResultPreTokens    *int64     `json:"compaction_last_result_pre_tokens,omitempty"`
	CompactionLastResultPostTokens   *int64     `json:"compaction_last_result_post_tokens,omitempty"`
	CompactionLastResultReductionPct *float64   `json:"compaction_last_result_reduction_pct,omitempty"`

	// Circuit breaker fields
	CircuitBreakers []CircuitBreakerInfo `json:"circuit_breakers,omitempty"`
}

// CircuitBreakerInfo is a simplified circuit breaker for rendering.
type CircuitBreakerInfo struct {
	Endpoint      string     `json:"endpoint"`
	State         string     `json:"state"` // "closed", "open", "half-open"
	FailureCount  int        `json:"failure_count"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty"`
}

// BuildViewModel constructs the normalized view from a session snapshot and
// the global cache. Either may be nil (no data yet).
//
// All upstream-sourced string fields (model ID, provider name, client type,
// session status, failure category, trigger, etc.) are sanitized to strip
// terminal-control sequences and bound their length. The session ID is
// masked — the full ID is in the snapshot for callers that need to act on
// it, but the view model is the surface shown to humans and to downstream
// tools, so it shows the masked form only. Callers that need the raw ID
// should read snapshot.Session.ID directly rather than going through the VM.
func BuildViewModel(version string, snap *schema.Snapshot, gs *schema.GlobalState, now time.Time) *ViewModel {
	vm := &ViewModel{
		Version:       version,
		PressureState: schema.PressureUnknown,
	}
	if snap == nil {
		return vm
	}

	vm.Client = secure.SanitizeField(snap.Client.Type)
	vm.SessionID = secure.MaskSessionID(snap.Session.ID)
	vm.SessionStatus = secure.SanitizeField(snap.Session.Status)
	vm.ModelID = secure.SanitizeField(snap.Model.ID)
	vm.ProviderName = secure.SanitizeField(snap.Provider.Name)
	vm.ProviderConfirmed = snap.Provider.Confirmed && snap.Provider.Name == schema.ProviderFreeInference

	if snap.LiveContext != nil {
		lc := snap.LiveContext
		vm.ContextWindowSize = lc.ContextWindowSize
		vm.ContextUsedPct = lc.UsedPercentage
		if lc.TotalInputTokens != nil {
			used := *lc.TotalInputTokens
			if lc.TotalOutputTokens != nil {
				used += *lc.TotalOutputTokens
			}
			vm.ContextUsedTokens = &used
		}
		if lc.LatestRequest != nil {
			vm.FreshInputTokens = lc.LatestRequest.FreshInputTokens
			vm.CacheReadTokens = lc.LatestRequest.CacheReadInputTokens
			vm.CacheCreationTokens = lc.LatestRequest.CacheCreationInputTokens
			vm.OutputTokens = lc.LatestRequest.OutputTokens
		}
	}

	// Cache analysis: populate each VM field exactly once. The previous
	// implementation assigned these fields twice (once in a block near
	// LiveContext, once again further down). Aside from being dead work, the
	// duplication masked future edits that touched only one of the two
	// blocks.
	if snap.CacheAnalysis != nil {
		vm.CacheReadShare = snap.CacheAnalysis.CacheReadShare
		vm.CacheTrend = snap.CacheAnalysis.Trend
		vm.CacheAnalysisRequestSamples = snap.CacheAnalysis.RequestSamples
		vm.CacheAnalysisReadShare = snap.CacheAnalysis.CacheReadShare
		vm.CacheAnalysisCreationShare = snap.CacheAnalysis.CacheCreationShare
		vm.CacheAnalysisFreshShare = snap.CacheAnalysis.FreshInputShare
		vm.CacheAnalysisTrend = snap.CacheAnalysis.Trend
	}

	vm.PressureState = snap.Pressure.State
	vm.WarningActive = snap.Pressure.State == schema.PressureWarn ||
		snap.Pressure.State == schema.PressureCritical ||
		snap.Warnings.CacheLowActive

	vm.TurnActive = snap.Activity.TurnActive
	if snap.Activity.TurnActive != nil && *snap.Activity.TurnActive && snap.Activity.TurnStartedAt != nil {
		d := max(int64(0), int64(now.Sub(*snap.Activity.TurnStartedAt).Seconds()))
		vm.TurnDurationSecs = &d
	}

	if snap.LastFailure != nil {
		vm.LastFailureCategory = secure.SanitizeField(snap.LastFailure.Category)
	}

	// Health is only surfaced for confirmed FreeInference sessions — never
	// show a green FreeInference health symbol for an unknown provider.
	if vm.ProviderConfirmed && gs != nil && gs.Health != nil {
		vm.HealthStatus = secure.SanitizeField(gs.Health.Status)
		age := max(int64(0), int64(now.Sub(gs.Health.FetchedAt).Seconds()))
		vm.HealthAgeSecs = &age
		if vm.LastFailureCategory == "" && gs.Health.UnhealthyCount != nil && *gs.Health.UnhealthyCount > 0 {
			vm.HealthStatus = "degraded"
		}
	}

	if snap.Compaction.LastResult != nil {
		r := snap.Compaction.LastResult
		vm.CompactionLastResultAt = &r.At
		vm.CompactionLastResultTrigger = secure.SanitizeField(r.Trigger)
		vm.CompactionLastResultPreTokens = r.PreTokens
		vm.CompactionLastResultPostTokens = r.PostTokens
		vm.CompactionLastResultReductionPct = r.ReductionPct
	}

	// Populate circuit breaker info from global state. The endpoint/state
	// strings are internal (not upstream), but sanitize anyway for defense in
	// depth — the view model is consumed by external integrators too.
	if gs != nil && len(gs.CircuitBreakers) > 0 {
		vm.CircuitBreakers = make([]CircuitBreakerInfo, 0, len(gs.CircuitBreakers))
		for _, cb := range gs.CircuitBreakers {
			vm.CircuitBreakers = append(vm.CircuitBreakers, CircuitBreakerInfo{
				Endpoint:      secure.SanitizeField(cb.Endpoint),
				State:         secure.SanitizeField(cb.State),
				FailureCount:  cb.FailureCount,
				LastFailureAt: cb.LastFailureAt,
				NextRetryAt:   cb.NextRetryAt,
			})
		}
	}

	return vm
}

// Line renders the collapsed one-line form with colors.
// Example: FI glm-5.1 ● | ctx 80% | read 93% | WARN
func (vm *ViewModel) Line(config RenderConfig) string {
	model := vm.ModelID
	if model == "" {
		model = "?"
	}

	s := config.syms()
	sep := config.colorize(" "+s.Separator+" ", ColorGray)

	ctxStr := "ctx ?"
	if vm.ContextUsedPct != nil {
		pct := *vm.ContextUsedPct
		ctxStr = fmt.Sprintf("ctx %.0f%%", pct)
		// Color the percentage based on pressure
		switch {
		case pct >= 90:
			ctxStr = config.colorize(ctxStr, ColorCrimson)
		case pct >= 80:
			ctxStr = config.colorize(ctxStr, ColorRed)
		case pct >= 70:
			ctxStr = config.colorize(ctxStr, ColorYellow)
		default:
			ctxStr = config.colorize(ctxStr, ColorGreen)
		}
	}

	readStr := ""
	if vm.CacheReadShare != nil {
		pct := int(*vm.CacheReadShare * 100)
		readStr = fmt.Sprintf("read %d%%", pct)
		// Color cache read share: green=good, yellow=ok, red=bad
		switch {
		case pct >= 50:
			readStr = config.colorize(readStr, ColorGreen)
		case pct >= 20:
			readStr = config.colorize(readStr, ColorYellow)
		default:
			readStr = config.colorize(readStr, ColorRed)
		}
	}

	healthSym := config.HealthSymbol(vm.ProviderConfirmed, vm.LastFailureCategory, vm.HealthStatus)

	pressureSym := config.PressureSymbol(vm.PressureState, vm.WarningActive)

	modelColored := config.colorize("FI "+model, ColorWhite)

	var parts []string
	parts = append(parts, modelColored)
	parts = append(parts, healthSym)
	parts = append(parts, ctxStr)
	if readStr != "" {
		parts = append(parts, readStr)
	}
	parts = append(parts, pressureSym)

	return strings.Join(parts, sep)
}

// Expanded renders the multi-line panel form with colors.
func (vm *ViewModel) Expanded(config RenderConfig) string {
	var b strings.Builder
	s := config.syms()
	sep := config.colorize(" ", ColorGray)
	bullet := config.colorize(s.Bullet+" ", ColorGray)

	model := vm.ModelID
	if model == "" {
		model = "?"
	}
	title := config.colorize("FREEINFERENCE", ColorCrimson) + sep + config.colorize(model, ColorWhite)
	fmt.Fprintln(&b, title)

	provider := "unconfirmed"
	providerColor := ColorGray
	if vm.ProviderConfirmed {
		provider = "confirmed"
		providerColor = ColorGreen
	}
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Provider", ColorWhite), sep, config.colorize(provider, providerColor))

	health := "unknown"
	healthColor := ColorGray
	if vm.ProviderConfirmed {
		if vm.HealthStatus != "" {
			health = vm.HealthStatus
			if vm.HealthAgeSecs != nil {
				health = fmt.Sprintf("%s · %ds old", health, *vm.HealthAgeSecs)
			}
			switch vm.HealthStatus {
			case "healthy":
				healthColor = ColorGreen
			case "degraded":
				healthColor = ColorYellow
			case "unreachable":
				healthColor = ColorRed
			}
		}
	} else {
		health = "not applicable (provider unconfirmed)"
		healthColor = ColorGray
	}
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Health", ColorWhite), sep, config.colorize(health, healthColor))

	context := "unknown"
	if vm.ContextUsedTokens != nil && vm.ContextWindowSize != nil {
		context = fmt.Sprintf("%s / %s", FormatTokenCount(*vm.ContextUsedTokens), FormatTokenCount(*vm.ContextWindowSize))
		if vm.ContextUsedPct != nil {
			pct := *vm.ContextUsedPct
			context += fmt.Sprintf(" · %.0f%%", pct)
			// Color the percentage
			pctStr := fmt.Sprintf("%.0f%%", pct)
			switch {
			case pct >= 90:
				pctStr = config.colorize(pctStr, ColorCrimson)
			case pct >= 80:
				pctStr = config.colorize(pctStr, ColorRed)
			case pct >= 70:
				pctStr = config.colorize(pctStr, ColorYellow)
			default:
				pctStr = config.colorize(pctStr, ColorGreen)
			}
			context = strings.Replace(context, fmt.Sprintf("%.0f%%", pct), pctStr, 1)
		}
	} else if vm.ContextUsedPct != nil {
		context = fmt.Sprintf("%.0f%% used", *vm.ContextUsedPct)
	}
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Context", ColorWhite), sep, context)

	fresh := formatTokenPtr(vm.FreshInputTokens)
	cacheRead := formatTokenPtr(vm.CacheReadTokens)
	if vm.CacheReadShare != nil {
		cacheRead += fmt.Sprintf(" · %d%%", int(*vm.CacheReadShare*100))
	}
	cacheNew := formatTokenPtr(vm.CacheCreationTokens)
	output := formatTokenPtr(vm.OutputTokens)

	// Color cache read share
	if vm.CacheReadShare != nil {
		pct := int(*vm.CacheReadShare * 100)
		var shareColor string
		switch {
		case pct >= 50:
			shareColor = ColorGreen
		case pct >= 20:
			shareColor = ColorYellow
		default:
			shareColor = ColorRed
		}
		cacheRead = strings.Replace(cacheRead, fmt.Sprintf("%d%%", pct), config.colorize(fmt.Sprintf("%d%%", pct), shareColor), 1)
	}

	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Fresh input", ColorWhite), sep, config.colorize(fresh, ColorCyan))
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Cache read", ColorWhite), sep, cacheRead)
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Cache new", ColorWhite), sep, config.colorize(cacheNew, ColorCyan))
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Output", ColorWhite), sep, config.colorize(output, ColorCyan))

	pressureSym := config.PressureSymbol(vm.PressureState, vm.WarningActive)
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Pressure", ColorWhite), sep, pressureSym)

	turn := "unknown"
	if vm.TurnActive != nil {
		if *vm.TurnActive {
			turn = config.colorize(s.TurnActive+" active", ColorGreen)
			if vm.TurnDurationSecs != nil {
				turn = config.colorize(fmt.Sprintf("%s active · %ds", s.TurnActive, *vm.TurnDurationSecs), ColorGreen)
			}
		} else {
			turn = config.colorize(s.TurnInactive+" inactive", ColorGray)
		}
	} else {
		turn = config.colorize(s.TurnUnknown+" unknown", ColorGray)
	}
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Turn", ColorWhite), sep, turn)

	lastFailure := "none"
	if vm.LastFailureCategory != "" {
		lastFailure = config.colorize(vm.LastFailureCategory, ColorRed)
	}
	fmt.Fprintf(&b, "%s%s%s %s\n", bullet, config.colorize("Last failure", ColorWhite), sep, lastFailure)

	// Cache analysis
	if vm.CacheAnalysisRequestSamples > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "%sCache Analysis (%d unique samples):\n", bullet, vm.CacheAnalysisRequestSamples)
		readShare := formatPct(vm.CacheAnalysisReadShare)
		creationShare := formatPct(vm.CacheAnalysisCreationShare)
		freshShare := formatPct(vm.CacheAnalysisFreshShare)
		trend := config.TrendSymbol(vm.CacheAnalysisTrend)

		fmt.Fprintf(&b, "%s  Read share:%s  %s\n", bullet, sep, readShare)
		fmt.Fprintf(&b, "%s  New share:%s   %s\n", bullet, sep, creationShare)
		fmt.Fprintf(&b, "%s  Fresh share:%s %s\n", bullet, sep, freshShare)
		fmt.Fprintf(&b, "%s  Trend:%s       %s\n", bullet, sep, trend)
	}

	// Compaction
	if vm.CompactionLastResultAt != nil {
		fmt.Fprintln(&b)
		rAt := vm.CompactionLastResultAt
		fmt.Fprintf(&b, "%sLast Compaction (%s", bullet, rAt.Format("2006-01-02 15:04:05"))
		if vm.CompactionLastResultTrigger != "" {
			fmt.Fprintf(&b, ", %s", vm.CompactionLastResultTrigger)
		}
		fmt.Fprintln(&b, "):")
		fmt.Fprintf(&b, "%s  Before:%s    %s\n", bullet, sep, formatTokenPtr(vm.CompactionLastResultPreTokens))
		fmt.Fprintf(&b, "%s  After:%s     %s\n", bullet, sep, formatTokenPtr(vm.CompactionLastResultPostTokens))
		if vm.CompactionLastResultReductionPct != nil {
			redStr := fmt.Sprintf("%.1f%%", *vm.CompactionLastResultReductionPct)
			redColor := ColorGreen
			if *vm.CompactionLastResultReductionPct < 20 {
				redColor = ColorYellow
			}
			if *vm.CompactionLastResultReductionPct < 10 {
				redColor = ColorRed
			}
			fmt.Fprintf(&b, "%s  Reduction:%s %s\n", bullet, sep, config.colorize(redStr, redColor))
		} else {
			fmt.Fprintf(&b, "%s  Reduction:%s unknown\n", bullet, sep)
		}
	}

	// Circuit Breakers
	if len(vm.CircuitBreakers) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "%sCircuit Breakers:\n", bullet)
		for _, cb := range vm.CircuitBreakers {
			stateColor := CircuitBreakerStateColor(cb.State)
			stateSym := CircuitBreakerStateSymbol(cb.State)
			fmt.Fprintf(&b, "%s  %s:%s %s %s (failures: %d", bullet, sep, config.colorize(cb.Endpoint, ColorWhite), config.colorize(stateSym, stateColor), config.colorize(cb.State, stateColor), cb.FailureCount)
			if cb.LastFailureAt != nil {
				fmt.Fprintf(&b, ", last: %s", cb.LastFailureAt.Format("15:04:05"))
			}
			if cb.NextRetryAt != nil {
				fmt.Fprintf(&b, ", retry: %s", cb.NextRetryAt.Format("15:04:05"))
			}
			fmt.Fprintln(&b, ")")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// CircuitBreakerStateColor returns the color for a circuit breaker state.
func CircuitBreakerStateColor(state string) string {
	switch state {
	case "open":
		return ColorRed
	case "half-open":
		return ColorYellow
	case "closed":
		return ColorGreen
	default:
		return ColorGray
	}
}

// CircuitBreakerStateSymbol returns a symbol for a circuit breaker state.
func CircuitBreakerStateSymbol(state string) string {
	switch state {
	case "open":
		return "●"
	case "half-open":
		return "◐"
	case "closed":
		return "●"
	default:
		return "?"
	}
}

// JSON renders the view model as indented JSON.
func (vm *ViewModel) JSON() ([]byte, error) {
	return json.MarshalIndent(vm, "", "  ")
}
