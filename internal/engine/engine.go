package engine

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ============================================================
// Pressure thresholds — lazy config-backed accessors
// ============================================================

// thresholdConfig holds the resolved threshold values for the current
// configuration. It is filled on first access via lazyResolve.
type thresholdConfig struct {
	WatchEnter    float64
	WarnEnter     float64
	CriticalEnter float64
	WatchLeave    float64
	WarnLeave     float64
	CriticalLeave float64
	reserve       int
}

// lazyThresholds returns the resolved threshold config, initializing once
// from the config manager (env → file → default precedence).
func lazyThresholds() *thresholdConfig {
	once.Do(initThresholds)
	return thresholds
}

var (
	once       sync.Once
	thresholds *thresholdConfig
)

func initThresholds() {
	mgr, err := config.NewManager()
	if err != nil {
		// If we can't create a manager, fall through with defaults.
		return
	}
	eff, err := mgr.Resolve()
	if err != nil {
		return
	}

	t := &thresholdConfig{}

	// Context thresholds — env vars are respected via config resolution.
	t.WatchEnter = eff.Context.WatchEnter.Value
	t.WarnEnter = eff.Context.WarnEnter.Value
	t.CriticalEnter = eff.Context.CriticalEnter.Value
	t.WatchLeave = eff.Context.WatchLeave.Value
	t.WarnLeave = eff.Context.WarnLeave.Value
	t.CriticalLeave = eff.Context.CriticalLeave.Value

	t.reserve = eff.Context.OutputReserve.Value
	if err := (ThresholdConfig{
		WatchEnter: t.WatchEnter, WarnEnter: t.WarnEnter, CriticalEnter: t.CriticalEnter,
		WatchLeave: t.WatchLeave, WarnLeave: t.WarnLeave, CriticalLeave: t.CriticalLeave,
		OutputReserve: t.reserve,
	}).Validate(); err != nil {
		thresholds = defaultThresholds()
		return
	}
	thresholds = t
}

// Read a single threshold value from the lazy-resolved config.
// This is the canonical accessor used by all pressure-state functions.
func (t *thresholdConfig) WatchEnterThreshold() float64    { return t.WatchEnter }
func (t *thresholdConfig) WarnEnterThreshold() float64     { return t.WarnEnter }
func (t *thresholdConfig) CriticalEnterThreshold() float64 { return t.CriticalEnter }
func (t *thresholdConfig) WatchLeaveThreshold() float64    { return t.WatchLeave }
func (t *thresholdConfig) WarnLeaveThreshold() float64     { return t.WarnLeave }
func (t *thresholdConfig) CriticalLeaveThreshold() float64 { return t.CriticalLeave }
func (t *thresholdConfig) OutputReserve() int              { return t.reserve }

// defaultThresholds returns the hard-coded defaults.
func defaultThresholds() *thresholdConfig {
	return &thresholdConfig{
		WatchEnter:    70.0,
		WarnEnter:     80.0,
		CriticalEnter: 90.0,
		WatchLeave:    60.0,
		WarnLeave:     65.0,
		CriticalLeave: 75.0,
		reserve:       16000,
	}
}

// Minimum hysteresis gap (percentage points) required between enter and leave
// thresholds for each pressure level. Prevents flapping from floating-point
// noise when the gap is too small (e.g., enter=60, leave=59.99).
const MinHysteresisGap = 3.0

// ValidateThresholds checks that thresholds are finite, in [0,100], ordered,
// and have valid hysteresis with a minimum gap. Returns a diagnostic string if invalid.
func ValidateThresholds() error {
	t := lazyThresholds()
	cfg := ThresholdConfig{
		WatchEnter:    t.WatchEnterThreshold(),
		WarnEnter:     t.WarnEnterThreshold(),
		CriticalEnter: t.CriticalEnterThreshold(),
		WatchLeave:    t.WatchLeaveThreshold(),
		WarnLeave:     t.WarnLeaveThreshold(),
		CriticalLeave: t.CriticalLeaveThreshold(),
		OutputReserve: t.OutputReserve(),
	}
	return cfg.Validate()
}

