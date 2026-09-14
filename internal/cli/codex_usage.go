package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/codexusage"
	"github.com/b-a-m-n/freeinference-companion/internal/engine"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// codexHomeForUsage resolves the same CODEX_HOME that the Codex client uses.
// HarvardCodex sets this to ~/.harvardcodex; ordinary Codex uses ~/.codex.
func codexHomeForUsage() (string, error) {
	if home := filepath.Clean(os.Getenv("CODEX_HOME")); home != "." && home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func latestCodexUsage() (*codexusage.Usage, error) {
	return codexUsageForSession(codexCurrentSessionID())
}

// codexCurrentSessionID prefers an explicit process session pointer. It then
// accepts a Companion-only launch-to-session binding created by hooks; the raw
// Codex session ID is never used as an instance token and rollout contents are
// never persisted.
func codexCurrentSessionID() string {
	if sessionID := strings.TrimSpace(os.Getenv("FI_SESSION_ID")); sessionID != "" {
		return sessionID
	}
	instanceID := strings.TrimSpace(os.Getenv("FI_CLIENT_INSTANCE_ID"))
	if instanceID == "" {
		return ""
	}
	paths, err := state.NewPaths()
	if err != nil {
		return ""
	}
	var binding struct {
		CodexSessionID string `json:"codex_session_id"`
	}
	bindingPath := filepath.Join(paths.CacheDir, "codex-instances", instanceID+".json")
	if err := state.ReadJSON(bindingPath, &binding); err != nil {
		return ""
	}
	return strings.TrimSpace(binding.CodexSessionID)
}

// codexUsageForSession reads rollout telemetry for the exact requested Codex
// session. Without a session binding, diagnostics retain their documented
// newest-rollout behavior.
func codexUsageForSession(sessionID string) (*codexusage.Usage, error) {
	home, err := codexHomeForUsage()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sessionID) != "" {
		return codexusage.ReadForSession(home, strings.TrimSpace(sessionID))
	}
	return codexusage.Latest(home)
}

// bindCodexClientInstance records only an opaque local process instance and
// its Codex session ID. It never records transcript paths or rollout content.
func bindCodexClientInstance(instanceID, sessionID string) error {
	instanceID, sessionID = strings.TrimSpace(instanceID), strings.TrimSpace(sessionID)
	if instanceID == "" || sessionID == "" {
		return nil
	}
	if !safeInstanceID(instanceID) || sessionID != secure.SafeIdentifier(sessionID) {
		return nil
	}
	paths, err := state.NewPaths()
	if err != nil {
		return err
	}
	dir := filepath.Join(paths.CacheDir, "codex-instances")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return state.WriteJSONAtomically(filepath.Join(dir, instanceID+".json"), map[string]string{
		"codex_session_id": sessionID,
	})
}

// latestCodexSnapshot creates the same in-memory view used by the interactive
// commands when Codex has not emitted a Companion lifecycle snapshot yet.
func latestCodexSnapshot(activation runtime.Activation) (*schema.Snapshot, error) {
	usage, err := latestCodexUsage()
	if err != nil {
		return nil, err
	}
	snap := codexSnapshotFromUsage(usage, activation)
	if snap == nil || snap.LiveContext == nil {
		return nil, codexusage.ErrNoUsage
	}
	return snap, nil
}

