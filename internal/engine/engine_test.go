package engine

import (
	"sync"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// resetThresholds forces re-initialization of the lazy threshold singleton,
// allowing tests to run with a clean state.
func resetThresholds() {
	once = sync.Once{}
	thresholds = nil
}

func TestComputePressure_Healthy(t *testing.T) {
	// Force default thresholds via env vars to ensure deterministic behavior.
	t.Setenv("FI_WATCH_ENTER", "70")
	t.Setenv("FI_WARN_ENTER", "80")
	t.Setenv("FI_CRITICAL_ENTER", "90")
	t.Setenv("FI_WATCH_LEAVE", "60")
	t.Setenv("FI_WARN_LEAVE", "65")
	t.Setenv("FI_CRITICAL_LEAVE", "75")
	resetThresholds()

	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{50, schema.PressureHealthy, schema.PressureHealthy},
		{69, schema.PressureHealthy, schema.PressureHealthy},
		{70, schema.PressureHealthy, schema.PressureWatch},
		{80, schema.PressureHealthy, schema.PressureWarn},
		{90, schema.PressureHealthy, schema.PressureCritical},
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Hysteresis_Warn(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "70")
	t.Setenv("FI_WARN_ENTER", "80")
	t.Setenv("FI_CRITICAL_ENTER", "90")
	t.Setenv("FI_WATCH_LEAVE", "60")
	t.Setenv("FI_WARN_LEAVE", "65")
	t.Setenv("FI_CRITICAL_LEAVE", "75")
	resetThresholds()
	// Hysteresis: enter WARN at 80%, but don't leave until below 65%
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{80, schema.PressureHealthy, schema.PressureWarn},    // enter
		{75, schema.PressureWarn, schema.PressureWarn},       // stays (above leave threshold)
		{65, schema.PressureWarn, schema.PressureWarn},       // still at threshold
		{60, schema.PressureWarn, schema.PressureRecovering}, // below leave threshold
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Hysteresis_Critical(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "70")
	t.Setenv("FI_WARN_ENTER", "80")
	t.Setenv("FI_CRITICAL_ENTER", "90")
	t.Setenv("FI_WATCH_LEAVE", "60")
	t.Setenv("FI_WARN_LEAVE", "65")
	t.Setenv("FI_CRITICAL_LEAVE", "75")
	resetThresholds()
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{90, schema.PressureHealthy, schema.PressureCritical},    // enter
		{80, schema.PressureCritical, schema.PressureCritical},   // stays (above leave threshold)
		{75, schema.PressureCritical, schema.PressureCritical},   // at threshold, stays
		{70, schema.PressureCritical, schema.PressureRecovering}, // below
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Recovering(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "70")
	t.Setenv("FI_WARN_ENTER", "80")
	t.Setenv("FI_CRITICAL_ENTER", "90")
	t.Setenv("FI_WATCH_LEAVE", "60")
	t.Setenv("FI_WARN_LEAVE", "65")
	t.Setenv("FI_CRITICAL_LEAVE", "75")
	resetThresholds()
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{60, schema.PressureRecovering, schema.PressureHealthy},    // recovered
		{70, schema.PressureRecovering, schema.PressureRecovering}, // still recovering
		{80, schema.PressureRecovering, schema.PressureWarn},       // re-warned
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestComputePressure_Unknown(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "70")
	t.Setenv("FI_WARN_ENTER", "80")
	t.Setenv("FI_CRITICAL_ENTER", "90")
	t.Setenv("FI_WATCH_LEAVE", "60")
	t.Setenv("FI_WARN_LEAVE", "65")
	t.Setenv("FI_CRITICAL_LEAVE", "75")
	resetThresholds()
	tests := []struct {
		pct     float64
		current string
		want    string
	}{
		{50, schema.PressureUnknown, schema.PressureHealthy},
		{75, schema.PressureUnknown, schema.PressureWatch},
		{85, schema.PressureUnknown, schema.PressureWarn},
		{95, schema.PressureUnknown, schema.PressureCritical},
	}

	for _, tt := range tests {
		got, _ := ComputePressure(tt.pct, tt.current)
		if got != tt.want {
			t.Errorf("ComputePressure(%.0f, %s) = %s, want %s", tt.pct, tt.current, got, tt.want)
		}
	}
}

func TestClassifyPressure(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "70")
	t.Setenv("FI_WARN_ENTER", "80")
	t.Setenv("FI_CRITICAL_ENTER", "90")
	t.Setenv("FI_WATCH_LEAVE", "60")
	t.Setenv("FI_WARN_LEAVE", "65")
	t.Setenv("FI_CRITICAL_LEAVE", "75")
	resetThresholds()
	state, reason := ClassifyPressure(85.0, schema.PressureHealthy)
	if state != schema.PressureWarn {
		t.Errorf("expected warn, got %s", state)
	}
	if reason == nil {
		t.Error("expected non-nil reason for warn state")
	}

	state, reason = ClassifyPressure(50.0, schema.PressureWarn)
	if state != schema.PressureRecovering {
		t.Errorf("expected recovering, got %s", state)
	}

	// No reason for healthy state
	state, reason = ClassifyPressure(30.0, schema.PressureHealthy)
	if state != schema.PressureHealthy {
		t.Errorf("expected healthy, got %s", state)
	}
	if reason != nil {
		t.Error("expected nil reason for healthy state")
	}
}