// ThresholdConfig holds validated runtime configuration.
type ThresholdConfig struct {
	WatchEnter    float64
	WarnEnter     float64
	CriticalEnter float64
	WatchLeave    float64
	WarnLeave     float64
	CriticalLeave float64
	OutputReserve int
}

// Validate checks the threshold configuration for correctness.
func (c ThresholdConfig) Validate() error {
	// All percentages must be finite and in [0,100].
	fields := map[string]float64{
		"watch_enter":    c.WatchEnter,
		"warn_enter":     c.WarnEnter,
		"critical_enter": c.CriticalEnter,
		"watch_leave":    c.WatchLeave,
		"warn_leave":     c.WarnLeave,
		"critical_leave": c.CriticalLeave,
	}
	for name, val := range fields {
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return fmt.Errorf("threshold %s is not finite: %v", name, val)
		}
		if val < 0 || val > 100 {
			return fmt.Errorf("threshold %s is out of range [0,100]: %v", name, val)
		}
	}
	// Enter thresholds must be strictly ordered.
	if !(c.WatchEnter < c.WarnEnter && c.WarnEnter < c.CriticalEnter) {
		return fmt.Errorf("enter thresholds must be ordered: watch < warn < critical (got %.1f < %.1f < %.1f)",
			c.WatchEnter, c.WarnEnter, c.CriticalEnter)
	}
	// Leave thresholds must be strictly ordered.
	if !(c.WatchLeave < c.WarnLeave && c.WarnLeave < c.CriticalLeave) {
		return fmt.Errorf("leave thresholds must be ordered: watch < warn < critical (got %.1f < %.1f < %.1f)",
			c.WatchLeave, c.WarnLeave, c.CriticalLeave)
	}
	// Hysteresis: leave < enter for each level, with a minimum gap to prevent flapping.
	if c.WatchLeave >= c.WatchEnter {
		return fmt.Errorf("watch leave (%.1f) must be < watch enter (%.1f)", c.WatchLeave, c.WatchEnter)
	}
	if c.WarnLeave >= c.WarnEnter {
		return fmt.Errorf("warn leave (%.1f) must be < warn enter (%.1f)", c.WarnLeave, c.WarnEnter)
	}
	if c.CriticalLeave >= c.CriticalEnter {
		return fmt.Errorf("critical leave (%.1f) must be < critical enter (%.1f)", c.CriticalLeave, c.CriticalEnter)
	}
	// Enforce minimum hysteresis gap (MinHysteresisGap percentage points).
	if c.WatchEnter-c.WatchLeave < MinHysteresisGap {
		return fmt.Errorf("watch hysteresis gap (%.1f) is below minimum required gap of %.1f (enter=%.1f, leave=%.1f)",
			c.WatchEnter-c.WatchLeave, MinHysteresisGap, c.WatchEnter, c.WatchLeave)
	}
	if c.WarnEnter-c.WarnLeave < MinHysteresisGap {
		return fmt.Errorf("warn hysteresis gap (%.1f) is below minimum required gap of %.1f (enter=%.1f, leave=%.1f)",
			c.WarnEnter-c.WarnLeave, MinHysteresisGap, c.WarnEnter, c.WarnLeave)
	}
	if c.CriticalEnter-c.CriticalLeave < MinHysteresisGap {
		return fmt.Errorf("critical hysteresis gap (%.1f) is below minimum required gap of %.1f (enter=%.1f, leave=%.1f)",
			c.CriticalEnter-c.CriticalLeave, MinHysteresisGap, c.CriticalEnter, c.CriticalLeave)
	}
	// Output reserve must be positive and bounded.
	if c.OutputReserve <= 0 {
		return fmt.Errorf("output reserve must be positive: %d", c.OutputReserve)
	}
	if c.OutputReserve > 1_000_000 {
		return fmt.Errorf("output reserve is unreasonable: %d (max 1000000)", c.OutputReserve)
	}
	return nil
}

