package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
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
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&input); err != nil {
		return nil, fmt.Errorf("parse codex hook event: %w", err)
	}
	return &input, nil
}

// newCodexSnapshot builds a fresh snapshot for a newly seen Codex session.
func newCodexSnapshot(sessionID, modelID, source string, now time.Time) *schema.Snapshot {
	modelID = secure.SanitizeField(modelID)
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
			ID:                sessionID,
			StartedAt:         now,
			LastEventAt:       now,
			Status:            schema.SessionActive,
			StartSource:       source,
			ConversationEpoch: 1,
		},
		// Provider identity is supplied by the activation-aware caller. Keep a
		// new snapshot unresolved until that evidence is threaded through.
		Provider: schema.ProviderInfo{Name: schema.ProviderUnknown, Source: "unresolved"},
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
//
// DEPRECATED: use HandleSessionStartWith, which accepts a runtime.Activation.
func (a *CodexAdapter) HandleSessionStart(input *schema.CodexHookInput) error {
	return a.HandleSessionStartWith(input, runtime.EvaluateForClient(runtime.ClientCodex))
}

// HandleSessionStartWith is the activation-aware variant. The caller must
// have already gated on activation.Active.
func (a *CodexAdapter) HandleSessionStartWith(input *schema.CodexHookInput, activation runtime.Activation) error {
	return a.HandleSessionStartWithTrace(input, activation, nil)
}

// HandleSessionStartWithTrace associates validated launch metadata with the
// session without recording a trace event or raw header values.
func (a *CodexAdapter) HandleSessionStartWithTrace(input *schema.CodexHookInput, activation runtime.Activation, trace *schema.TraceInfo) error {
	if input == nil {
		return nil
	}
	sessionID := input.SessionID
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	provider := activation.ProviderInfo()
	modelID := secure.SanitizeField(input.Model)
	source := normalizeCodexStartSource(input.Source)
	created := false
	wasActive := false
	modelChanged := false
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID,
		func() *schema.Snapshot {
			created = true
			return newCodexSnapshot(sessionID, modelID, source, now)
		},
		func(snap *schema.Snapshot) error {
			wasActive = snap.Session.Status == schema.SessionActive
			snap.Session.Status = schema.SessionActive
			snap.Session.LastEventAt = now
			snap.Session.EndedAt = nil
			snap.Session.StartSource = source
			if snap.Session.ConversationEpoch == 0 {
				snap.Session.ConversationEpoch = 1
			}
			if source == "clear" && !created {
				snap.Session.ConversationEpoch++
			}
			snap.Provider = provider
			snap.ActivationID = a.Paths.ActivationID
			if trace != nil && trace.Verified {
				copy := *trace
				snap.Trace = &copy
			}
			modelChanged = observeCodexModel(snap, modelID, "SessionStart", now)
			return nil
		})
	if err == nil {
		if modelChanged {
			appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventModelSwitch, Model: modelID, Provider: provider.Name, Detail: "source=codex:SessionStart"})
		}
		if created || source == "clear" || (source == "startup" && !wasActive) {
			appendCodexEvent(a.Paths, sessionID,
				state.Event{Type: state.EventSessionStarted, Model: modelID, Provider: provider.Name, Detail: "source=" + source})
		}
	}
	return err
}

// HandleUserPromptSubmit activates the turn for a Codex session.
// Codex hooks expose no live token/context snapshot, so no context or cache
// warnings are ever generated — returns (nil, nil), producing no stdout.
func (a *CodexAdapter) HandleUserPromptSubmit(input *schema.CodexHookInput, sessionID string) (*schema.CodexWarningOutput, error) {
	return a.HandleUserPromptSubmitWith(input, sessionID, runtime.EvaluateForClient(runtime.ClientCodex))
}

// HandleUserPromptSubmitWith is the activation-aware variant. Codex still
// emits no warning output because it has no cache/context telemetry, but the
// snapshot retains the selected provider identity when a supported embedding
// supplies it.
func (a *CodexAdapter) HandleUserPromptSubmitWith(input *schema.CodexHookInput, sessionID string, activation runtime.Activation) (*schema.CodexWarningOutput, error) {
	if sessionID == "" || input == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	duplicate := false
	modelChanged := false
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID,
		func() *schema.Snapshot {
			return newCodexSnapshot(sessionID, "", "startup", now)
		},
		func(snap *schema.Snapshot) error {
			modelChanged = observeCodexModel(snap, codexInputModel(input), "UserPromptSubmit", now)
			turnID := sanitizeCodexTurnID(input.TurnID)
			duplicate = turnID != "" && (snap.Activity.TurnID == turnID || snap.Activity.LastTurnID == turnID)
			if !duplicate {
				active := true
				snap.Activity.TurnActive = &active
				snap.Activity.TurnStartedAt = &now
				snap.Activity.TurnEndedAt = nil
				snap.Activity.TurnID = turnID
				snap.Activity.LastTurnID = turnID
			}
			snap.Session.Status = schema.SessionActive
			snap.Session.LastEventAt = now
			snap.ActivationID = a.Paths.ActivationID
			snap.Provider = activation.ProviderInfo()
			return nil
		})
	if err != nil {
		return nil, nil
	}
	if modelChanged {
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventModelSwitch, Model: codexInputModel(input), Provider: activation.ProviderInfo().Name, Detail: "source=codex:UserPromptSubmit"})
	}
	if !duplicate {
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventPromptSubmitted, Model: codexInputModel(input), Provider: activation.ProviderInfo().Name, Detail: codexTurnDetail(input)})
	}
	return nil, nil
}

