package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bamn/freeinference-companion/internal/engine"
	"github.com/bamn/freeinference-companion/internal/state"
	"github.com/bamn/freeinference-companion/pkg/schema"
)

// ClaudeAdapter handles Claude Code-specific integration logic.
type ClaudeAdapter struct {
	Paths state.Paths
}

// NewClaudeAdapter creates a new ClaudeAdapter.
func NewClaudeAdapter(paths state.Paths) *ClaudeAdapter {
	return &ClaudeAdapter{Paths: paths}
}

// ParseStatusLineInput reads and parses Claude status line JSON from stdin.
func (a *ClaudeAdapter) ParseStatusLineInput(r io.Reader) (*schema.ClaudeStatusLineInput, error) {
	var input schema.ClaudeStatusLineInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return nil, fmt.Errorf("parse status line: %w", err)
	}
	return &input, nil
}

// ParseHookEvent reads and parses a Claude hook event from stdin.
func (a *ClaudeAdapter) ParseHookEvent(r io.Reader) (*schema.ClaudeHookEvent, error) {
	var event schema.ClaudeHookEvent
	if err := json.NewDecoder(r).Decode(&event); err != nil {
		return nil, fmt.Errorf("parse hook event: %w", err)
	}
	return &event, nil
}

// HandleSessionStart initializes a new session state.
func (a *ClaudeAdapter) HandleSessionStart(event *schema.ClaudeHookEvent) error {
	sessionID := event.Payload.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}

	now := time.Now().UTC()
	modelID := "unknown"
	if event.Payload.Model != nil {
		modelID = *event.Payload.Model
	}
	ctxLen := 200000
	if event.Payload.ContextLength != nil {
		ctxLen = *event.Payload.ContextLength
	}

	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		PluginVersion: "0.1.0",
		Client: schema.ClientInfo{
			Type: schema.ClientClaudeCode,
		},
		Session: schema.SessionInfo{
			ID:          sessionID,
			StartedAt:   now,
			LastEventAt: now,
			Status:      schema.SessionActive,
		},
		Model: schema.ModelInfo{
			ID:              modelID,
			Provider:        "freeinference",
			ContextLength:   ctxLen,
			MaxOutputTokens: ctxLen / 2, // conservative default
			MetadataSource:  "client_statusline",
			AccessState:     schema.AccessUnknown,
		},
		Pressure: schema.PressureState{
			State:              schema.PressureUnknown,
			PreviousState:      schema.PressureUnknown,
			ProjectionConfidence: engine.ProjectionConfidenceLow,
			ChangedAt:          now,
		},
		Activity: schema.ActivityState{
			Confidence: schema.ConfidenceClientLifecycle,
		},
		Compaction: schema.CompactionState{},
	}

	return state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
}

// HandleStatusLineUpdate processes a status line input and updates session state.
// This is the primary mechanism for updating live context data.
// IMPORTANT: Does NOT accumulate values — status line may be refreshed multiple times per response.
func (a *ClaudeAdapter) HandleStatusLineUpdate(input *schema.ClaudeStatusLineInput, sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		return fmt.Errorf("no session to update")
	}

	now := time.Now().UTC()

	// Update model info from status line
	snap.Model.ID = input.Model.ID
	if input.ContextWindow.ContextWindowSize > 0 {
		snap.Model.ContextLength = int(input.ContextWindow.ContextWindowSize)
	}

	// Update live context snapshot (NOT cumulative — single observation)
	freshInput := input.ContextWindow.CurrentUsage.InputTokens
	cacheRead := input.ContextWindow.CurrentUsage.CacheReadInputTokens
	cacheCreate := input.ContextWindow.CurrentUsage.CacheCreationInputTokens
	output := input.ContextWindow.CurrentUsage.OutputTokens
	usedPct := input.ContextWindow.UsedPercentage
	ctxSize := input.ContextWindow.ContextWindowSize

	snap.LiveContext = &schema.LiveContext{
		Source:                   "claude_statusline",
		ObservedAt:               now,
		FreshInputTokens:         &freshInput,
		CacheCreationInputTokens: &cacheCreate,
		CacheReadInputTokens:     &cacheRead,
		OutputTokens:             &output,
		ContextWindowSize:        &ctxSize,
		UsedPercentage:           &usedPct,
	}

	// Compute pressure state
	newState, reason := engine.ClassifyPressure(usedPct, snap.Pressure.State)
	snap.Pressure.PreviousState = snap.Pressure.State
	snap.Pressure.State = newState
	snap.Pressure.Reason = reason
	snap.Pressure.ChangedAt = now

	// Update session timestamp
	snap.Session.LastEventAt = now

	return state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
}

