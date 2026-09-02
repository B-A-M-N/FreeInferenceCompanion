package schema

import (
	"errors"
	"fmt"
	"math"
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
	if s.Trace != nil {
		if err := validateTraceInfo(s.Trace); err != nil {
			return err
		}
	}
	if s.LastFailure != nil {
		if err := validateFailureRecord(s.LastFailure); err != nil {
			return err
		}
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

	// Used percentage, when present, must be in [0, 100] and finite.
	if s.LiveContext != nil && s.LiveContext.UsedPercentage != nil {
		p := *s.LiveContext.UsedPercentage
		if math.IsNaN(p) || math.IsInf(p, 0) {
			return fmt.Errorf("used_percentage is not finite: %v", p)
		}
		if p < 0 || p > 100 {
			return fmt.Errorf("used_percentage out of range: %f", p)
		}
	}

	// Remaining percentage, when present, must be in [0, 100] and finite.
	if s.LiveContext != nil && s.LiveContext.RemainingPercentage != nil {
		r := *s.LiveContext.RemainingPercentage
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return fmt.Errorf("remaining_percentage is not finite: %v", r)
		}
		if r < 0 || r > 100 {
			return fmt.Errorf("remaining_percentage out of range: %f", r)
		}
	}

	// Token counts must be non-negative when present.
	if s.LiveContext != nil {
		lc := s.LiveContext
		switch lc.TotalTokenSemantics {
		case "", TokenSemanticsCurrentContext, TokenSemanticsCumulativeSession, TokenSemanticsUnknown:
		default:
			return fmt.Errorf("invalid total_token_semantics %q", lc.TotalTokenSemantics)
		}
		if lc.TotalInputTokens != nil && *lc.TotalInputTokens < 0 {
			return fmt.Errorf("total_input_tokens is negative: %d", *lc.TotalInputTokens)
		}
		if lc.TotalOutputTokens != nil && *lc.TotalOutputTokens < 0 {
			return fmt.Errorf("total_output_tokens is negative: %d", *lc.TotalOutputTokens)
		}
		if lc.ContextWindowSize != nil && *lc.ContextWindowSize < 0 {
			return fmt.Errorf("context_window_size is negative: %d", *lc.ContextWindowSize)
		}
	}

	// Cache analysis shares, when present, must be in [0, 1] and finite.
	if s.CacheAnalysis != nil {
		ca := s.CacheAnalysis
		switch ca.Availability {
		case "", CacheTelemetryAvailable, CacheTelemetryPartial,
			CacheTelemetryUnavailable, CacheTelemetryUnsupported, CacheTelemetryStale:
		default:
			return fmt.Errorf("invalid cache telemetry availability %q", ca.Availability)
		}
		if ca.RequestSamples < 0 || ca.ObservationCount < 0 || ca.UsableSampleCount < 0 || ca.AnalysisWindowCount < 0 {
			return errors.New("cache analysis counts must be non-negative")
		}
		if ca.CacheReadShare != nil {
			v := *ca.CacheReadShare
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("cache_read_share is not finite: %v", v)
			}
			if v < 0 || v > 1 {
				return fmt.Errorf("cache_read_share out of range [0,1]: %f", v)
			}
		}
		if ca.CacheCreationShare != nil {
			v := *ca.CacheCreationShare
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("cache_creation_share is not finite: %v", v)
			}
			if v < 0 || v > 1 {
				return fmt.Errorf("cache_creation_share out of range [0,1]: %f", v)
			}
		}
		if ca.FreshInputShare != nil {
			v := *ca.FreshInputShare
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("fresh_input_share is not finite: %v", v)
			}
			if v < 0 || v > 1 {
				return fmt.Errorf("fresh_input_share out of range [0,1]: %f", v)
			}
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

func validateFailureRecord(f *FailureRecord) error {
	if f == nil {
		return nil
	}
	if len(f.Category) > 64 || len(f.Source) > 128 || len(f.TransportClass) > 64 ||
		len(f.ProviderErrorType) > 128 || len(f.ErrorOrigin) > 64 || len(f.RequestReference) > 128 {
		return errors.New("failure metadata field is too long")
	}
	for name, value := range map[string]string{
		"category": f.Category, "source": f.Source, "transport_class": f.TransportClass,
		"provider_error_type": f.ProviderErrorType, "error_origin": f.ErrorOrigin,
		"request_reference": f.RequestReference,
	} {
		for _, r := range value {
			if r < 0x20 || r > 0x7e {
				return fmt.Errorf("failure %s contains unsafe characters", name)
			}
		}
	}
	if f.HTTPStatus != nil && (*f.HTTPStatus < 400 || *f.HTTPStatus > 599) {
		return fmt.Errorf("failure http_status out of range: %d", *f.HTTPStatus)
	}
	if f.RetryAfterSeconds != nil && (*f.RetryAfterSeconds < 0 || *f.RetryAfterSeconds > 7*24*60*60) {
		return fmt.Errorf("failure retry_after_seconds out of range: %d", *f.RetryAfterSeconds)
	}
	return nil
}

