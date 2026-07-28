package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// CodexAdapter handles Codex-specific integration logic.
type CodexAdapter struct {
	Paths state.Paths
}

// NewCodexAdapter creates a new CodexAdapter.
func NewCodexAdapter(paths state.Paths) *CodexAdapter {
	return &CodexAdapter{Paths: paths}
}

// ParseHookInput reads and parses a flat Codex hook event from stdin.
func (a *CodexAdapter) ParseHookInput(r io.Reader) (*schema.CodexHookInput, error) {
	var input schema.CodexHookInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return nil, fmt.Errorf("parse codex hook event: %w", err)
	}
	return &input, nil
}

// newCodexSnapshot builds a fresh snapshot for a newly seen Codex session.
func newCodexSnapshot(sessionID, modelID string, now time.Time) *schema.Snapshot {
	if modelID == "" {
		modelID = "unknown"
	}
	return &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		PluginVersion: PluginVersion,
		Client: schema.ClientInfo{
			Type: schema.ClientCodex,
		},
		Session: schema.SessionInfo{
			ID:          sessionID,
			StartedAt:   now,
			LastEventAt: now,
			Status:      schema.SessionActive,
		},
		Provider: DetectProvider().ToProviderInfo(),
		Model: schema.ModelInfo{
			ID:             modelID,
			MetadataSource: "client_hook",
			AccessState:    schema.AccessUnknown,
		},
		Pressure: schema.PressureState{
			State:     schema.PressureUnknown,
			ChangedAt: now,
		},
		Activity: schema.ActivityState{
			Confidence: schema.ConfidenceClientLifecycle,
		},
	}
}

// HandleSessionStart initializes a new Codex session.
// Codex does not provide context window size from hooks — it stays null.
func (a *CodexAdapter) HandleSessionStart(input *schema.CodexHookInput) error {
	sessionID := input.SessionID
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	provider := DetectProvider()
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID,
		func() *schema.Snapshot {
			return newCodexSnapshot(sessionID, input.Model, now)
		},
		func(snap *schema.Snapshot) error {
			snap.Session.Status = schema.SessionActive
			snap.Session.LastEventAt = now
			snap.Session.EndedAt = nil
			snap.Provider = provider.ToProviderInfo()
			if input.Model != "" && (snap.Model.ID == "" || snap.Model.ID == "unknown") {
				snap.Model.ID = input.Model
				snap.Model.MetadataSource = "client_hook"
			}
			return nil
		})
	if err == nil {
		appendCodexEvent(a.Paths, sessionID,
			state.Event{Type: state.EventSessionStarted, Model: input.Model, Provider: provider.Name})
	}
	return err
}

// HandleUserPromptSubmit activates the turn for a Codex session.
// Codex hooks expose no live token/context snapshot, so no context or cache
// warnings are ever generated — returns (nil, nil), producing no stdout.
func (a *CodexAdapter) HandleUserPromptSubmit(input *schema.CodexHookInput, sessionID string) (*schema.CodexWarningOutput, error) {
	if sessionID == "" {
		return nil, nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID,
		func() *schema.Snapshot {
			return newCodexSnapshot(sessionID, "", now)
		},
		func(snap *schema.Snapshot) error {
			active := true
			snap.Activity.TurnActive = &active
			snap.Activity.TurnStartedAt = &now
			snap.Session.Status = schema.SessionActive
			snap.Session.LastEventAt = now
			return nil
		})
	if err != nil {
		return nil, nil
	}
	appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventPromptSubmitted})
	return nil, nil
}

// HandleSessionEnd marks a Codex session as completed.
func (a *CodexAdapter) HandleSessionEnd(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			inactive := false
			snap.Session.Status = schema.SessionCompleted
			snap.Session.EndedAt = &now
			snap.Session.LastEventAt = now
			snap.Activity.TurnActive = &inactive
			return nil
		})
	if err == nil {
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventSessionEnded})
	}
	return err
}

