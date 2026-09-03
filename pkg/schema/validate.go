package schema

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// SupportedSchemaRange is the range of schema versions this build can safely
// load. Versions below MinSupportedSchemaVersion trigger MigrateSnapshot;
// versions above CurrentSchemaVersion are rejected as forward-incompatible.
const (
	MinSupportedSchemaVersion = 1
	// CurrentSchemaVersion mirrors StateVersion; declared separately to avoid
	// an import cycle in test code.
	CurrentSchemaVersion = StateVersion

	// Metadata cache freshness is deliberately conservative. These values are
	// also used by renderers so an old successful response cannot look live.
	DefaultHealthMaxAge       = 2 * time.Minute
	DefaultAccountUsageMaxAge = 60 * time.Minute

	MaxCatalogModels       = 512
	MaxCatalogFeatures     = 64
	MaxCatalogPricing      = 32
	MaxCatalogTextBytes    = 512
	MaxCatalogModelIDBytes = 256
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

	// Diagnosis scores are heuristic values, never percentages or arbitrary
	// weights. Keep malformed persisted values out of renderers and reports.
	if s.CacheDiagnosis != nil {
		if !validHeuristicScore(s.CacheDiagnosis.Confidence) {
			return fmt.Errorf("cache diagnosis confidence out of range [0,1]: %f", s.CacheDiagnosis.Confidence)
		}
		for i, cause := range s.CacheDiagnosis.CandidateCauses {
			if !validHeuristicScore(cause.HeuristicScore) {
				return fmt.Errorf("cache diagnosis candidate cause %d heuristic score out of range [0,1]: %f", i, cause.HeuristicScore)
			}
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

func validHeuristicScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
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

// ValidatePublicStatusCache checks the bounded, unauthenticated monitor
// artifact before it is used or written. A failed refresh may intentionally
// leave Models empty, but any supplied model/sample must be safe and finite.
func ValidatePublicStatusCache(c *PublicStatusCache) error {
	if c == nil {
		return errors.New("nil public status cache")
	}
	if len(c.Source) > 256 || len(c.CycleError) > 200 || len(c.LastError) > 200 {
		return errors.New("public status cache field is too long")
	}
	for name, value := range map[string]string{
		"source": c.Source, "cycle_error": c.CycleError, "last_error": c.LastError,
	} {
		for _, r := range value {
			if r < 0x20 || r > 0x7e {
				return fmt.Errorf("public status %s contains unsafe characters", name)
			}
		}
	}
	if c.Total < 0 || c.Healthy < 0 || c.Unhealthy < 0 ||
		c.Healthy > c.Total || c.Unhealthy > c.Total-c.Healthy {
		return errors.New("public status cache contains inconsistent model counts")
	}
	if c.ConsecutiveFailure < 0 || c.ConsecutiveFailure > 1000 {
		return errors.New("public status cache failure count is out of range")
	}
	if len(c.Models) > MaxPublicStatusModels {
		return errors.New("public status cache contains too many models")
	}
	seen := make(map[string]struct{}, len(c.Models))
	for _, model := range c.Models {
		if model.ModelID == "" || len(model.ModelID) > 256 {
			return errors.New("public status cache model id is invalid")
		}
		for _, r := range model.ModelID {
			if r < 0x20 || r > 0x7e {
				return errors.New("public status cache model id contains unsafe characters")
			}
		}
		if _, exists := seen[model.ModelID]; exists {
			return errors.New("public status cache contains duplicate model ids")
		}
		seen[model.ModelID] = struct{}{}
		if model.UptimeRatio != nil && (math.IsNaN(*model.UptimeRatio) || math.IsInf(*model.UptimeRatio, 0) || *model.UptimeRatio < 0 || *model.UptimeRatio > 1) {
			return errors.New("public status cache uptime ratio is invalid")
		}
		if err := validatePublicStatusCacheSample(model.Latest); err != nil {
			return err
		}
		if len(model.History) > MaxPublicStatusSamplesPerModel {
			return errors.New("public status cache contains too many samples")
		}
		for i := range model.History {
			if err := validatePublicStatusCacheSample(&model.History[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateHealthCache checks the authenticated health artifact before it is
// persisted or used for display. Counts are optional because older providers
// sometimes return only a status, but supplied counts must be sane.
func ValidateHealthCache(c *HealthCache) error {
	if c == nil {
		return errors.New("nil health cache")
	}
	if c.FetchedAt.IsZero() {
		return errors.New("health cache timestamp is missing")
	}
	switch c.Status {
	case "healthy", "degraded", "unreachable", "unknown":
	default:
		return fmt.Errorf("invalid health status %q", c.Status)
	}
	if c.HealthyCount != nil && *c.HealthyCount < 0 {
		return errors.New("health healthy count is negative")
	}
	if c.UnhealthyCount != nil && *c.UnhealthyCount < 0 {
		return errors.New("health unhealthy count is negative")
	}
	if len(c.Source) > MaxCatalogTextBytes || !safeMetadataText(c.Source) {
		return errors.New("health source is invalid")
	}
	return nil
}

// ValidateModelsCache checks the catalog before it is persisted or trusted by
// the CLI. This bounds upstream-controlled strings and rejects duplicate or
// contradictory records.
func ValidateModelsCache(c *ModelsCache) error {
	if c == nil {
		return errors.New("nil models cache")
	}
	if c.FetchedAt.IsZero() {
		return errors.New("models cache timestamp is missing")
	}
	if len(c.Models) > MaxCatalogModels {
		return fmt.Errorf("models cache contains too many models: %d", len(c.Models))
	}
	seen := make(map[string]struct{}, len(c.Models))
	for i := range c.Models {
		m := &c.Models[i]
		if m.ID == "" || len(m.ID) > MaxCatalogModelIDBytes || !safeMetadataText(m.ID) {
			return fmt.Errorf("model %d has an invalid id", i)
		}
		if _, ok := seen[m.ID]; ok {
			return fmt.Errorf("models cache contains duplicate model id %q", m.ID)
		}
		seen[m.ID] = struct{}{}
		if len(m.Name) > MaxCatalogTextBytes || !safeMetadataText(m.Name) {
			return fmt.Errorf("model %q has an invalid name", m.ID)
		}
		if m.ContextLength < 0 || m.ContextLength > 1<<30 || m.MaxOutputLength < 0 || m.MaxOutputLength > 1<<30 {
			return fmt.Errorf("model %q has invalid token limits", m.ID)
		}
		switch m.AccessState {
		case "", AccessAvailable, AccessRestricted, AccessUnknown:
		default:
			return fmt.Errorf("model %q has invalid access state %q", m.ID, m.AccessState)
		}
		if len(m.Features) > MaxCatalogFeatures {
			return fmt.Errorf("model %q has too many features", m.ID)
		}
		for _, feature := range m.Features {
			if feature == "" || len(feature) > MaxCatalogTextBytes || !safeMetadataText(feature) {
				return fmt.Errorf("model %q has an invalid feature", m.ID)
			}
		}
		if len(m.Pricing) > MaxCatalogPricing {
			return fmt.Errorf("model %q has too many pricing fields", m.ID)
		}
		for key, value := range m.Pricing {
			if key == "" || len(key) > MaxCatalogTextBytes || len(value) > MaxCatalogTextBytes ||
				!safeMetadataText(key) || !safeMetadataText(value) {
				return fmt.Errorf("model %q has invalid pricing metadata", m.ID)
			}
		}
	}
	return nil
}

// ValidateAccountUsage checks that quota data is explicitly authoritative and
// internally consistent. A syntactically valid response is not enough to
// become trusted account state.
func ValidateAccountUsage(a *AccountUsage) error {
	if a == nil {
		return errors.New("nil account usage")
	}
	if !a.Authoritative {
		return errors.New("account usage is not authoritative")
	}
	if a.FetchedAt.IsZero() {
		return errors.New("account usage timestamp is missing")
	}
	if a.RequestsUsed == nil && a.RequestsLimit == nil && a.TokensUsed == nil && a.TokensLimit == nil {
		return errors.New("account usage contains no quota fields")
	}
	if err := validateQuotaPair("requests", a.RequestsUsed, a.RequestsLimit); err != nil {
		return err
	}
	return validateQuotaPair("tokens", a.TokensUsed, a.TokensLimit)
}

func validateQuotaPair(name string, used, limit *int64) error {
	if used != nil && *used < 0 {
		return fmt.Errorf("account usage %s used is negative", name)
	}
	if limit != nil && *limit < 0 {
		return fmt.Errorf("account usage %s limit is negative", name)
	}
	if used != nil && limit != nil && *used > *limit {
		return fmt.Errorf("account usage %s used exceeds limit", name)
	}
	return nil
}

// ValidateAccountUsageCapability validates the negotiated endpoint state.
func ValidateAccountUsageCapability(c *AccountUsageCapability) error {
	if c == nil {
		return errors.New("nil account usage capability")
	}
	switch c.State {
	case CapabilityUnknown, CapabilitySupported, CapabilityUnsupported, CapabilityForbidden:
	default:
		return fmt.Errorf("invalid account usage capability %q", c.State)
	}
	if c.CheckedAt.IsZero() {
		return errors.New("account usage capability timestamp is missing")
	}
	return nil
}

// HasUsableAccountUsage reports whether quota data is validated, explicitly
// supported, and fresh enough for current display or ETA calculations.
func (g *GlobalState) HasUsableAccountUsage(now time.Time, maxAge time.Duration) bool {
	if g == nil || g.AccountUsage == nil || g.AccountUsageCapability == nil ||
		g.AccountUsageCapability.State != CapabilitySupported || maxAge <= 0 {
		return false
	}
	if ValidateAccountUsage(g.AccountUsage) != nil || ValidateAccountUsageCapability(g.AccountUsageCapability) != nil {
		return false
	}
	age := now.Sub(g.AccountUsage.FetchedAt)
	return age >= 0 && age <= maxAge
}

func safeMetadataText(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u007f' {
			return false
		}
	}
	return true
}

func validatePublicStatusCacheSample(sample *PublicStatusSampleCache) error {
	if sample == nil {
		return nil
	}
	if sample.CheckedAt.IsZero() {
		return errors.New("public status cache sample timestamp is missing")
	}
	if sample.LatencyMs != nil && (*sample.LatencyMs < 0 || *sample.LatencyMs > 7*24*60*60*1000) {
		return errors.New("public status cache latency is out of range")
	}
	if sample.TTFTMs != nil && (*sample.TTFTMs < 0 || *sample.TTFTMs > 7*24*60*60*1000) {
		return errors.New("public status cache ttft is out of range")
	}
	if sample.CompletionTokens != nil && (*sample.CompletionTokens < 0 || *sample.CompletionTokens > 1<<40) {
		return errors.New("public status cache completion tokens are out of range")
	}
	if sample.ThroughputTps != nil && (math.IsNaN(*sample.ThroughputTps) || math.IsInf(*sample.ThroughputTps, 0) || *sample.ThroughputTps < 0 || *sample.ThroughputTps > 1e9) {
		return errors.New("public status cache throughput is invalid")
	}
	if len(sample.Error) > 200 {
		return errors.New("public status cache sample error is too long")
	}
	for _, r := range sample.Error {
		if r < 0x20 || r > 0x7e {
			return errors.New("public status cache sample error contains unsafe characters")
		}
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
			if s.CacheDiagnosis != nil {
				for i := range s.CacheDiagnosis.CandidateCauses {
					cause := &s.CacheDiagnosis.CandidateCauses[i]
					if cause.Likelihood != nil {
						if validHeuristicScore(*cause.Likelihood) {
							cause.HeuristicScore = *cause.Likelihood
						}
						cause.Likelihood = nil
					}
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
