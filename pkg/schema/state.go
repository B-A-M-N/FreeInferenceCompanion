package schema

import "time"

// StateVersion is the schema version for state files.
const StateVersion = 2

// MaxClockSkew is the maximum permitted clock skew for timestamp validation.
// Timestamps more than this far in the future are treated as invalid.
const MaxClockSkew = 5 * time.Minute

// SanitizeTimestamp returns a clamped timestamp. If the timestamp is in the
// future beyond MaxClockSkew, it is clamped to now (the source is marked
// invalid). This prevents future timestamps from suppressing warnings, keeping
// caches fresh indefinitely, or producing negative age calculations.
func SanitizeTimestamp(ts time.Time, now time.Time) time.Time {
	if ts.IsZero() {
		return ts
	}
	if ts.After(now.Add(MaxClockSkew)) {
		return now
	}
	return ts
}

// Snapshot is the per-session state persisted to disk.
type Snapshot struct {
	SchemaVersion     int                `json:"schema_version"`
	PluginVersion     string             `json:"plugin_version"`
	Client            ClientInfo         `json:"client"`
	Session           SessionInfo        `json:"session"`
	Provider          ProviderInfo       `json:"provider"`
	Model             ModelInfo          `json:"model"`
	LiveContext       *LiveContext       `json:"live_context"`
	Pressure          PressureState      `json:"pressure"`
	CacheAnalysis     *CacheAnalysis     `json:"cache_analysis"`
	UsageObservations []UsageObservation `json:"usage_observations,omitempty"`
	Activity          ActivityState      `json:"activity"`
	Warnings          WarningState       `json:"warnings"`
	LastFailure       *FailureRecord     `json:"last_failure"`
	Compaction        CompactionState    `json:"compaction"`
	// ActivationID is the provider-level identity under which this session
	// was recorded. Rendering only uses data from this snapshot when the
	// current runtime activation produces the same ActivationID.
	ActivationID string `json:"activation_id,omitempty"`
}

// ClientInfo identifies the coding-agent client.
type ClientInfo struct {
	Type    string  `json:"type"`    // "claude-code" or "codex"
	Version *string `json:"version"` // client version, null if unavailable
}

// ProviderInfo records the detected API provider for this session.
type ProviderInfo struct {
	Name      string `json:"name"` // "freeinference" or "unknown"
	Confirmed bool   `json:"confirmed"`
	Source    string `json:"source"` // env var or mechanism that confirmed the provider
	BaseURL   string `json:"base_url,omitempty"`
}

// SessionInfo tracks session lifecycle.
type SessionInfo struct {
	ID          string     `json:"id"`
	StartedAt   time.Time  `json:"started_at"`
	LastEventAt time.Time  `json:"last_event_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	Status      string     `json:"status"` // "active", "stopped", "completed"
}

// ModelInfo describes the current model.
// Null numeric fields mean "unknown" — never convert null to zero.
type ModelInfo struct {
	ID              string  `json:"id"`
	DisplayName     *string `json:"display_name,omitempty"`
	ContextLength   *int64  `json:"context_length"`
	MaxOutputTokens *int64  `json:"max_output_tokens"`
	MetadataSource  string  `json:"metadata_source"` // "freeinference_catalog", "client_statusline", "client_hook"
	AccessState     string  `json:"access_state"`    // "available", "restricted", "unknown"
}

// LiveContext is the authoritative current context snapshot from the client status line.
// Session totals (TotalInputTokens/TotalOutputTokens) are kept separate from the
// latest API request usage (LatestRequest).
type LiveContext struct {
	Source              string        `json:"source"`
	ObservedAt          time.Time     `json:"observed_at"`
	TotalInputTokens    *int64        `json:"total_input_tokens"`
	TotalOutputTokens   *int64        `json:"total_output_tokens"`
	ContextWindowSize   *int64        `json:"context_window_size"`
	UsedPercentage      *float64      `json:"used_percentage"`
	RemainingPercentage *float64      `json:"remaining_percentage,omitempty"`
	LatestRequest       *RequestUsage `json:"latest_request"`
}

// RequestUsage is the token breakdown of the latest API request.
type RequestUsage struct {
	FreshInputTokens         *int64 `json:"fresh_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
}

