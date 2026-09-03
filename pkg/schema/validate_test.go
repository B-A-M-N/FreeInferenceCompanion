package schema

import (
	"math"
	"strings"
	"testing"
	"time"
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

func TestValidatePublicStatusCacheBoundsAndFiniteMetrics(t *testing.T) {
	ok := true
	badUptime := math.NaN()
	cache := &PublicStatusCache{
		Source: "https://status.freeinference.org",
		Models: []PublicStatusModelCache{{
			ModelID:     "glm-5.2",
			UptimeRatio: &badUptime,
			Latest:      &PublicStatusSampleCache{OK: &ok, CheckedAt: nowForSchemaTest()},
		}},
	}
	if err := ValidatePublicStatusCache(cache); err == nil {
		t.Fatal("NaN public monitor uptime must be rejected")
	}

	cache.Models[0].UptimeRatio = nil
	cache.Models[0].History = make([]PublicStatusSampleCache, MaxPublicStatusSamplesPerModel+1)
	if err := ValidatePublicStatusCache(cache); err == nil {
		t.Fatal("oversized public monitor history must be rejected")
	}
}

func TestValidatePublicStatusCacheRejectsDuplicateModels(t *testing.T) {
	ok := true
	now := nowForSchemaTest()
	cache := &PublicStatusCache{
		Source: "https://status.freeinference.org",
		Models: []PublicStatusModelCache{
			{ModelID: "same", Latest: &PublicStatusSampleCache{OK: &ok, CheckedAt: now}},
			{ModelID: "same", Latest: &PublicStatusSampleCache{OK: &ok, CheckedAt: now}},
		},
	}
	if err := ValidatePublicStatusCache(cache); err == nil {
		t.Fatal("duplicate public monitor models must be rejected")
	}
}

func TestValidateAccountUsageRejectsNonsensicalQuota(t *testing.T) {
	used, limit := int64(11), int64(10)
	usage := &AccountUsage{
		Authoritative: true,
		FetchedAt:     nowForSchemaTest(),
		RequestsUsed:  &used,
		RequestsLimit: &limit,
	}
	if err := ValidateAccountUsage(usage); err == nil {
		t.Fatal("usage above limit must be rejected")
	}
}

func TestHasUsableAccountUsageRequiresFreshSupportedData(t *testing.T) {
	used, limit := int64(1), int64(10)
	now := nowForSchemaTest()
	gs := &GlobalState{
		AccountUsage:           &AccountUsage{Authoritative: true, FetchedAt: now, RequestsUsed: &used, RequestsLimit: &limit},
		AccountUsageCapability: &AccountUsageCapability{State: CapabilitySupported, CheckedAt: now},
	}
	if !gs.HasUsableAccountUsage(now, DefaultAccountUsageMaxAge) {
		t.Fatal("fresh supported usage should be usable")
	}
	if gs.HasUsableAccountUsage(now.Add(DefaultAccountUsageMaxAge+time.Second), DefaultAccountUsageMaxAge) {
		t.Fatal("stale usage must not be usable")
	}
}

func TestValidateModelsCacheRejectsUnsafeDuplicateCatalog(t *testing.T) {
	cache := &ModelsCache{
		FetchedAt: nowForSchemaTest(),
		Models: []CatalogModel{
			{ID: "same", AccessState: AccessUnknown},
			{ID: "same", AccessState: AccessUnknown},
		},
	}
	if err := ValidateModelsCache(cache); err == nil {
		t.Fatal("duplicate model ids must be rejected")
	}
}

func nowForSchemaTest() time.Time {
	return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
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
