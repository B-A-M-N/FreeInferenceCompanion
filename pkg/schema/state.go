package schema

import "time"

// StateVersion is the schema version for state files.
const StateVersion = 1

// Snapshot is the per-session state persisted to disk.
type Snapshot struct {
	SchemaVersion  int              `json:"schema_version"`
	PluginVersion  string           `json:"plugin_version"`
	Client         ClientInfo       `json:"client"`
	Session        SessionInfo      `json:"session"`
	Model          ModelInfo        `json:"model"`
	LiveContext    *LiveContext     `json:"live_context"`
	ObservedUsage  *ObservedUsage   `json:"observed_session_usage"`
	Pressure       PressureState    `json:"pressure"`
	CacheAnalysis  *CacheAnalysis   `json:"cache_analysis"`
	Activity       ActivityState    `json:"activity"`
	Warnings       WarningState     `json:"warnings"`
	LastFailure    *FailureRecord   `json:"last_failure"`
	Compaction     CompactionState  `json:"compaction"`
}

// ClientInfo identifies the coding-agent client.
type ClientInfo struct {
	Type    string  `json:"type"`    // "claude-code" or "codex"
	Version *string `json:"version"` // client version, null if unavailable
}

// SessionInfo tracks session lifecycle.
type SessionInfo struct {
	ID           string    `json:"id"`
	StartedAt    time.Time `json:"started_at"`
	LastEventAt  time.Time `json:"last_event_at"`
	Status       string    `json:"status"` // "active", "stopped", "completed"
}

// ModelInfo describes the current model.
type ModelInfo struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	ContextLength   int    `json:"context_length"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	MetadataSource  string `json:"metadata_source"` // "freeinference_catalog", "client_statusline"
	AccessState     string `json:"access_state"`     // "available", "restricted", "unknown"
}

// LiveContext is the authoritative current context snapshot from the client status line.
type LiveContext struct {
	Source                    string    `json:"source"`
	ObservedAt                time.Time `json:"observed_at"`
	FreshInputTokens          *int64    `json:"fresh_input_tokens"`
	CacheCreationInputTokens  *int64    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens      *int64    `json:"cache_read_input_tokens"`
	OutputTokens              *int64    `json:"output_tokens"`
	ContextWindowSize         *int64    `json:"context_window_size"`
	UsedPercentage            *float64  `json:"used_percentage"`
}

// ObservedUsage tracks best-effort local session observations.
type ObservedUsage struct {
	Authoritative             bool   `json:"authoritative"`
	SampleCount               int    `json:"sample_count"`
	FreshInputTokens          *int64 `json:"fresh_input_tokens"`
	CacheCreationInputTokens  *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens      *int64 `json:"cache_read_input_tokens"`
	OutputTokens              *int64 `json:"output_tokens"`
}

// PressureState represents the context pressure state machine.
type PressureState struct {
	State              string     `json:"state"`          // "unknown","healthy","watch","warn","critical","recovering"
	PreviousState      string     `json:"previous_state"`
	ProjectedPercentage *float64  `json:"projected_percentage"`
	ProjectionConfidence string   `json:"projection_confidence"` // "low", "medium", "high"
	Reason             *string    `json:"reason"`
	ChangedAt          time.Time  `json:"changed_at"`
}

// CacheAnalysis tracks rolling window cache metrics.
type CacheAnalysis struct {
	RequestSamples    int      `json:"request_samples"`
	CacheReadShare    *float64 `json:"cache_read_share"`
	CacheCreationShare *float64 `json:"cache_creation_share"`
	FreshInputShare   *float64 `json:"fresh_input_share"`
	Trend             string   `json:"trend"` // "rising", "stable", "declining", "insufficient_data"
}

// ActivityState tracks turn-level activity from lifecycle hooks.
type ActivityState struct {
	TurnActive   bool       `json:"turn_active"`
	TurnStartedAt *time.Time `json:"turn_started_at"`
	Confidence   string     `json:"confidence"` // "client-lifecycle"
}

// WarningState tracks active and historical warnings.
type WarningState struct {
	Active        []ActiveWarning `json:"active"`
	HistoryCount  int             `json:"history_count"`
}

// ActiveWarning is a currently-active warning.
type ActiveWarning struct {
	Reason        string    `json:"reason"`        // "context_high", "cache_low", "projected_overflow", "repeated_timeout"
	Severity      string    `json:"severity"`      // "watch", "warn", "critical"
	Message       string    `json:"message"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
	LastShownAt   time.Time `json:"last_shown_at"`
}

// FailureRecord stores the last failure from StopFailure hooks.
type FailureRecord struct {
	Category   string    `json:"category"`    // "rate_limit", "overloaded", "authentication_failed", "invalid_request", "model_not_found", "server_error", "max_output_tokens"
	ObservedAt time.Time `json:"observed_at"`
	Source     string    `json:"source"`      // "claude_stop_failure"
}

// CompactionState tracks pending and completed compaction operations.
type CompactionState struct {
	Pending     bool            `json:"pending"`
	LastResult  *CompactionResult `json:"last_result"`
}

// CompactionResult records the outcome of a compaction.
type CompactionResult struct {
	At           time.Time `json:"at"`
	PreTokens    *int64    `json:"pre_tokens"`
	PostTokens   *int64    `json:"post_tokens"`
	ReductionPct *float64  `json:"reduction_pct"`
}

// GlobalState is the shared provider-level cache, not tied to any session.
type GlobalState struct {
	Health          *HealthCache       `json:"health"`
	Models          *ModelsCache       `json:"models"`
	AccountUsage    *AccountUsage      `json:"account_usage"`
	CircuitBreakers []CircuitBreaker   `json:"circuit_breakers"`
}

// HealthCache caches provider health information.
type HealthCache struct {
	FetchedAt      time.Time `json:"fetched_at"`
	Status         string    `json:"status"`          // "healthy", "degraded", "unreachable", "unknown"
	HealthyCount   *int      `json:"healthy_count"`
	UnhealthyCount *int      `json:"unhealthy_count"`
	Source         string    `json:"source"`
}

// ModelsCache caches the model catalog.
type ModelsCache struct {
	FetchedAt time.Time     `json:"fetched_at"`
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
	Endpoint      string     `json:"endpoint"`
	State         string     `json:"state"` // "closed", "open", "half-open"
	FailureCount  int        `json:"failure_count"`
	LastFailureAt *time.Time `json:"last_failure_at"`
	NextRetryAt   *time.Time `json:"next_retry_at"`
}

// ============================================================
// Warning constants
// ============================================================

const (
	WarningReasonContextHigh       = "context_high"
	WarningReasonCacheLow          = "cache_low"
	WarningReasonProjectedOverflow = "projected_overflow"
	WarningReasonRepeatedTimeout   = "repeated_timeout"

	WarningSeverityWatch    = "watch"
	WarningSeverityWarn     = "warn"
	WarningSeverityCritical = "critical"
)

// ============================================================
// Pressure state constants
// ============================================================

const (
	PressureUnknown   = "unknown"
	PressureHealthy   = "healthy"
	PressureWatch     = "watch"
	PressureWarn      = "warn"
	PressureCritical  = "critical"
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
)