// HandleUserPromptSubmit processes a prompt submission and produces warnings.
func (a *ClaudeAdapter) HandleUserPromptSubmit(event *schema.ClaudeHookEvent, sessionID string) (out *schema.ClaudeWarningOutput, err error) {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		// Fail open — no state, no warning
		return &schema.ClaudeWarningOutput{Continue: true}, nil
	}

	now := time.Now().UTC()

	// Estimate prompt length from the prompt text
	var estimatedPrompt int64
	if event.Payload.Prompt != nil {
		estimatedPrompt = estimateTokenCount(*event.Payload.Prompt)
	}

	// Compute projected context
	currentTokens := int64(0)
	if snap.LiveContext != nil && snap.LiveContext.FreshInputTokens != nil {
		currentTokens = *snap.LiveContext.FreshInputTokens
	}
	if snap.LiveContext != nil && snap.LiveContext.CacheReadInputTokens != nil {
		currentTokens += *snap.LiveContext.CacheReadInputTokens
	}

	proj := engine.EstimateProjectedContext(
		currentTokens,
		int64(snap.Model.ContextLength),
		estimatedPrompt,
		engine.DefaultOutputReserve,
	)

	// Store projection in pressure state
	pct := proj.ProjectedPercent
	snap.Pressure.ProjectedPercentage = &pct
	snap.Pressure.ProjectionConfidence = proj.Confidence

	// Check pressure and build warnings
	warnings := a.buildWarnings(snap, proj, now)
	if len(warnings) > 0 {
		msg := warnings[0] // show the most important warning
		out = &schema.ClaudeWarningOutput{
			Continue:       true,
			SystemMessage:  msg,
			SuppressOutput: true,
		}
	} else {
		out = &schema.ClaudeWarningOutput{Continue: true}
	}

	snap.Session.LastEventAt = now
	state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
	return out, nil
}

// HandlePreCompact records pre-compaction state.
func (a *ClaudeAdapter) HandlePreCompact(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		return nil // fail open
	}

	snap.Compaction.Pending = true
	now := time.Now().UTC()
	snap.Session.LastEventAt = now
	return state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
}

// HandlePostCompact records post-compaction metrics.
func (a *ClaudeAdapter) HandlePostCompact(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		return nil // fail open
	}

	now := time.Now().UTC()

	// If we have a pending compact and live context, calculate reduction
	if snap.Compaction.Pending && snap.LiveContext != nil && snap.LiveContext.FreshInputTokens != nil {
		preTokens := int64(0)
		postTokens := *snap.LiveContext.FreshInputTokens

		reductionPct := engine.ComputeCompactionReduction(preTokens, postTokens)
		snap.Compaction.LastResult = &schema.CompactionResult{
			At:           now,
			PreTokens:    &preTokens,
			PostTokens:   &postTokens,
			ReductionPct: &reductionPct,
		}
	}

	snap.Compaction.Pending = false
	snap.Session.LastEventAt = now
	return state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
}

// HandleStopFailure records a structured failure from StopFailure hook.
func (a *ClaudeAdapter) HandleStopFailure(event *schema.ClaudeHookEvent, sessionID string) error {
	if event.Payload.ErrorCategory == nil {
		return nil
	}

	snap, err := state.LoadSnapshot(a.Paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		return nil // fail open
	}

	now := time.Now().UTC()
	snap.LastFailure = &schema.FailureRecord{
		Category:   *event.Payload.ErrorCategory,
		ObservedAt: now,
		Source:     "claude_stop_failure",
	}
	snap.Session.LastEventAt = now
	return state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
}

// HandleSessionEnd marks a session as completed.
func (a *ClaudeAdapter) HandleSessionEnd(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		return nil // fail open
	}

	now := time.Now().UTC()
	snap.Session.Status = schema.SessionCompleted
	snap.Session.LastEventAt = now
	return state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
}