// HandleStop marks the turn as inactive without ending the session.
func (a *CodexAdapter) HandleStop(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			inactive := false
			snap.Activity.TurnActive = &inactive
			snap.Activity.TurnEndedAt = &now
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventTurnStopped})
	}
	return err
}

// HandlePreCompact records that compaction started. Codex provides no token
// snapshot, so no pre-compaction token count is stored.
func (a *CodexAdapter) HandlePreCompact(input *schema.CodexHookInput, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			snap.Compaction.Pending = true
			snap.Compaction.InitiatedAt = &now
			if input != nil && input.Trigger != "" {
				trigger := input.Trigger
				snap.Compaction.Trigger = &trigger
			}
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		trigger := ""
		if input != nil {
			trigger = input.Trigger
		}
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventCompactionStarted, Detail: trigger})
	}
	return err
}

// HandlePostCompact records that compaction occurred. Codex has no token
// telemetry, so no reduction percentage is ever reported.
func (a *CodexAdapter) HandlePostCompact(input *schema.CodexHookInput, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			trigger := ""
			if snap.Compaction.Trigger != nil {
				trigger = *snap.Compaction.Trigger
			}
			if input != nil && input.Trigger != "" {
				trigger = input.Trigger
			}
			snap.Compaction.Pending = false
			snap.Compaction.AwaitingPostObservation = false
			snap.Compaction.LastResult = &schema.CompactionResult{
				At:      now,
				Trigger: trigger,
			}
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventCompactionCompleted})
	}
	return err
}

// HandleStopFailure records a structured failure from the StopFailure hook.
// The raw reason text is never persisted — only a sanitized category.
func (a *CodexAdapter) HandleStopFailure(input *schema.CodexHookInput, sessionID string) error {
	if sessionID == "" || input.Reason == "" {
		return nil
	}
	category := sanitizeCodexFailureCategory(input.Reason)
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			inactive := false
			snap.Activity.TurnActive = &inactive
			snap.Activity.TurnEndedAt = &now
			snap.LastFailure = &schema.FailureRecord{
				Category:   category,
				ObservedAt: now,
				Source:     "codex_stop_failure",
			}
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventTurnFailed, Detail: category})
	}
	return err
}

// MarshalCodexWarning serializes a CodexWarningOutput to JSON.
func MarshalCodexWarning(w *schema.CodexWarningOutput) ([]byte, error) {
	return json.Marshal(w)
}

// appendCodexEvent is the codex-side mirror of appendEventBestEffort.
// It swallows all errors so event logging never blocks the client.
func appendCodexEvent(paths state.Paths, sessionID string, ev state.Event) {
	_ = paths.EnsureSessionDir(schema.ClientCodex, sessionID)
	_ = state.AppendEvent(paths, schema.ClientCodex, sessionID, ev)
	_ = state.RotateEvents(paths, schema.ClientCodex, sessionID)
}

// sanitizeCodexFailureCategory collapses a raw failure reason into a short,
// shareable category. Same scheme as the Claude adapter so reports are
// consistent across clients.
func sanitizeCodexFailureCategory(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(raw, "rate") && strings.Contains(raw, "limit"):
		return "rate_limit"
	case strings.Contains(raw, "overload"):
		return "overloaded"
	case strings.Contains(raw, "auth") || strings.Contains(raw, "unauthor") || strings.Contains(raw, "api key"):
		return "authentication_failed"
	case strings.Contains(raw, "not found") || strings.Contains(raw, "model_not_found"):
		return "model_not_found"
	case strings.Contains(raw, "max_output") || strings.Contains(raw, "max tokens"):
		return "max_output_tokens"
	case strings.Contains(raw, "invalid"):
		return "invalid_request"
	case strings.Contains(raw, "server") || strings.Contains(raw, "503") || strings.Contains(raw, "500"):
		return "server_error"
	}
	return "unknown"
}
