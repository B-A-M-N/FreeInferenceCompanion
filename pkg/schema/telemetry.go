package schema

// ============================================================
// Claude Code hook input (flat schema)
// Source: https://code.claude.com/docs/en/hooks
// ============================================================

// ClaudeHookInput is the flat JSON object Claude Code sends to hook scripts on stdin.
// All fields are top-level — there is no nested "payload" object.
type ClaudeHookInput struct {
	// Common fields present on all hook events
	SessionID      string  `json:"session_id"`
	TranscriptPath *string `json:"transcript_path,omitempty"`
	CWD            string  `json:"cwd,omitempty"`
	PermissionMode string  `json:"permission_mode,omitempty"`
	HookEventName  string  `json:"hook_event_name"`

	// Event-specific fields (only present on the relevant event)
	Source             string `json:"source,omitempty"`
	Model              string `json:"model,omitempty"`
	Prompt             string `json:"prompt,omitempty"`
	Error              string `json:"error,omitempty"`
	Trigger            string `json:"trigger,omitempty"`
	Reason             string `json:"reason,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`
}

// ClaudeStatusLineInput is the JSON Claude Code sends to the status line command on stdin.
// Source: https://code.claude.com/docs/en/statusline
type ClaudeStatusLineInput struct {
	Model          ModelStatus         `json:"model"`
	SessionID      string              `json:"session_id"`
	TranscriptPath string              `json:"transcript_path"`
	ContextWindow  ContextWindowStatus `json:"context_window"`
	Cost           CostStatus          `json:"cost,omitempty"`
	RateLimits     RateLimitStatus     `json:"rate_limits,omitempty"`
	Workspace      WorkspaceStatus     `json:"workspace,omitempty"`
}

// ModelStatus from Claude status line.
type ModelStatus struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

// ContextWindowStatus from Claude status line.
// CurrentUsage may be null before the first response and immediately after compaction.
// TotalInputTokens and TotalOutputTokens are pointers so that an explicit zero
// (reported by Claude before the first response) is preserved and never collapsed
// to nil. Null means "not reported"; zero means "explicitly zero".
type ContextWindowStatus struct {
	TotalInputTokens  *int64        `json:"total_input_tokens"`
	TotalOutputTokens *int64        `json:"total_output_tokens"`
	CurrentUsage      *CurrentUsage `json:"current_usage"`
	ContextWindowSize int64         `json:"context_window_size"`
	UsedPercentage    *float64      `json:"used_percentage"`
}

// CurrentUsage breaks down the latest API response token usage.
// This is null when Claude has not yet produced a response in the current turn.
// All fields are pointers so explicit zero values are preserved (null ≠ zero).
type CurrentUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
}

// CostStatus from Claude status line.
type CostStatus struct {
	TotalCostUSD    float64 `json:"total_cost_usd,omitempty"`
	TotalDurationMs int64   `json:"total_duration_ms,omitempty"`
}

// RateLimitStatus from Claude status line (Claude.ai subscribers only).
type RateLimitStatus struct {
	FiveHour RateLimitBucket `json:"five_hour,omitempty"`
	SevenDay RateLimitBucket `json:"seven_day,omitempty"`
}

// RateLimitBucket describes a rate limit time window.
type RateLimitBucket struct {
	UsedPercentage float64 `json:"used_percentage,omitempty"`
	ResetsAt       string  `json:"resets_at,omitempty"`
}

// WorkspaceStatus from Claude status line.
type WorkspaceStatus struct {
	CurrentDir string `json:"current_dir,omitempty"`
	ProjectDir string `json:"project_dir,omitempty"`
}

// ============================================================
// Codex hook input (flat schema)
// Source: https://developers.openai.com/codex/hooks
// ============================================================

// CodexHookInput is the flat JSON object Codex sends to hook scripts on stdin.
// Codex hooks expose session, model, prompt, and lifecycle information.
// Codex does NOT provide live token/context snapshots.
type CodexHookInput struct {
	// Common fields
	SessionID      string  `json:"session_id"`
	TranscriptPath *string `json:"transcript_path,omitempty"`
	CWD            string  `json:"cwd,omitempty"`
	HookEventName  string  `json:"hook_event_name"`

	// Event-specific fields
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	Source         string `json:"source,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	Trigger        string `json:"trigger,omitempty"`
	Reason         string `json:"reason,omitempty"`
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
//   - Codex does not expose any live token/context snapshot
//
// Field rules:
//   - null = not exposed / not collected yet
//   - zero = explicitly reported as zero (legitimate value)
//   - Never convert null to zero
//   - Never accumulate status line refreshes (same response may be read multiple times)