// applyCodexUsage attaches the latest rollout counters to a snapshot in
// memory. The rollout reader is the source of truth for Codex usage; the
// lifecycle snapshot remains the source of truth for hook/session state.
func applyCodexUsage(snap *schema.Snapshot, usage *codexusage.Usage, activation runtime.Activation) bool {
	if snap == nil || usage == nil || usage.Last.InputTokens == nil {
		return false
	}
	if snap.Session.ID != "" && usage.SessionID != "" && snap.Session.ID != usage.SessionID {
		return false
	}
	if snap.Client.Type == "" {
		snap.Client.Type = schema.ClientCodex
	}
	if usage.SessionID != "" {
		snap.Session.ID = usage.SessionID
	}
	if snap.Session.ID == "" {
		snap.Session.ID = "codex-live"
	}
	if snap.Session.StartedAt.IsZero() {
		snap.Session.StartedAt = usage.ObservedAt
	}
	snap.Session.LastEventAt = usage.ObservedAt
	snap.Session.Status = schema.SessionActive
	if activation.Active {
		snap.Provider = activation.ProviderInfo()
	}
	if model := secure.SafeIdentifier(usage.Model); model != "" {
		snap.Model.ID = model
		snap.Model.MetadataSource = "codex_rollout"
	}

	input := usage.Last.InputTokens
	output := usage.Last.OutputTokens
	window := usage.ContextWindow
	var usedPct, remainingPct *float64
	if input != nil && window > 0 {
		used := float64(*input) / float64(window) * 100
		if used < 0 {
			used = 0
		}
		if used > 100 {
			used = 100
		}
		remaining := 100 - used
		usedPct, remainingPct = &used, &remaining
	}
	fresh := usage.FreshInputTokens()
	snap.LiveContext = &schema.LiveContext{
		Source:              "codex_rollout",
		ObservedAt:          usage.ObservedAt,
		TotalTokenSemantics: schema.TokenSemanticsCurrentContext,
		TotalInputTokens:    input,
		TotalOutputTokens:   output,
		ContextWindowSize:   int64PtrIfPositive(window),
		UsedPercentage:      usedPct,
		RemainingPercentage: remainingPct,
		LatestRequest: &schema.RequestUsage{
			FreshInputTokens:         fresh,
			CacheCreationInputTokens: usage.Last.CacheWriteInputTokens,
			CacheReadInputTokens:     usage.Last.CachedInputTokens,
			OutputTokens:             output,
		},
	}

	if usedPct != nil {
		state, reason := engine.ClassifyPressure(*usedPct, snap.Pressure.State)
		snap.Pressure.PreviousState = snap.Pressure.State
		snap.Pressure.State = state
		snap.Pressure.Reason = reason
		snap.Pressure.ChangedAt = usage.ObservedAt
	}

	snap.UsageObservations = nil
	identity := engine.BuildObservationIdentity(
		snap.Model.ID,
		"",
		derefCodexCounter(input),
		derefCodexCounter(output),
		fresh,
		usage.Last.CachedInputTokens,
		usage.Last.CacheWriteInputTokens,
		output,
	)
	snap.UsageObservations = append(snap.UsageObservations, schema.UsageObservation{
		Fingerprint:              identity.Fingerprint,
		FingerprintSource:        identity.Source,
		ObservedAt:               usage.ObservedAt,
		ModelID:                  snap.Model.ID,
		TotalInputTokens:         input,
		TotalOutputTokens:        output,
		FreshInputTokens:         fresh,
		CacheReadInputTokens:     usage.Last.CachedInputTokens,
		CacheCreationInputTokens: usage.Last.CacheWriteInputTokens,
		OutputTokens:             output,
	})
	engine.AnalyzeCache(snap, derefCodexCounter(input), usage.ObservedAt)
	return true
}

func codexSnapshotFromUsage(usage *codexusage.Usage, activation runtime.Activation) *schema.Snapshot {
	sessionID := usage.SessionID
	if sessionID == "" {
		sessionID = "codex-live"
	}
	model := secure.SafeIdentifier(usage.Model)
	if model == "" {
		model = "unknown"
	}
	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		PluginVersion: Version,
		ActivationID:  codexActivationID(activation),
		Client:        schema.ClientInfo{Type: schema.ClientCodex},
		Session: schema.SessionInfo{
			ID:                sessionID,
			StartedAt:         usage.ObservedAt,
			LastEventAt:       usage.ObservedAt,
			Status:            schema.SessionActive,
			StartSource:       "rollout",
			ConversationEpoch: 1,
		},
		Provider: activation.ProviderInfo(),
		Model: schema.ModelInfo{
			ID:             model,
			MetadataSource: "codex_rollout",
			AccessState:    schema.AccessUnknown,
		},
		Pressure: schema.PressureState{State: schema.PressureUnknown, ChangedAt: usage.ObservedAt},
		Activity: schema.ActivityState{Confidence: schema.ConfidenceClientLifecycle},
	}
	applyCodexUsage(snap, usage, activation)
	return snap
}

func codexActivationID(activation runtime.Activation) string {
	if !activation.Active {
		return ""
	}
	id, err := activation.Identity(runtime.DefaultSaltLoader())
	if err != nil {
		return ""
	}
	return id.DirName()
}

func int64PtrIfPositive(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func derefCodexCounter(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func codexUsageError(err error) string {
	switch {
	case errors.Is(err, codexusage.ErrNoRollout):
		return "no Codex rollout found"
	case errors.Is(err, codexusage.ErrNoUsage):
		return "Codex rollout has no completed token usage yet"
	default:
		return fmt.Sprintf("Codex rollout unavailable: %v", err)
	}
}

// safeInstanceID accepts only a bounded token suitable for a local filename.
func safeInstanceID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
