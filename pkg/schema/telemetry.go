package schema

import "time"

// ============================================================
// Claude Code hook payload types
// Source: https://docs.anthropic.com/en/docs/claude-code/hooks
// ============================================================

// ClaudeHookEvent is the JSON object Claude Code sends to hook scripts on stdin.
type ClaudeHookEvent struct {
	Event     string          `json:"event"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   ClaudePayload   `json:"payload"`
}

// ClaudePayload contains event-specific data from Claude Code.
type ClaudePayload struct {
	SessionID     string                 `json:"session_id,omitempty"`
	TranscriptPath string                `json:"transcript_path,omitempty"`
	Model         *string                `json:"model,omitempty"`
	ContextLength *int                   `json:"context_length,omitempty"`
	StatusLine    *ClaudeStatusLineInput `json:"status_line,omitempty"`
	Prompt        *string                `json:"prompt,omitempty"`
	StopReason    *string                `json:"stop_reason,omitempty"`
	ErrorCategory *string                `json:"error_category,omitempty"`
}

// ClaudeStatusLineInput is the JSON Claude Code sends to the status line command on stdin.
// Source: https://docs.anthropic.com/en/docs/claude-code/statusline
type ClaudeStatusLineInput struct {
	Model          ModelStatus          `json:"model"`
	SessionID      string               `json:"session_id"`
	TranscriptPath string               `json:"transcript_path"`
	ContextWindow  ContextWindowStatus  `json:"context_window"`
	Cost           CostStatus           `json:"cost,omitempty"`
	RateLimits     RateLimitStatus      `json:"rate_limits,omitempty"`
	Workspace      WorkspaceStatus      `json:"workspace,omitempty"`
}

// ModelStatus from Claude status line.
type ModelStatus struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name,omitempty"`
}

// ContextWindowStatus from Claude status line.
type ContextWindowStatus struct {
	TotalInputTokens  int64             `json:"total_input_tokens"`
	TotalOutputTokens int64             `json:"total_output_tokens"`
	CurrentUsage      CurrentUsage      `json:"current_usage"`
	ContextWindowSize int64             `json:"context_window_size"`
	UsedPercentage    float64           `json:"used_percentage"`
}

// CurrentUsage breaks down the latest API response token usage.
type CurrentUsage struct {
	InputTokens                int64 `json:"input_tokens"`
	OutputTokens               int64 `json:"output_tokens"`
	CacheCreationInputTokens   int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens       int64 `json:"cache_read_input_tokens"`
}

// CostStatus from Claude status line.
type CostStatus struct {
	TotalCostUSD      float64 `json:"total_cost_usd,omitempty"`
	TotalDurationMs   int64   `json:"total_duration_ms,omitempty"`
}

// RateLimitStatus from Claude status line (Claude.ai subscribers only).
type RateLimitStatus struct {
	FiveHour  RateLimitBucket `json:"five_hour,omitempty"`
	SevenDay  RateLimitBucket `json:"seven_day,omitempty"`
}

// RateLimitBucket describes a rate limit time window.
type RateLimitBucket struct {
	UsedPercentage float64 `json:"used_percentage,omitempty"`
	ResetsAt       string  `json:"resets_at,omitempty"`
}

// WorkspaceStatus from Claude status line.
type WorkspaceStatus struct {
	CurrentDir  string `json:"current_dir,omitempty"`
	ProjectDir  string `json:"project_dir,omitempty"`
}

// ============================================================
// Warning output types
// ============================================================

// ClaudeWarningOutput is the JSON a UserPromptSubmit hook returns to show a user-visible warning.
type ClaudeWarningOutput struct {
	Continue       bool   `json:"continue"`
	SystemMessage  string `json:"systemMessage,omitempty"`
	SuppressOutput bool   `json:"suppressOutput,omitempty"`
}

// CodexWarningOutput is the JSON a Codex hook returns (no suppressOutput).
type CodexWarningOutput struct {
	Continue      bool   `json:"continue"`
	SystemMessage string `json:"systemMessage,omitempty"`
}

// ============================================================
// Codex hook payload types
// Source: https://developers.openai.com/codex/hooks
// ============================================================

// CodexHookEvent is the JSON Codex sends to hook scripts on stdin.
type CodexHookEvent struct {
	Event     string        `json:"event"`
	Timestamp time.Time     `json:"timestamp"`
	Payload   CodexPayload  `json:"payload"`
}

// CodexPayload contains event-specific data from Codex.
type CodexPayload struct {
	SessionID       string            `json:"session_id,omitempty"`
	Model           *string           `json:"model,omitempty"`
	ContextLength   *int64            `json:"context_length,omitempty"`
	Prompt          *string           `json:"prompt,omitempty"`
	ConversationID  *string           `json:"conversation_id,omitempty"`
	ErrorCategory   *string           `json:"error_category,omitempty"`
}

// ============================================================
// Field semantics
// ============================================================
//
// Authoritative fields (from client status line):
//   - ClaudeStatusLineInput.ContextWindow.* — live context snapshot
//   - ClaudeStatusLineInput.Model.* — current model
//
// Estimated fields (from hooks):
//   - Prompt length estimates are approximate
//   - Token counts from UserPromptSubmit are pre-response, not post-response
//   - No hook provides cumulative session totals
//
// Unavailable fields:
//   - FreeInference does not expose /v1/usage, /v1/account, or rate limit headers
//   - account_usage remains null in v1 unless FreeInference adds an endpoint
//   - observed_session_usage fields are null until sufficient samples collected
//
// Field rules:
//   - null = not exposed / not collected yet
//   - zero = explicitly reported as zero (legitimate value)
//   - Never convert null to zero
//   - Never accumulate status line refreshes (same response may be read multiple times)