// ContextCriticalEnterThreshold returns the configured critical enter threshold.
func ContextCriticalEnterThreshold() float64 { return lazyThresholds().CriticalEnterThreshold() }

// ContextWarnEnterThreshold returns the configured warn enter threshold.
func ContextWarnEnterThreshold() float64 { return lazyThresholds().WarnEnterThreshold() }

// ContextWatchEnterThreshold returns the configured watch enter threshold.
func ContextWatchEnterThreshold() float64 { return lazyThresholds().WatchEnterThreshold() }

// ============================================================
// Pressure state machine
// ============================================================

// ComputePressure determines the next pressure state based on used percentage and current state.
// Implements hysteresis: entering a state requires a higher threshold than leaving it.
func ComputePressure(usedPct float64, currentState string) (string, string) {
	t := lazyThresholds()
	switch currentState {
	case schema.PressureUnknown:
		if usedPct >= t.CriticalEnterThreshold() {
			return schema.PressureCritical, schema.PressureUnknown
		}
		if usedPct >= t.WarnEnterThreshold() {
			return schema.PressureWarn, schema.PressureUnknown
		}
		if usedPct >= t.WatchEnterThreshold() {
			return schema.PressureWatch, schema.PressureUnknown
		}
		return schema.PressureHealthy, schema.PressureUnknown

	case schema.PressureHealthy:
		if usedPct >= t.CriticalEnterThreshold() {
			return schema.PressureCritical, schema.PressureHealthy
		}
		if usedPct >= t.WarnEnterThreshold() {
			return schema.PressureWarn, schema.PressureHealthy
		}
		if usedPct >= t.WatchEnterThreshold() {
			return schema.PressureWatch, schema.PressureHealthy
		}
		return schema.PressureHealthy, schema.PressureHealthy

	case schema.PressureWatch:
		if usedPct >= t.CriticalEnterThreshold() {
			return schema.PressureCritical, schema.PressureWatch
		}
		if usedPct >= t.WarnEnterThreshold() {
			return schema.PressureWarn, schema.PressureWatch
		}
		if usedPct < t.WatchLeaveThreshold() {
			return schema.PressureHealthy, schema.PressureWatch
		}
		return schema.PressureWatch, schema.PressureWatch

	case schema.PressureWarn:
		if usedPct >= t.CriticalEnterThreshold() {
			return schema.PressureCritical, schema.PressureWarn
		}
		if usedPct < t.WarnLeaveThreshold() {
			return schema.PressureRecovering, schema.PressureWarn
		}
		return schema.PressureWarn, schema.PressureWarn

	case schema.PressureCritical:
		if usedPct < t.CriticalLeaveThreshold() {
			return schema.PressureRecovering, schema.PressureCritical
		}
		return schema.PressureCritical, schema.PressureCritical

	case schema.PressureRecovering:
		if usedPct >= t.WarnEnterThreshold() {
			return schema.PressureWarn, schema.PressureRecovering
		}
		if usedPct < t.WarnLeaveThreshold() {
			return schema.PressureHealthy, schema.PressureRecovering
		}
		return schema.PressureRecovering, schema.PressureRecovering

	default:
		return schema.PressureUnknown, schema.PressureUnknown
	}
}

// ClassifyPressure determines the state and reason for a given context percentage.
func ClassifyPressure(usedPct float64, prevState string) (newState string, reason *string) {
	newState, _ = ComputePressure(usedPct, prevState)

	var r *string
	switch newState {
	case schema.PressureWatch:
		s := "context approaching watch threshold"
		r = &s
	case schema.PressureWarn:
		s := "context above warn threshold"
		r = &s
	case schema.PressureCritical:
		s := "context at critical level"
		r = &s
	case schema.PressureRecovering:
		s := "context recovering after pressure reduction"
		r = &s
	}
	return newState, r
}

// ============================================================
// Warning deduplication
// ============================================================

// ContextWarningCooldown is the minimum interval between same-severity
// context-family warnings. The Claude adapter persists last-shown timestamps
// in the snapshot so the cooldown survives across hook invocations.
const ContextWarningCooldown = 15 * time.Minute