func validateTraceInfo(t *TraceInfo) error {
	if t.Source != TraceSourceCompanionGenerated && t.Source != TraceSourceExistingHeader && t.Source != TraceSourceNone {
		return fmt.Errorf("invalid trace source %q", t.Source)
	}
	if t.Header != "" && t.Header != TraceHeaderSessionID {
		return fmt.Errorf("invalid trace header %q", t.Header)
	}
	if t.Client != "" && t.Client != ClientClaudeCode && t.Client != ClientCodex {
		return fmt.Errorf("invalid trace client %q", t.Client)
	}
	if t.Provider != "" && t.Provider != ProviderFreeInference {
		return fmt.Errorf("invalid trace provider %q", t.Provider)
	}
	for name, value := range map[string]string{"client": t.Client, "provider": t.Provider, "endpoint_origin": t.EndpointOrigin} {
		if len(value) > 256 {
			return fmt.Errorf("trace %s is too long", name)
		}
		for _, r := range value {
			if r < 0x20 || r > 0x7e {
				return fmt.Errorf("trace %s contains unsafe characters", name)
			}
		}
	}
	if t.SessionID != "" {
		if len(t.SessionID) != len("fic-v1-")+26 || !strings.HasPrefix(t.SessionID, "fic-v1-") {
			return fmt.Errorf("invalid trace session id")
		}
		for _, r := range t.SessionID[len("fic-v1-"):] {
			if !((r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')) {
				return fmt.Errorf("invalid trace session id")
			}
		}
	}
	if !t.Enabled && t.SessionID != "" {
		return fmt.Errorf("disabled trace cannot have a session id")
	}
	if t.Enabled && t.Source == TraceSourceNone {
		return fmt.Errorf("enabled trace cannot have an empty source")
	}
	if t.Enabled && (t.SessionID == "" || t.Header != TraceHeaderSessionID) {
		return fmt.Errorf("enabled trace is missing its correlation header or id")
	}
	if t.Enabled && !t.Verified {
		return fmt.Errorf("durable trace provenance is not receipt-verified")
	}
	return nil
}

// MigrateSnapshot upgrades an older snapshot in-place to the current schema
// version. Returns ErrUnsupportedSchema when the version is too old to migrate
// or newer than this binary understands.
//
// Migrations are strictly additive where possible: they populate new fields
// from legacy equivalents and bump SchemaVersion. When a legacy field has no
// modern equivalent (because the newer schema splits what was a single value
// into several), the migration leaves the newer fields null and drops the
// legacy value — reconstructing a synthesized split would be fabrication.
func MigrateSnapshot(s *Snapshot) error {
	if s == nil {
		return errors.New("nil snapshot")
	}
	if s.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("%w: file=%d, current=%d", ErrUnsupportedSchema, s.SchemaVersion, CurrentSchemaVersion)
	}

	for s.SchemaVersion < CurrentSchemaVersion {
		switch s.SchemaVersion {
		case 0, 1:
			// v1 → v2: v1 stored session totals and latest-request usage in a
			// flat structure. v2 splits these into LiveContext.Total* (session
			// totals) and LiveContext.LatestRequest (per-request breakdown).
			// The v1 flat representation cannot be cleanly decomposed: it had
			// a single "input_tokens" and "output_tokens" that represented
			// the latest request, and no separate session-total concept.
			// We migrate by leaving LiveContext null (rather than fabricating
			// a split) — the next status-line update will populate it fresh.
			s.LiveContext = nil
			// v2 also introduced the rolling CacheAnalysis. A v1 snapshot had
			// observations but no derived analysis. Leave it nil — AnalyzeCache
			// will rebuild it on the next status-line update.
			s.CacheAnalysis = nil
			s.SchemaVersion = 2
		case 2:
			// v2 → v3: v2 did not record whether Claude total tokens were
			// current-context or cumulative-session values. Preserve existing
			// observations but mark the semantics unknown so no old cumulative
			// counter is used as active context. New cache counters are rebuilt
			// by the next status-line update.
			if s.LiveContext != nil && s.LiveContext.TotalTokenSemantics == "" {
				s.LiveContext.TotalTokenSemantics = TokenSemanticsUnknown
			}
			if s.CacheAnalysis != nil {
				if s.CacheAnalysis.ObservationCount == 0 {
					s.CacheAnalysis.ObservationCount = s.CacheAnalysis.RequestSamples
				}
				if s.CacheAnalysis.Availability == "" {
					s.CacheAnalysis.Availability = CacheTelemetryUnavailable
				}
			}
			s.SchemaVersion = 3
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
