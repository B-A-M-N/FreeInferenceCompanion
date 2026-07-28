package engine

import (
	"testing"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func TestComputePressure_Healthy(t *testing.T) {
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
