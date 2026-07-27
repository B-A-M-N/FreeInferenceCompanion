package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/bamn/freeinference-companion/internal/engine"
	"github.com/bamn/freeinference-companion/internal/state"
	"github.com/bamn/freeinference-companion/pkg/schema"
)

// CodexAdapter handles Codex-specific integration logic.
type CodexAdapter struct {
	Paths state.Paths
}

// NewCodexAdapter creates a new CodexAdapter.
func NewCodexAdapter(paths state.Paths) *CodexAdapter {
	return &CodexAdapter{Paths: paths}
}

// ParseHookEvent reads and parses a Codex hook event from stdin.
func (a *CodexAdapter) ParseHookEvent(r io.Reader) (*schema.CodexHookEvent, error) {
	var event schema.CodexHookEvent
	if err := json.NewDecoder(r).Decode(&event); err != nil {
		return nil, fmt.Errorf("parse codex hook event: %w", err)
	}
	return &event, nil
}

// HandleSessionStart initializes a new Codex session.
func (a *CodexAdapter) HandleSessionStart(event *schema.CodexHookEvent) error {
	sessionID := event.Payload.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}

	now := time.Now().UTC()
	modelID := "unknown"
	if event.Payload.Model != nil {
		modelID = *event.Payload.Model
	}
	ctxLen := int64(200000)
	if event.Payload.ContextLength != nil {
		ctxLen = *event.Payload.ContextLength
	}

	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		PluginVersion: "0.1.0",
		Client: schema.ClientInfo{
			Type: schema.ClientCodex,
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
			ContextLength:   int(ctxLen),
			MaxOutputTokens: int(ctxLen) / 2,
			MetadataSource:  "client_hook",
			AccessState:     schema.AccessUnknown,
		},
		Pressure: schema.PressureState{
			State:                schema.PressureUnknown,
			PreviousState:        schema.PressureUnknown,
			ProjectionConfidence: engine.ProjectionConfidenceLow,
			ChangedAt:            now,
		},
		Activity: schema.ActivityState{
			Confidence: schema.ConfidenceClientLifecycle,
		},
	}

	return state.SaveSnapshot(a.Paths, schema.ClientCodex, sessionID, snap)
}

// HandleUserPromptSubmit processes a prompt submission and generates Codex warnings.
// Codex output format: {"continue":true,"systemMessage":"..."} (no suppressOutput)
func (a *CodexAdapter) HandleUserPromptSubmit(event *schema.CodexHookEvent, sessionID string) (out *schema.CodexWarningOutput, err error) {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientCodex, sessionID)
	if err != nil || snap == nil {
		return &schema.CodexWarningOutput{Continue: true}, nil
	}

	now := time.Now().UTC()

	// Estimate prompt length
	var estimatedPrompt int64
	if event.Payload.Prompt != nil {
		estimatedPrompt = int64(len(*event.Payload.Prompt) / 4)
	}

	// Compute projected context
	currentTokens := int64(0)
	if snap.LiveContext != nil && snap.LiveContext.FreshInputTokens != nil {
		currentTokens = *snap.LiveContext.FreshInputTokens
	}

	proj := engine.EstimateProjectedContext(
		currentTokens,
		int64(snap.Model.ContextLength),
		estimatedPrompt,
		engine.DefaultOutputReserve,
	)

	// Build warnings (same logic, different output format)
	var warnings []string
	if proj.ProjectedPercent > 95 && proj.Confidence != engine.ProjectionConfidenceLow {
		warnings = append(warnings, engine.BuildProjectedOverflowWarning(proj))
	}
	if snap.Pressure.State == schema.PressureCritical || snap.Pressure.State == schema.PressureWarn {
		usedPct := 0.0
		if snap.LiveContext != nil && snap.LiveContext.UsedPercentage != nil {
			usedPct = *snap.LiveContext.UsedPercentage
		}
		msg := engine.BuildContextWarning(usedPct, proj.ProjectedPercent, snap.Model.ID, int64(snap.Model.ContextLength))
		warnings = append(warnings, msg)
	}

	if len(warnings) > 0 {
		out = &schema.CodexWarningOutput{
			Continue:      true,
			SystemMessage: warnings[0],
		}
	} else {
		out = &schema.CodexWarningOutput{Continue: true}
	}

	// Store projection
	pct := proj.ProjectedPercent
	snap.Pressure.ProjectedPercentage = &pct
	snap.Pressure.ProjectionConfidence = proj.Confidence
	snap.Session.LastEventAt = now
	state.SaveSnapshot(a.Paths, schema.ClientCodex, sessionID, snap)
	return out, nil
}

// HandleSessionEnd marks a Codex session as completed.
func (a *CodexAdapter) HandleSessionEnd(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientCodex, sessionID)
	if err != nil || snap == nil {
		return nil // fail open
	}
	now := time.Now().UTC()
	snap.Session.Status = schema.SessionCompleted
	snap.Session.LastEventAt = now
	snap.Activity.TurnActive = false
	return state.SaveSnapshot(a.Paths, schema.ClientCodex, sessionID, snap)
}

// HandleStop marks a Codex session as stopped.
func (a *CodexAdapter) HandleStop(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientCodex, sessionID)
	if err != nil || snap == nil {
		return nil // fail open
	}
	now := time.Now().UTC()
	snap.Session.Status = schema.SessionStopped
	snap.Session.LastEventAt = now
	snap.Activity.TurnActive = false
	return state.SaveSnapshot(a.Paths, schema.ClientCodex, sessionID, snap)
}

// HandlePreCompact records pre-compaction state.
func (a *CodexAdapter) HandlePreCompact(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientCodex, sessionID)
	if err != nil || snap == nil {
		return nil
	}
	snap.Compaction.Pending = true
	snap.Session.LastEventAt = time.Now().UTC()
	return state.SaveSnapshot(a.Paths, schema.ClientCodex, sessionID, snap)
}

// HandlePostCompact records post-compaction metrics.
func (a *CodexAdapter) HandlePostCompact(sessionID string) error {
	snap, err := state.LoadSnapshot(a.Paths, schema.ClientCodex, sessionID)
	if err != nil || snap == nil {
		return nil
	}
	now := time.Now().UTC()
	if snap.Compaction.Pending && snap.LiveContext != nil && snap.LiveContext.FreshInputTokens != nil {
		postTokens := *snap.LiveContext.FreshInputTokens
		reductionPct := engine.ComputeCompactionReduction(0, postTokens)
		snap.Compaction.LastResult = &schema.CompactionResult{
			At:           now,
			PostTokens:   &postTokens,
			ReductionPct: &reductionPct,
		}
	}
	snap.Compaction.Pending = false
	snap.Session.LastEventAt = now
	return state.SaveSnapshot(a.Paths, schema.ClientCodex, sessionID, snap)
}

// MarshalWarningJSON serializes a CodexWarningOutput to JSON.
func MarshalCodexWarning(w *schema.CodexWarningOutput) ([]byte, error) {
	return json.Marshal(w)
}