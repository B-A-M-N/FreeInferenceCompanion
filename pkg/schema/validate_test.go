package schema

import (
	"math"
	"strings"
	"testing"
)

func TestValidateSnapshotValid(t *testing.T) {
	s := &Snapshot{
		SchemaVersion: StateVersion,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1", Status: SessionActive},
		Pressure:      PressureState{State: PressureHealthy},
	}
	if err := ValidateSnapshot(s); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
}

func TestValidateSnapshotRejectsNil(t *testing.T) {
	if err := ValidateSnapshot(nil); err == nil {
		t.Fatal("nil snapshot must be rejected")
	}
}

func TestValidateSnapshotRejectsFutureVersion(t *testing.T) {
	s := &Snapshot{
		SchemaVersion: CurrentSchemaVersion + 1,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
	}
	err := ValidateSnapshot(s)
	if err == nil || !strings.Contains(err.Error(), "unsupported schema version") {
		t.Fatalf("future version must be rejected, got %v", err)
	}
}

func TestValidateSnapshotRejectsAncientVersion(t *testing.T) {
	s := &Snapshot{
		SchemaVersion: MinSupportedSchemaVersion - 1,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
	}
	if err := ValidateSnapshot(s); err == nil {
		t.Fatal("ancient version must be rejected")
	}
}

func TestValidateSnapshotRejectsUnknownClient(t *testing.T) {
	s := &Snapshot{
		SchemaVersion: StateVersion,
		Client:        ClientInfo{Type: "gemini"},
		Session:       SessionInfo{ID: "s1"},
	}
	if err := ValidateSnapshot(s); err == nil {
		t.Fatal("unknown client must be rejected")
	}
}

func TestValidateSnapshotRejectsBadPressure(t *testing.T) {
	s := &Snapshot{
		SchemaVersion: StateVersion,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
		Pressure:      PressureState{State: "exploded"},
	}
	if err := ValidateSnapshot(s); err == nil {
		t.Fatal("bad pressure state must be rejected")
	}
}

func TestValidateSnapshotRejectsOutOfRangePercentage(t *testing.T) {
	bad := 150.0
	s := &Snapshot{
		SchemaVersion: StateVersion,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
		LiveContext:   &LiveContext{UsedPercentage: &bad},
	}
	if err := ValidateSnapshot(s); err == nil {
		t.Fatal("percentage over 100 must be rejected")
	}
}

func TestMigrateSnapshotFromV1(t *testing.T) {
	s := &Snapshot{
		SchemaVersion: 1,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
	}
	if err := MigrateSnapshot(s); err != nil {
		t.Fatalf("migrate v1: %v", err)
	}
	if s.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("post-migrate version = %d, want %d", s.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestMigrateSnapshotRejectsFutureVersion(t *testing.T) {
	s := &Snapshot{SchemaVersion: CurrentSchemaVersion + 1}
	if err := MigrateSnapshot(s); err == nil {
		t.Fatal("future version must not migrate")
	}
}

func TestQuarantineReasonSanitized(t *testing.T) {
	for _, in := range []struct {
		name string
		in   string
	}{
		{"slashes", "corrupt at /etc/passwd and /tmp"},
		{"spaces", "checksum mismatch for foo"},
		{"empty", ""},
	} {
		t.Run(in.name, func(t *testing.T) {
			err := errWithString(in.in)
			got := QuarantineReason(err)
			if strings.Contains(got, "/") || strings.Contains(got, "\\") {
				t.Errorf("reason contains a path separator: %q", got)
			}
			if strings.Contains(got, " ") {
				t.Errorf("reason contains a space: %q", got)
			}
			if len(got) > 60 {
				t.Errorf("reason too long: %d", len(got))
			}
		})
	}
}

func TestValidateSnapshotRejectsNaNTokens(t *testing.T) {
	nanVal := math.NaN()
	s := &Snapshot{
		SchemaVersion: StateVersion,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
		LiveContext:   &LiveContext{UsedPercentage: &nanVal},
	}
	if err := ValidateSnapshot(s); err == nil {
		t.Fatal("NaN used_percentage must be rejected")
	}
}

func TestValidateSnapshotRejectsNegativeTokens(t *testing.T) {
	neg := int64(-100)
	s := &Snapshot{
		SchemaVersion: StateVersion,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
		LiveContext:   &LiveContext{TotalInputTokens: &neg},
	}
	if err := ValidateSnapshot(s); err == nil {
		t.Fatal("negative total_input_tokens must be rejected")
	}
}

func TestValidateSnapshotRejectsBadCacheShares(t *testing.T) {
	badShare := 1.5
	s := &Snapshot{
		SchemaVersion: StateVersion,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
		CacheAnalysis: &CacheAnalysis{CacheReadShare: &badShare},
	}
	if err := ValidateSnapshot(s); err == nil {
		t.Fatal("cache_read_share > 1 must be rejected")
	}
}

func TestMigrateV1ClearsLiveContext(t *testing.T) {
	// A v1 snapshot cannot be cleanly decomposed into the v2 LiveContext
	// split. The migration must leave LiveContext null (not fabricate data).
	lc := &LiveContext{}
	s := &Snapshot{
		SchemaVersion: 1,
		Client:        ClientInfo{Type: ClientClaudeCode},
		Session:       SessionInfo{ID: "s1"},
		LiveContext:   lc,
	}
	if err := MigrateSnapshot(s); err != nil {
		t.Fatalf("migrate v1: %v", err)
	}
	if s.LiveContext != nil {
		t.Error("v1→v2 migration must clear LiveContext (cannot fabricate split)")
	}
}

type stringErr string

func (s stringErr) Error() string { return string(s) }

func errWithString(s string) error { return stringErr(s) }
