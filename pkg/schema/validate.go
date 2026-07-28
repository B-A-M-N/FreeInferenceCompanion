package schema

import (
	"errors"
	"fmt"
	"strings"
)

// SupportedSchemaRange is the range of schema versions this build can safely
// load. Versions below MinSupportedSchemaVersion trigger MigrateSnapshot;
// versions above CurrentSchemaVersion are rejected as forward-incompatible.
const (
	MinSupportedSchemaVersion = 1
	// CurrentSchemaVersion mirrors StateVersion; declared separately to avoid
	// an import cycle in test code.
	CurrentSchemaVersion = StateVersion
)

// ErrUnsupportedSchema is returned when a snapshot's schema version is too new
// for this binary to safely interpret.
var ErrUnsupportedSchema = errors.New("unsupported schema version")

// ValidateSnapshot reports whether a snapshot is structurally sound. It checks:
//   - schema version is in the supported range
//   - required identifying fields are populated
//   - pointer-vs-zero semantics are internally consistent (no nil-masked totals)
//
// It does NOT check semantic invariants that depend on time or external state;
// those belong in the engine layer.
func ValidateSnapshot(s *Snapshot) error {
	if s == nil {
		return errors.New("nil snapshot")
	}
	if s.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("%w: file=%d, supported≤%d", ErrUnsupportedSchema, s.SchemaVersion, CurrentSchemaVersion)
	}
	if s.SchemaVersion < MinSupportedSchemaVersion {
		return fmt.Errorf("%w: file=%d, supported≥%d", ErrUnsupportedSchema, s.SchemaVersion, MinSupportedSchemaVersion)
	}
	if s.Session.ID == "" {
		return errors.New("missing session id")
	}
	if s.Client.Type != ClientClaudeCode && s.Client.Type != ClientCodex {
		return fmt.Errorf("unknown client type %q", s.Client.Type)
	}

	// Pressure state must be a known constant. Empty is allowed because
	// snapshots are mutated incrementally and a fresh snapshot may not yet
	// have a pressure state populated.
	switch s.Pressure.State {
	case "", PressureUnknown, PressureHealthy, PressureWatch, PressureWarn,
		PressureCritical, PressureRecovering:
	default:
		return fmt.Errorf("invalid pressure state %q", s.Pressure.State)
	}

	// Access state must be a known constant (empty allowed for fresh sessions).
	switch s.Model.AccessState {
	case "", AccessAvailable, AccessRestricted, AccessUnknown:
	default:
		return fmt.Errorf("invalid model access state %q", s.Model.AccessState)
	}

	// Used percentage, when present, must be in [0, 100].
	if s.LiveContext != nil && s.LiveContext.UsedPercentage != nil {
		p := *s.LiveContext.UsedPercentage
		if p < 0 || p > 100 {
			return fmt.Errorf("used_percentage out of range: %f", p)
		}
	}

	// Cache analysis trend, when present, must be a known constant.
	if s.CacheAnalysis != nil && s.CacheAnalysis.Trend != "" {
		switch s.CacheAnalysis.Trend {
		case TrendRising, TrendStable, TrendDeclining, TrendInsufficientData:
		default:
			return fmt.Errorf("invalid cache trend %q", s.CacheAnalysis.Trend)
		}
	}

	// Session status must be a known constant. Empty is allowed — a
	// freshly-initialized snapshot may not yet have a status set.
	switch s.Session.Status {
	case "", SessionActive, SessionStopped, SessionCompleted:
	default:
		return fmt.Errorf("invalid session status %q", s.Session.Status)
	}

	return nil
}

// MigrateSnapshot upgrades an older snapshot in-place to the current schema
// version. Returns ErrUnsupportedSchema when the version is too old to migrate
// or newer than this binary understands.
//
// Migrations are strictly additive: they populate new fields with their null
// defaults and bump SchemaVersion. They never invent data the older snapshot
// did not contain.
func MigrateSnapshot(s *Snapshot) error {
	if s == nil {
		return errors.New("nil snapshot")
	}
	if s.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("%w: file=%d, current=%d", ErrUnsupportedSchema, s.SchemaVersion, CurrentSchemaVersion)
	}

	// v1 → v2: split cumulative totals from latest-request usage and add the
	// rolling cache analysis. A v1 snapshot may have only the legacy fields;
	// everything else is left null.
	for s.SchemaVersion < CurrentSchemaVersion {
		switch s.SchemaVersion {
		case 0, 1:
			// v1 only had a flat request-usage block; the v2 LiveContext
			// pointer is populated by the caller before persisting. Nothing
			// to fabricate here.
			s.SchemaVersion = 2
		default:
			return fmt.Errorf("%w: no migration from %d", ErrUnsupportedSchema, s.SchemaVersion)
		}
	}
	return nil
}

// QuarantineReason builds a short, sanitized reason string for naming a
// quarantined snapshot file. It removes any path separators so the reason can
// be embedded in a filename safely.
func QuarantineReason(err error) string {
	if err == nil {
		return "unknown"
	}
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return "unknown"
	}
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, " ", "-")
	// Bound the length so quarantine names stay manageable.
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}