// HandleStop marks a session as stopped.
func (a *ClaudeAdapter) HandleStop(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		return nil // fail open
	}

	now := time.Now().UTC()
	snap.Session.Status = schema.SessionStopped
	snap.Session.LastEventAt = now
	snap.Activity.TurnActive = false
	return state.SaveSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, snap)
}

// buildWarnings constructs warning messages based on current state.
func (a *ClaudeAdapter) buildWarnings(snap *schema.Snapshot, proj engine.ProjectedContext, now time.Time) []string {
	var warnings []string

	// Check for projected overflow
	if proj.ProjectedPercent > 95 && proj.Confidence != engine.ProjectionConfidenceLow {
		warnings = append(warnings, engine.BuildProjectedOverflowWarning(proj))
	}

	// Check context pressure
	if snap.Pressure.State == schema.PressureCritical || snap.Pressure.State == schema.PressureWarn {
		usedPct := 0.0
		if snap.LiveContext != nil && snap.LiveContext.UsedPercentage != nil {
			usedPct = *snap.LiveContext.UsedPercentage
		}
		msg := engine.BuildContextWarning(usedPct, proj.ProjectedPercent, snap.Model.ID, int64(snap.Model.ContextLength))
		warnings = append(warnings, msg)
	}

	// Check cache health (if we have data)
	if snap.CacheAnalysis != nil {
		window := &engine.CacheWindowResult{
			RequestSamples:     snap.CacheAnalysis.RequestSamples,
			TotalObservedInput: 0,
			CacheReadShare:     readShareOrDefault(snap.CacheAnalysis.CacheReadShare),
			Trend:              snap.CacheAnalysis.Trend,
		}
		if engine.ShouldWarnCache(window) && !engine.ShouldResolveCacheWarning(window) {
			msg := engine.BuildCacheWarning(window.CacheReadShare, window.RequestSamples)
			warnings = append(warnings, msg)
		}
	}

	return warnings
}

// ============================================================
// Utility functions
// ============================================================

// estimateTokenCount provides a rough token estimate for a text string.
// Uses a simple heuristic: ~4 characters per token for English text.
// This is explicitly an approximation — labeled as such in all output.
func estimateTokenCount(text string) int64 {
	return int64(len(text) / 4)
}

// readShareOrDefault returns the cache read share or a default if nil.
func readShareOrDefault(share *float64) float64 {
	if share == nil {
		return 0
	}
	return *share
}

// FormatStatusLineCompact renders a single-line compact status for the Claude status bar.
func FormatStatusLineCompact(snap *schema.Snapshot, health *schema.HealthCache) string {
	if snap == nil {
		return "FI: no data"
	}

	model := snap.Model.ID
	if model == "" {
		model = "?"
	}

	// Health indicator
	healthChar := "●"
	if health != nil && health.UnhealthyCount != nil && *health.UnhealthyCount > 0 {
		healthChar = "◐"
	}
	if snap.LastFailure != nil {
		healthChar = "✗"
	}

	// Context percentage
	ctxStr := "ctx ?"
	if snap.LiveContext != nil && snap.LiveContext.UsedPercentage != nil {
		ctxStr = fmt.Sprintf("ctx %.0f%%", *snap.LiveContext.UsedPercentage)
	}

	// Cache percentage (read share)
	cacheStr := ""
	if snap.CacheAnalysis != nil && snap.CacheAnalysis.CacheReadShare != nil {
		pct := int(*snap.CacheAnalysis.CacheReadShare * 100)
		cacheStr = fmt.Sprintf(" cache %d%%", pct)
	}

	// Pressure indicator
	pressureStr := ""
	switch snap.Pressure.State {
	case schema.PressureWatch:
		pressureStr = " WATCH"
	case schema.PressureWarn:
		pressureStr = " WARN"
	case schema.PressureCritical:
		pressureStr = " CRIT"
	}

	return fmt.Sprintf("FI %s %s | %s%s%s", model, healthChar, ctxStr, cacheStr, pressureStr)
}

// init ensures the module can compile standalone
var _ = os.Stdin