func TestValidateThresholds_ValidDefaults(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "70")
	t.Setenv("FI_WARN_ENTER", "80")
	t.Setenv("FI_CRITICAL_ENTER", "90")
	t.Setenv("FI_WATCH_LEAVE", "60")
	t.Setenv("FI_WARN_LEAVE", "65")
	t.Setenv("FI_CRITICAL_LEAVE", "75")
	resetThresholds()
	// Default thresholds should be valid
	if err := ValidateThresholds(); err != nil {
		t.Errorf("default thresholds should be valid: %v", err)
	}
}

func TestValidateThresholds_InvalidWatchGapTooSmall(t *testing.T) {
	// Watch leave must be < watch enter with at least some gap
	// This tests the hysteresis gap enforcement
	cfg := ThresholdConfig{
		WatchEnter:    60.0,
		WarnEnter:     70.0,
		CriticalEnter: 80.0,
		WatchLeave:    59.9, // Only 0.1% gap - should fail
		WarnLeave:     65.0,
		CriticalLeave: 75.0,
		OutputReserve: 16000,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("watch leave 59.9 with enter 60.0 (0.1% gap) should fail validation")
	}
}

func TestValidateThresholds_InvalidWarnGapTooSmall(t *testing.T) {
	cfg := ThresholdConfig{
		WatchEnter:    60.0,
		WarnEnter:     70.0,
		CriticalEnter: 80.0,
		WatchLeave:    55.0,
		WarnLeave:     69.9, // Only 0.1% gap - should fail
		CriticalLeave: 75.0,
		OutputReserve: 16000,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("warn leave 69.9 with enter 70.0 (0.1% gap) should fail validation")
	}
}

func TestValidateThresholds_InvalidCriticalGapTooSmall(t *testing.T) {
	cfg := ThresholdConfig{
		WatchEnter:    60.0,
		WarnEnter:     70.0,
		CriticalEnter: 80.0,
		WatchLeave:    55.0,
		WarnLeave:     65.0,
		CriticalLeave: 79.9, // Only 0.1% gap - should fail
		OutputReserve: 16000,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("critical leave 79.9 with enter 80.0 (0.1% gap) should fail validation")
	}
}

func TestValidateThresholds_ValidReasonableGap(t *testing.T) {
	cfg := ThresholdConfig{
		WatchEnter:    70.0,
		WarnEnter:     80.0,
		CriticalEnter: 90.0,
		WatchLeave:    60.0, // 10% gap - OK
		WarnLeave:     70.0, // 10% gap - OK
		CriticalLeave: 80.0, // 10% gap - OK
		OutputReserve: 16000,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("reasonable gaps should be valid: %v", err)
	}
}

func TestValidateThresholds_WatchLeaveEqualsWatchEnter_Fails(t *testing.T) {
	cfg := ThresholdConfig{
		WatchEnter:    70.0,
		WarnEnter:     80.0,
		CriticalEnter: 90.0,
		WatchLeave:    70.0, // Equal - should fail (leave < enter required)
		WarnLeave:     70.0,
		CriticalLeave: 80.0,
		OutputReserve: 16000,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("watch leave == watch enter should fail")
	}
}

func TestValidateThresholds_OrderedEnterThresholds(t *testing.T) {
	cfg := ThresholdConfig{
		WatchEnter:    80.0, // Wrong order: watch > warn
		WarnEnter:     70.0,
		CriticalEnter: 90.0,
		WatchLeave:    60.0,
		WarnLeave:     65.0,
		CriticalLeave: 75.0,
		OutputReserve: 16000,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("enter thresholds must be ordered watch < warn < critical")
	}
}

func TestValidateThresholds_OrderedLeaveThresholds(t *testing.T) {
	cfg := ThresholdConfig{
		WatchEnter:    60.0,
		WarnEnter:     70.0,
		CriticalEnter: 80.0,
		WatchLeave:    70.0, // Wrong order: watch leave > warn leave
		WarnLeave:     60.0,
		CriticalLeave: 75.0,
		OutputReserve: 16000,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("leave thresholds must be ordered watch < warn < critical")
	}
}
