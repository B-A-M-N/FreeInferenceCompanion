package engine

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ============================================================
// Pressure thresholds
// ============================================================

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

// Default thresholds (can be overridden via environment variables)
var (
	// Thresholds for entering states (higher bar)
	WatchEnterThreshold    = getEnvFloat("FI_WATCH_ENTER", 70.0)
	WarnEnterThreshold     = getEnvFloat("FI_WARN_ENTER", 80.0)
	CriticalEnterThreshold = getEnvFloat("FI_CRITICAL_ENTER", 90.0)

	// Thresholds for leaving states (lower bar = hysteresis)
	WatchLeaveThreshold    = getEnvFloat("FI_WATCH_LEAVE", 60.0)
	WarnLeaveThreshold     = getEnvFloat("FI_WARN_LEAVE", 65.0)
	CriticalLeaveThreshold = getEnvFloat("FI_CRITICAL_LEAVE", 75.0)

	// Default output reserve in tokens
	DefaultOutputReserve = getEnvInt("FI_OUTPUT_RESERVE", 16000)
)

// Minimum hysteresis gap (percentage points) required between enter and leave
// thresholds for each pressure level. Prevents flapping from floating-point
// noise when the gap is too small (e.g., enter=60, leave=59.99).
const MinHysteresisGap = 3.0

// ValidateThresholds checks that thresholds are finite, in [0,100], ordered,
// and have valid hysteresis with a minimum gap. Returns a diagnostic string if invalid.
func ValidateThresholds() error {
	cfg := ThresholdConfig{
		WatchEnter:    WatchEnterThreshold,
		WarnEnter:     WarnEnterThreshold,
		CriticalEnter: CriticalEnterThreshold,
		WatchLeave:    WatchLeaveThreshold,
		WarnLeave:     WarnLeaveThreshold,
		CriticalLeave: CriticalLeaveThreshold,
		OutputReserve: DefaultOutputReserve,
	}
	return cfg.Validate()
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

func getEnvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

// ============================================================
// Pressure state machine
// ============================================================

// ComputePressure determines the next pressure state based on used percentage and current state.
// Implements hysteresis: entering a state requires a higher threshold than leaving it.
func ComputePressure(usedPct float64, currentState string) (string, string) {
	switch currentState {
	case schema.PressureUnknown:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureUnknown
		}
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureUnknown
		}
		if usedPct >= WatchEnterThreshold {
			return schema.PressureWatch, schema.PressureUnknown
		}
		return schema.PressureHealthy, schema.PressureUnknown

	case schema.PressureHealthy:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureHealthy
		}
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureHealthy
		}
		if usedPct >= WatchEnterThreshold {
			return schema.PressureWatch, schema.PressureHealthy
		}
		return schema.PressureHealthy, schema.PressureHealthy

	case schema.PressureWatch:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureWatch
		}
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureWatch
		}
		if usedPct < WatchLeaveThreshold {
			return schema.PressureHealthy, schema.PressureWatch
		}
		return schema.PressureWatch, schema.PressureWatch

	case schema.PressureWarn:
		if usedPct >= CriticalEnterThreshold {
			return schema.PressureCritical, schema.PressureWarn
		}
		if usedPct < WarnLeaveThreshold {
			return schema.PressureRecovering, schema.PressureWarn
		}
		return schema.PressureWarn, schema.PressureWarn

	case schema.PressureCritical:
		if usedPct < CriticalLeaveThreshold {
			return schema.PressureRecovering, schema.PressureCritical
		}
		return schema.PressureCritical, schema.PressureCritical

	case schema.PressureRecovering:
		if usedPct >= WarnEnterThreshold {
			return schema.PressureWarn, schema.PressureRecovering
		}
		if usedPct < WarnLeaveThreshold {
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
