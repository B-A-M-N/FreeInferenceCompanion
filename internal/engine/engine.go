package engine

import (
	"os"
	"strconv"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ============================================================
// Pressure thresholds
// ============================================================

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