// HandleSessionEnd marks a Codex session as completed.
func (a *CodexAdapter) HandleSessionEnd(sessionID string, inputs ...*schema.CodexHookInput) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	modelChanged := false
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			if input := firstCodexInput(inputs); input != nil {
				modelChanged = observeCodexModel(snap, codexInputModel(input), "SessionEnd", now)
			}
			inactive := false
			snap.Session.Status = schema.SessionCompleted
			snap.Session.EndedAt = &now
			snap.Session.LastEventAt = now
			snap.Activity.TurnActive = &inactive
			snap.Activity.TurnID = ""
			return nil
		})
	if err == nil {
		input := firstCodexInput(inputs)
		if modelChanged {
			appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventModelSwitch, Model: codexInputModel(input), Detail: "source=codex:SessionEnd"})
		}
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventSessionEnded, Model: codexInputModel(input), Detail: codexTurnDetail(input)})
	}
	return err
}

// HandleStop marks the turn as inactive without ending the session. The
// optional input enables bounded turn-ID deduplication while preserving the
// original one-argument API used by older callers.
func (a *CodexAdapter) HandleStop(sessionID string, inputs ...*schema.CodexHookInput) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	stopped := false
	modelChanged := false
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			input := firstCodexInput(inputs)
			if input != nil {
				modelChanged = observeCodexModel(snap, codexInputModel(input), "Stop", now)
				turnID := sanitizeCodexTurnID(input.TurnID)
				if turnID != "" && snap.Activity.TurnID != "" && turnID != snap.Activity.TurnID {
					return nil
				}
				if turnID != "" && snap.Activity.TurnID == "" && snap.Activity.LastTurnID == turnID {
					return nil
				}
			}
			inactive := false
			snap.Activity.TurnActive = &inactive
			snap.Activity.TurnEndedAt = &now
			snap.Activity.LastTurnID = snap.Activity.TurnID
			snap.Activity.TurnID = ""
			snap.Session.LastEventAt = now
			stopped = true
			return nil
		})
	if err == nil {
		input := firstCodexInput(inputs)
		if modelChanged {
			appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventModelSwitch, Model: codexInputModel(input), Detail: "source=codex:Stop"})
		}
		if stopped {
			appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventTurnStopped, Model: codexInputModel(input), Detail: codexTurnDetail(input)})
		}
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
	modelChanged := false
	err := state.UpdateSnapshot(a.Paths, schema.ClientCodex, sessionID, nil,
		func(snap *schema.Snapshot) error {
			if input != nil {
				modelChanged = observeCodexModel(snap, codexInputModel(input), "PreCompact", now)
			}
			snap.Compaction.Pending = true
			snap.Compaction.InitiatedAt = &now
			if input != nil && input.Trigger != "" {
				trigger := secure.SanitizeField(input.Trigger)
				snap.Compaction.Trigger = &trigger
			}
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		if modelChanged {
			appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventModelSwitch, Model: codexInputModel(input), Detail: "source=codex:PreCompact"})
		}
		trigger := ""
		if input != nil {
			trigger = input.Trigger
		}
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventCompactionStarted, Model: codexInputModel(input), Detail: codexDetail(trigger, input)})
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
			if input != nil {
				observeCodexModel(snap, codexInputModel(input), "PostCompact", now)
			}
			trigger := ""
			if snap.Compaction.Trigger != nil {
				trigger = *snap.Compaction.Trigger
			}
			if input != nil && input.Trigger != "" {
				trigger = secure.SanitizeField(input.Trigger)
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
		appendCodexEvent(a.Paths, sessionID, state.Event{Type: state.EventCompactionCompleted, Model: codexInputModel(input), Detail: codexTurnDetail(input)})
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

func normalizeCodexStartSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case "startup", "resume", "compact", "clear":
		return source
	default:
		return "unknown"
	}
}

func observeCodexModel(snap *schema.Snapshot, rawModel, _ string, now time.Time) bool {
	model := secure.SanitizeField(rawModel)
	if snap == nil || model == "" {
		return false
	}
	previous := snap.Model.ID
	snap.Model.ID = model
	snap.Model.MetadataSource = "client_hook"
	if previous == "" || previous == "unknown" || previous == model {
		return false
	}
	beginCacheEpoch(snap, "model_switch", now)
	return true
}

func codexInputModel(input *schema.CodexHookInput) string {
	if input == nil {
		return ""
	}
	return secure.SanitizeField(input.Model)
}

func firstCodexInput(inputs []*schema.CodexHookInput) *schema.CodexHookInput {
	if len(inputs) == 0 {
		return nil
	}
	return inputs[0]
}

func sanitizeCodexTurnID(turnID string) string {
	turnID = secure.Redact(secure.SanitizeField(turnID))
	if len(turnID) > 128 {
		return turnID[:128]
	}
	return turnID
}

func codexTurnDetail(input *schema.CodexHookInput) string {
	if input == nil {
		return ""
	}
	turnID := sanitizeCodexTurnID(input.TurnID)
	if turnID == "" {
		return ""
	}
	return "turn_id=" + turnID
}

func codexDetail(trigger string, input *schema.CodexHookInput) string {
	trigger = secure.SanitizeField(trigger)
	if turn := codexTurnDetail(input); turn != "" {
		if trigger != "" {
			return trigger + " " + turn
		}
		return turn
	}
	return trigger
}