// UsageObservation is one unique status-line usage sample.
// Duplicate renders of the same response share a fingerprint and are not re-recorded.
// Pointer fields are used for optional/missing values — nil means "unknown",
// not zero. This distinguishes a genuine zero-token response from a missing
// measurement.
type UsageObservation struct {
	Fingerprint              string    `json:"fingerprint"`
	ObservedAt               time.Time `json:"observed_at"`
	ModelID                  string    `json:"model_id"`
	TotalInputTokens         *int64    `json:"total_input_tokens"`
	TotalOutputTokens        *int64    `json:"total_output_tokens"`
	FreshInputTokens         *int64    `json:"fresh_input_tokens"`
	CacheReadInputTokens     *int64    `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64    `json:"cache_creation_input_tokens"`
	OutputTokens             *int64    `json:"output_tokens"`
}

// PressureState represents the context pressure state machine.
type PressureState struct {
	State         string    `json:"state"` // "unknown","healthy","watch","warn","critical","recovering"
	PreviousState string    `json:"previous_state"`
	Reason        *string   `json:"reason"`
	ChangedAt     time.Time `json:"changed_at"`
}

// CacheAnalysis tracks rolling-window cache metrics over unique usage observations.
type CacheAnalysis struct {
	RequestSamples       int      `json:"request_samples"`
	CacheReadShare       *float64 `json:"cache_read_share"`
	CacheCreationShare   *float64 `json:"cache_creation_share"`
	FreshInputShare      *float64 `json:"fresh_input_share"`
	PreviousReadShare    *float64 `json:"previous_read_share"`
	Trend                string   `json:"trend"` // "rising", "stable", "declining", "insufficient_data"
	ConsecutiveLow       int      `json:"consecutive_low"`
	ConsecutiveRecovered int      `json:"consecutive_recovered"`
}

// ActivityState tracks turn-level activity from lifecycle hooks.
// TurnActive is a pointer so "unknown" (null) is distinct from "false".
type ActivityState struct {
	TurnActive    *bool      `json:"turn_active"`
	TurnStartedAt *time.Time `json:"turn_started_at,omitempty"`
	TurnEndedAt   *time.Time `json:"turn_ended_at,omitempty"`
	Confidence    string     `json:"confidence"` // "client-lifecycle"
}

// WarningState tracks active and historical warnings.
type WarningState struct {
	Active       []string `json:"active"`
	HistoryCount int      `json:"history_count"`
	// Persistent cooldown tracking (survives across hook invocations)
	ContextSeverity    string     `json:"context_severity,omitempty"`
	LastContextShownAt *time.Time `json:"last_context_shown_at,omitempty"`
	// Cache-low warning state
	CacheLowActive   bool       `json:"cache_low_active,omitempty"`
	LastCacheShownAt *time.Time `json:"last_cache_shown_at,omitempty"`
	// Cache TTL expiry warning state (prompt cache evicted due to idle time)
	CacheTTLWarningActive bool       `json:"cache_ttl_warning_active,omitempty"`
	LastCacheTTLShownAt   *time.Time `json:"last_cache_ttl_shown_at,omitempty"`
}

// FailureRecord stores the last failure from StopFailure hooks.
type FailureRecord struct {
	Category   string    `json:"category"` // "rate_limit", "overloaded", "authentication_failed", "invalid_request", "model_not_found", "server_error", "max_output_tokens"
	ObservedAt time.Time `json:"observed_at"`
	Source     string    `json:"source"` // "claude_stop_failure"
}

// CompactionState tracks pending and completed compaction operations.
type CompactionState struct {
	Pending                 bool              `json:"pending"`
	AwaitingPostObservation bool              `json:"awaiting_post_observation"`
	Trigger                 *string           `json:"trigger,omitempty"`
	InitiatedAt             *time.Time        `json:"initiated_at,omitempty"`
	PreTokens               *int64            `json:"pre_tokens,omitempty"`
	LastResult              *CompactionResult `json:"last_result"`
}

// CompactionResult records the outcome of a compaction.
type CompactionResult struct {
	At           time.Time `json:"at"`
	PreTokens    *int64    `json:"pre_tokens,omitempty"`
	PostTokens   *int64    `json:"post_tokens,omitempty"`
	ReductionPct *float64  `json:"reduction_pct,omitempty"`
	Trigger      string    `json:"trigger,omitempty"` // "manual" or "automatic"
}

// GlobalState is the shared provider-level cache, not tied to any session.
type GlobalState struct {
	Health          *HealthCache     `json:"health"`
	Models          *ModelsCache     `json:"models"`
	AccountUsage    *AccountUsage    `json:"account_usage"`
	CircuitBreakers []CircuitBreaker `json:"circuit_breakers"`
}

// HealthCache caches provider health information.
type HealthCache struct {
	FetchedAt      time.Time `json:"fetched_at"`
	Status         string    `json:"status"` // "healthy", "degraded", "unreachable", "unknown"
	HealthyCount   *int      `json:"healthy_count"`
	UnhealthyCount *int      `json:"unhealthy_count"`
	Source         string    `json:"source"`
}

// ModelsCache caches the model catalog.
type ModelsCache struct {
	FetchedAt time.Time      `json:"fetched_at"`
	Models    []CatalogModel `json:"models"`
}

// CatalogModel is a model entry from the FreeInference catalog.
type CatalogModel struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	ContextLength   int               `json:"context_length"`
	MaxOutputLength int               `json:"max_output_length"`
	AccessState     string            `json:"access_state"`
	Pricing         map[string]string `json:"pricing,omitempty"`
	Features        []string          `json:"features,omitempty"`
}

// AccountUsage stores FreeInference account-level usage when available.
type AccountUsage struct {
	Authoritative bool      `json:"authoritative"`
	FetchedAt     time.Time `json:"fetched_at"`
	RequestsUsed  *int64    `json:"requests_used"`
	RequestsLimit *int64    `json:"requests_limit"`
	TokensUsed    *int64    `json:"tokens_used"`
	TokensLimit   *int64    `json:"tokens_limit"`
}

// CircuitBreaker tracks per-endpoint circuit breaker state.
type CircuitBreaker struct {
	Endpoint      string     `json:"endpoint"` // "models", "health", "account-usage"
	State         string     `json:"state"`    // "closed", "open", "half-open"
	FailureCount  int        `json:"failure_count"`
	LastFailureAt *time.Time `json:"last_failure_at"`
	NextRetryAt   *time.Time `json:"next_retry_at"`
}

// ============================================================
// Warning constants
// ============================================================

const (
	WarningReasonContextHigh     = "context_high"
	WarningReasonCacheLow        = "cache_low"
	WarningReasonRepeatedTimeout = "repeated_timeout"

	WarningSeverityWatch    = "watch"
	WarningSeverityWarn     = "warn"
	WarningSeverityCritical = "critical"
)

// ============================================================
// Pressure state constants
// ============================================================

const (
	PressureUnknown    = "unknown"
	PressureHealthy    = "healthy"
	PressureWatch      = "watch"
	PressureWarn       = "warn"
	PressureCritical   = "critical"
	PressureRecovering = "recovering"
)

// ============================================================
// Activity / client constants
// ============================================================

const (
	ClientClaudeCode = "claude-code"
	ClientCodex      = "codex"

	ConfidenceClientLifecycle = "client-lifecycle"

	SessionActive    = "active"
	SessionStopped   = "stopped"
	SessionCompleted = "completed"

	CircuitClosed   = "closed"
	CircuitOpen     = "open"
	CircuitHalfOpen = "half-open"

	TrendRising           = "rising"
	TrendStable           = "stable"
	TrendDeclining        = "declining"
	TrendInsufficientData = "insufficient_data"

	AccessAvailable  = "available"
	AccessRestricted = "restricted"
	AccessUnknown    = "unknown"

	ProviderFreeInference = "freeinference"
	ProviderUnknown       = "unknown"
)
