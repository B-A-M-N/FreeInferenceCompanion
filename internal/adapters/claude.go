package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/engine"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

// PluginVersion is the companion version stamped onto new snapshots. It
// is initialized from pkg/version at package load time, but main()
// overrides it via ldflags so release builds report the correct version.
var PluginVersion = version.Version

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
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&input); err != nil {
		return nil, fmt.Errorf("parse status line: %w", err)
	}
	return &input, nil
}

// ParseHookInput reads and parses a flat Claude hook event from stdin.
func (a *ClaudeAdapter) ParseHookInput(r io.Reader) (*schema.ClaudeHookInput, error) {
	var input schema.ClaudeHookInput
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&input); err != nil {
		return nil, fmt.Errorf("parse hook event: %w", err)
	}
	return &input, nil
}

// newClaudeSnapshot builds a fresh snapshot for a newly seen session.
func newClaudeSnapshot(sessionID, modelID string, now time.Time) *schema.Snapshot {
	if modelID == "" {
		modelID = "unknown"
	}
	return &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		PluginVersion: PluginVersion,
		Client: schema.ClientInfo{
			Type: schema.ClientClaudeCode,
		},
		Session: schema.SessionInfo{
			ID:          sessionID,
			StartedAt:   now,
			LastEventAt: now,
			Status:      schema.SessionActive,
		},
		// Provider identity is supplied by the activation-aware caller. Keep a
		// new snapshot unresolved until that evidence is threaded through.
		Provider: schema.ProviderInfo{Name: schema.ProviderUnknown, Source: "unresolved"},
		Model: schema.ModelInfo{
			ID:             secure.SanitizeField(modelID),
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

func newClaudeSnapshotForActivation(sessionID, modelID string, now time.Time, activation runtime.Activation) *schema.Snapshot {
	snap := newClaudeSnapshot(sessionID, modelID, now)
	snap.Provider = activation.ProviderInfo()
	return snap
}

// HandleSessionStart initializes session state. Existing snapshots (which may
// already carry status-line telemetry) are preserved — only lifecycle fields
// and provider detection are refreshed.
//
// DEPRECATED: use HandleSessionStartWith, which accepts a runtime.Activation
// so the caller evaluates activation once and threads it through.
func (a *ClaudeAdapter) HandleSessionStart(input *schema.ClaudeHookInput) error {
	return a.HandleSessionStartWith(input, claudeActivation())
}

// claudeActivation is the lazy fallback used by deprecated Claude-only
// methods that do not receive an activation from their caller. New code
// should thread the activation through explicitly.
func claudeActivation() runtime.Activation {
	return runtime.EvaluateForClient(runtime.ClientClaudeCode)
}

// HandleSessionStartWith is the activation-aware variant. The caller must have
// already gated on activation.Active — this method does NOT re-check.
func (a *ClaudeAdapter) HandleSessionStartWith(input *schema.ClaudeHookInput, activation runtime.Activation) error {
	sessionID := input.SessionID
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	provider := activation.ProviderInfo()

	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID,
		func() *schema.Snapshot {
			return newClaudeSnapshotForActivation(sessionID, input.Model, now, activation)
		},
		func(snap *schema.Snapshot) error {
			snap.Session.Status = schema.SessionActive
			snap.Session.LastEventAt = now
			snap.Session.EndedAt = nil
			snap.Provider = provider
			snap.ActivationID = a.Paths.ActivationID
			// Only fill in the model if we don't already know a better one.
			if input.Model != "" && (snap.Model.ID == "" || snap.Model.ID == "unknown") {
				snap.Model.ID = secure.SanitizeField(input.Model)
				snap.Model.MetadataSource = "client_hook"
			}
			return nil
		})
	if err == nil {
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
			state.Event{Type: state.EventSessionStarted, Model: input.Model, Provider: provider.Name})
	}
	return err
}

// HandleStatusLineUpdate processes a status line input and updates session state.
// Status line updates upsert the session if it doesn't exist yet (handles the
// async SessionStart race). Values are never accumulated — each update is one
// observation of the current context, deduplicated by fingerprint.
//
// P0-2/P0-3: the caller (cmdStatus) MUST gate on activation.Active before
// invoking this method. Status-line updates are the most frequent automatic
// integration and must be a true no-op for ordinary Claude sessions.
func (a *ClaudeAdapter) HandleStatusLineUpdate(input *schema.ClaudeStatusLineInput, sessionID string) error {
	return a.HandleStatusLineUpdateWith(input, sessionID, claudeActivation())
}

// HandleStatusLineUpdateWith is the activation-aware variant. The caller must
// have already gated on activation.Active — this method does NOT re-check.
func (a *ClaudeAdapter) HandleStatusLineUpdateWith(input *schema.ClaudeStatusLineInput, sessionID string, activation runtime.Activation) error {
	if sessionID == "" {
		return nil
	}
	var sawCompactionCompletion bool
	var compactionReductionPct *float64
	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID,
		func() *schema.Snapshot {
			return newClaudeSnapshotForActivation(sessionID, input.Model.ID, time.Now().UTC(), activation)
		},
		func(snap *schema.Snapshot) error {
			now := time.Now().UTC()

			// Stamp the activation identity so the render layer can gate
			// visibility on activation match.
			snap.ActivationID = a.Paths.ActivationID

			previousModel := snap.Model.ID
			// Model info from status line (authoritative). The model ID is
			// client-controlled and sanitized to prevent terminal injection
			// when the value is later rendered.
			if input.Model.ID != "" {
				snap.Model.ID = secure.SanitizeField(input.Model.ID)
				snap.Model.MetadataSource = "client_statusline"
			}
			if input.Version != "" {
				version := secure.SanitizeField(input.Version)
				snap.Client.Version = &version
			}
			if input.Model.DisplayName != "" {
				// The display name is client-controlled and could in theory
				// carry a value the user pasted with a secret in it. Redact
				// defensively before persisting.
				displayName := secure.Redact(input.Model.DisplayName)
				snap.Model.DisplayName = &displayName
			}
			if input.ContextWindow.ContextWindowSize > 0 {
				size := input.ContextWindow.ContextWindowSize
				snap.Model.ContextLength = &size
			}

			snap.Provider = activation.ProviderInfo()
			nowSemantics := ClaudeTokenSemantics(input.Version)
			if input.Version == "" && snap.LiveContext != nil {
				nowSemantics = snap.LiveContext.TotalTokenSemantics
			}
			if nowSemantics == "" {
				nowSemantics = schema.TokenSemanticsUnknown
			}
			if snap.CacheEpochID == "" {
				beginCacheEpoch(snap, "session_start", now)
			} else if previousModel != "" && previousModel != "unknown" && input.Model.ID != "" && previousModel != secure.SanitizeField(input.Model.ID) {
				beginCacheEpoch(snap, "model_switch", now)
			}

			// Latest request usage (may be nil before first response or
			// immediately after compaction).
			var latest *schema.RequestUsage
			if input.ContextWindow.CurrentUsage != nil {
				cu := input.ContextWindow.CurrentUsage
				latest = &schema.RequestUsage{}
				if cu.InputTokens != nil {
					v := *cu.InputTokens
					latest.FreshInputTokens = &v
				}
				if cu.CacheReadInputTokens != nil {
					v := *cu.CacheReadInputTokens
					latest.CacheReadInputTokens = &v
				}
				if cu.CacheCreationInputTokens != nil {
					v := *cu.CacheCreationInputTokens
					latest.CacheCreationInputTokens = &v
				}
				if cu.OutputTokens != nil {
					v := *cu.OutputTokens
					latest.OutputTokens = &v
				}
			}

			var totalInput, totalOutput *int64
			if input.ContextWindow.TotalInputTokens != nil {
				t := *input.ContextWindow.TotalInputTokens
				totalInput = &t
			}
			if input.ContextWindow.TotalOutputTokens != nil {
				t := *input.ContextWindow.TotalOutputTokens
				totalOutput = &t
			}
			var ctxSize *int64
			if input.ContextWindow.ContextWindowSize > 0 {
				s := input.ContextWindow.ContextWindowSize
				ctxSize = &s
			}
			usedPct := input.ContextWindow.UsedPercentage
			var remainingPct *float64
			if input.ContextWindow.RemainingPercentage != nil {
				// Prefer the authoritative value from the status line.
				r := *input.ContextWindow.RemainingPercentage
				remainingPct = &r
			} else if usedPct != nil {
				// Fallback for older clients that don't expose it.
				r := 100.0 - *usedPct
				remainingPct = &r
			}

			snap.LiveContext = &schema.LiveContext{
				Source:              "claude_statusline",
				ObservedAt:          now,
				TotalTokenSemantics: nowSemantics,
				TotalInputTokens:    totalInput,
				TotalOutputTokens:   totalOutput,
				ContextWindowSize:   ctxSize,
				UsedPercentage:      usedPct,
				RemainingPercentage: remainingPct,
				LatestRequest:       latest,
			}

			// Record a unique usage observation (deduped by fingerprint) and
			// refresh rolling cache analysis.
			newObservation := false
			if latest != nil {
				identity := engine.BuildObservationIdentity(
					snap.Model.ID,
					input.PromptID,
					deref(totalInput), deref(totalOutput),
					latest.FreshInputTokens, latest.CacheReadInputTokens,
					latest.CacheCreationInputTokens, latest.OutputTokens)
				obs := schema.UsageObservation{
					Fingerprint:              identity.Fingerprint,
					FingerprintSource:        identity.Source,
					ObservedAt:               now,
					ModelID:                  snap.Model.ID,
					TotalInputTokens:         totalInput,
					TotalOutputTokens:        totalOutput,
					FreshInputTokens:         latest.FreshInputTokens,
					CacheReadInputTokens:     latest.CacheReadInputTokens,
					CacheCreationInputTokens: latest.CacheCreationInputTokens,
					OutputTokens:             latest.OutputTokens,
					RequestReference:         input.PromptID,
					EpochID:                  snap.CacheEpochID,
					EpochReason:              snap.CacheEpochReason,
				}
				newObservation = engine.AddObservation(snap, obs)
			}
			// Track cache timing separately from general session activity.
			// Only refresh on new inference observations so we don't
			// pollute the cache clock with lifecycle noise.
			if newObservation {
				if snap.CacheTiming == nil {
					snap.CacheTiming = &schema.CacheTiming{}
				}
				snap.CacheTiming.LastInferenceObservedAt = now
			}
			if newObservation {
				engine.AnalyzeCache(snap, ActiveContextTokens(snap), now)
			}

			// Pressure state from the authoritative used percentage.
			if usedPct != nil {
				newState, reason := engine.ClassifyPressure(*usedPct, snap.Pressure.State)
				if newState != snap.Pressure.State {
					snap.Pressure.PreviousState = snap.Pressure.State
					snap.Pressure.ChangedAt = now
				}
				snap.Pressure.State = newState
				snap.Pressure.Reason = reason
			}

			// Complete a pending compaction measurement on the first changed
			// observation after PostCompact.
			if snap.Compaction.AwaitingPostObservation && newObservation {
				completeCompaction(snap, now)
				sawCompactionCompletion = true
				if snap.Compaction.LastResult != nil {
					compactionReductionPct = snap.Compaction.LastResult.ReductionPct
				}
			}

			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
			state.Event{Type: state.EventStatusObserved, Model: input.Model.ID})
		if sawCompactionCompletion {
			detail := "unknown"
			if compactionReductionPct != nil {
				detail = fmt.Sprintf("%.0f%%", *compactionReductionPct)
			}
			appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
				state.Event{Type: state.EventCompactionCompleted, Detail: detail})
		}
	}
	return err
}

// completeCompaction calculates the reduction from pre/post context totals.
// Guards against false and negative reductions:
//   - pre must be a valid positive token count
//   - post must be a valid positive token count
//   - post must be <= pre (compaction cannot increase context)
//   - the completion must happen within a bounded timeout of PostCompact
//
// If any guard fails, the result is recorded as unknown (no ReductionPct)
// rather than as a misleading or negative reduction.
func completeCompaction(snap *schema.Snapshot, now time.Time) {
	snap.Compaction.AwaitingPostObservation = false
	pre := snap.Compaction.PreTokens
	post := ActiveContextTokens(snap)

	result := &schema.CompactionResult{
		At:        now,
		PreTokens: pre,
	}

	// Valid pre-compaction observation is required.
	if pre == nil || *pre <= 0 {
		snap.Compaction.LastResult = result
		return
	}

	// Bounded completion timeout: if too much time has passed since
	// PostCompact, the post observation may not represent the compacted
	// context — record as unknown.
	if snap.Compaction.InitiatedAt != nil {
		const compactionCompletionTimeout = 5 * time.Minute
		if now.Sub(*snap.Compaction.InitiatedAt) > compactionCompletionTimeout {
			snap.Compaction.LastResult = result
			return
		}
	}

	// Valid post observation required.
	if post <= 0 {
		snap.Compaction.LastResult = result
		return
	}

	// Compaction cannot increase context. If post >= pre, the observation
	// does not represent a successful compaction — record as unknown rather
	// than a false or negative reduction.
	if post >= *pre {
		snap.Compaction.LastResult = result
		return
	}

	result.PostTokens = &post
	reduction := float64(*pre-post) / float64(*pre) * 100
	result.ReductionPct = &reduction
	if snap.Compaction.Trigger != nil {
		result.Trigger = *snap.Compaction.Trigger
	}
	snap.Compaction.LastResult = result
}

// ActiveContextTokens returns the best estimate of the current active context
// size in tokens. Current-context totals are authoritative only when the
// Claude version contract says they are current-context values. Older and
// unknown versions use used_percentage × context_window_size; their total
// counters may be cumulative session values and must not drive context or
// compaction math. Zero means unknown.
func ActiveContextTokens(snap *schema.Snapshot) int64 {
	if snap == nil || snap.LiveContext == nil {
		return 0
	}
	lc := snap.LiveContext
	if lc.TotalTokenSemantics == schema.TokenSemanticsCurrentContext && lc.TotalInputTokens != nil {
		return maxInt64(*lc.TotalInputTokens, 0)
	}
	if lc.UsedPercentage != nil && lc.ContextWindowSize != nil {
		return int64(math.Round(*lc.UsedPercentage / 100.0 * float64(*lc.ContextWindowSize)))
	}
	return 0
}

func maxInt64(value, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
}

// CurrentContextFootprint returns the current input context plus the most
// recent response output when both current-context semantics and values are
// available. It is a display metric, not a cumulative session total.
func CurrentContextFootprint(snap *schema.Snapshot) int64 {
	if snap == nil || snap.LiveContext == nil || snap.LiveContext.TotalTokenSemantics != schema.TokenSemanticsCurrentContext {
		return 0
	}
	lc := snap.LiveContext
	var total int64
	if lc.TotalInputTokens != nil {
		total += *lc.TotalInputTokens
	}
	if lc.TotalOutputTokens != nil {
		total += *lc.TotalOutputTokens
	}
	return total
}

// TotalContextTokens is retained as a compatibility alias. It no longer
// represents a cumulative session total; use CurrentContextFootprint.
// Deprecated: use CurrentContextFootprint.
func TotalContextTokens(snap *schema.Snapshot) int64 {
	return CurrentContextFootprint(snap)
}

// ClaudeTokenSemantics maps the Claude Code version boundary that changed
// total_input_tokens from a cumulative session counter to current context.
// An absent or malformed version is unknown and therefore fail-closed for
// token sizing.
func ClaudeTokenSemantics(version string) schema.TokenSemantics {
	major, minor, patch, ok := parseClientVersion(version)
	if !ok {
		return schema.TokenSemanticsUnknown
	}
	if major > 2 || (major == 2 && (minor > 1 || (minor == 1 && patch >= 132))) {
		return schema.TokenSemanticsCurrentContext
	}
	return schema.TokenSemanticsCumulativeSession
}

func parseClientVersion(version string) (major, minor, patch int, ok bool) {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	parts := strings.SplitN(version, ".", 3)
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, false
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, 0, false
	}
	patchPart := parts[2]
	if dash := strings.IndexByte(patchPart, '-'); dash >= 0 {
		patchPart = patchPart[:dash]
	}
	if patch, err = strconv.Atoi(patchPart); err != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, major >= 0 && minor >= 0 && patch >= 0
}

func beginCacheEpoch(snap *schema.Snapshot, reason string, now time.Time) {
	if snap == nil {
		return
	}
	firstEpoch := snap.CacheEpochID == ""
	snap.CacheEpochID = fmt.Sprintf("epoch-%d", now.UnixNano())
	snap.CacheEpochReason = reason
	snap.CacheEpochStartedAt = now
	// Legacy observations without an epoch can be assigned to the initial
	// lineage. Once a boundary exists, leave them untagged rather than
	// accidentally importing pre-boundary history into the new epoch.
	if firstEpoch {
		for i := range snap.UsageObservations {
			if snap.UsageObservations[i].EpochID == "" {
				snap.UsageObservations[i].EpochID = snap.CacheEpochID
				snap.UsageObservations[i].EpochReason = reason
			}
		}
	}
}

// HandleUserPromptSubmit activates the turn and produces warnings.
// Returns (nil, nil) when there is nothing to show — no stdout output.
//
// DEPRECATED: use HandleUserPromptSubmitWith, which accepts a runtime.Activation.
func (a *ClaudeAdapter) HandleUserPromptSubmit(input *schema.ClaudeHookInput, sessionID string) (*schema.ClaudeWarningOutput, error) {
	return a.HandleUserPromptSubmitWith(input, sessionID, claudeActivation())
}

// HandleUserPromptSubmitWith is the activation-aware variant. The caller must
// have already gated on activation.Active.
func (a *ClaudeAdapter) HandleUserPromptSubmitWith(input *schema.ClaudeHookInput, sessionID string, activation runtime.Activation) (*schema.ClaudeWarningOutput, error) {
	if sessionID == "" {
		return nil, nil
	}
	now := time.Now().UTC()
	var output *schema.ClaudeWarningOutput
	var events []state.Event

	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID,
		func() *schema.Snapshot {
			return newClaudeSnapshot(sessionID, "", now)
		},
		func(snap *schema.Snapshot) error {
			active := true
			// Capture the previous activity timestamp BEFORE overwriting it.
			// The cache TTL warning needs the idle gap between the last event
			// and this prompt — if we overwrite first, the gap is always zero.
			prevLastEventAt := snap.Session.LastEventAt
			// Use CacheTiming as the authoritative cache clock. The cache
			// TTL evaluation must not be driven by Session.LastEventAt which
			// gets updated for every lifecycle event. Instead use
			// LastInferenceObservedAt which only advances on real inference
			// observations. Fall back to LastEventAt if CacheTiming has never
			// been set (legacy sessions).
			cacheClock := prevLastEventAt
			if snap.CacheTiming != nil && !snap.CacheTiming.LastInferenceObservedAt.IsZero() {
				cacheClock = snap.CacheTiming.LastInferenceObservedAt
			}
			snap.Activity.TurnActive = &active
			snap.Activity.TurnStartedAt = &now
			snap.Session.Status = schema.SessionActive
			snap.Session.LastEventAt = now
			snap.ActivationID = a.Paths.ActivationID
			snap.Provider = activation.ProviderInfo()

			// Never warn during a non-FreeInference session.
			if !IsConfirmedFreeInference(snap.Provider) {
				return nil
			}

			// Calculate all warning-family state transitions FIRST,
			// then select one user-facing message by priority afterward.
			// This ensures that cache-warning recovery is evaluated even
			// when a context warning fires (the old code short-circuited).

			// 1. Context pressure warning state
			var contextMsg string
			var contextSeverity string
			var contextWouldShow bool
			if snap.LiveContext != nil && snap.LiveContext.UsedPercentage != nil {
				usedPct := *snap.LiveContext.UsedPercentage
				switch {
				case usedPct >= engine.ContextCriticalEnterThreshold():
					contextSeverity = schema.WarningSeverityCritical
					contextMsg = fmt.Sprintf("FreeInference: context usage is %.0f%% on %s. Compact or start a fresh session.", usedPct, snap.Model.ID)
				case usedPct >= engine.ContextWarnEnterThreshold():
					contextSeverity = schema.WarningSeverityWarn
					contextMsg = fmt.Sprintf("FreeInference: context usage is %.0f%% on %s. Consider compacting soon.", usedPct, snap.Model.ID)
				}
			}
			if contextMsg != "" {
				contextWouldShow = shouldShowContextWarning(snap, contextSeverity, now)
			}

			// 2. Projection warning state (only evaluated if context won't show)
			var projectionMsg string
			if !contextWouldShow && shouldShowProjectionWarning(snap, now) {
				projection := engine.ProjectNextRequest(
					ActiveContextTokens(snap),
					promptByteSize(input),
					snap.Model.ContextLength,
					engine.DefaultOutputReserve(),
				)
				projectionMsg = projection.AdvisoryMessage()
			}

			// 3. Cache warning state
			cacheDecision := engine.QualifyCacheWarning(snap, ActiveContextTokens(snap), true, now)

			// 4. Cache TTL expiry warning state. The prompt cache evaporates
			// after ~5min idle; warn the user that their next request will
			// pay full price for the entire context. Only evaluated when
			// context warning won't show (context is urgent safety; TTL is
			// cost — but both matter, so TTL gets priority over projection
			// and cache-low because it's the most actionable cost signal).
			// Uses cacheClock (derived from CacheTiming when available) — the
			// idle gap is between the last inference observation and now.
			var ttlDecision engine.CacheTTLDecision
			var ttlWouldShow bool
			if !contextWouldShow {
				ttlDecision = engine.EvaluateCacheTTLExpiryV2(snap, ActiveContextTokens(snap), cacheClock, now)
				if ttlDecision.Warn && engine.ShouldShowCacheTTLWarning(snap, now) {
					ttlWouldShow = true
				}
			}

			// Persist ALL state transitions now (before short-circuiting).
			if contextWouldShow {
				snap.Warnings.ContextSeverity = contextSeverity
				snap.Warnings.LastContextShownAt = &now
				snap.Warnings.HistoryCount++
				events = append(events, state.Event{Type: state.EventWarningShown, Detail: "context:" + contextSeverity})
			} else if ttlWouldShow {
				snap.Warnings.CacheTTLWarningActive = true
				snap.Warnings.LastCacheTTLShownAt = &now
				snap.Warnings.HistoryCount++
				events = append(events, state.Event{Type: state.EventWarningShown, Detail: "cache_ttl_expiry"})
			} else if projectionMsg != "" {
				snap.Warnings.LastContextShownAt = &now
				snap.Warnings.HistoryCount++
				events = append(events, state.Event{Type: state.EventWarningShown, Detail: "projection_overflow"})
			}

			// TTL state is only authoritative when the provider supplied a TTL.
			// Never use a local default timer to infer that the cache recovered.
			ttlKnown := snap.CacheTiming != nil && snap.CacheTiming.CacheTTLSeconds != nil && *snap.CacheTiming.CacheTTLSeconds > 0
			if snap.Warnings.CacheTTLWarningActive && !ttlWouldShow && ttlKnown && now.Sub(cacheClock) < time.Duration(*snap.CacheTiming.CacheTTLSeconds)*time.Second {
				snap.Warnings.CacheTTLWarningActive = false
				events = append(events, state.Event{Type: state.EventWarningResolved, Detail: "cache_ttl_expiry"})
			}

			switch {
			case cacheDecision.Warn:
				snap.Warnings.CacheLowActive = true
				snap.Warnings.LastCacheShownAt = &now
				snap.Warnings.HistoryCount++
				events = append(events, state.Event{Type: state.EventWarningShown, Detail: "cache_low"})
			case cacheDecision.Resolved:
				snap.Warnings.CacheLowActive = false
				events = append(events, state.Event{Type: state.EventWarningResolved})
			}

			// Select ONE output message by priority:
			// context > cache-ttl > projection > cache-low.
			switch {
			case contextWouldShow:
				output = &schema.ClaudeWarningOutput{
					Continue:       true,
					SystemMessage:  contextMsg,
					SuppressOutput: true,
				}
			case ttlWouldShow:
				output = &schema.ClaudeWarningOutput{
					Continue:       true,
					SystemMessage:  engine.CacheTTLWarningMessageV2(snap, ttlDecision.IdleMinutes, ActiveContextTokens(snap)),
					SuppressOutput: true,
				}
			case projectionMsg != "":
				output = &schema.ClaudeWarningOutput{
					Continue:       true,
					SystemMessage:  projectionMsg,
					SuppressOutput: true,
				}
			case cacheDecision.Warn:
				share := 0.0
				if cacheDecision.Share != nil {
					share = *cacheDecision.Share
				}
				// Include pattern classification and a likely diagnosis so the
				// warning is actionable without claiming a confirmed cause.
				diag := engine.BuildCacheDiagnosis(snap, now)
				var hypothesis string
				if len(diag.CandidateCauses) > 0 {
					hypothesis = diag.CandidateCauses[0].Label
				}
				msg := fmt.Sprintf(
					"FreeInference: cache reuse is low (read share %.0f%% over recent requests). Repeated full-context re-reads increase latency and cost.",
					share*100)
				if hypothesis != "" && diag.Kind != "unknown" {
					msg += " Possible cause: " + hypothesis + "."
				}
				if diag.Kind == "unknown" || diag.Confidence < 0.3 {
					msg += " FreeInference has not returned a cache-miss reason, so causes are hypothetical."
				}
				output = &schema.ClaudeWarningOutput{
					Continue:       true,
					SystemMessage:  msg,
					SuppressOutput: true,
				}
			}
			return nil
		})

	if err != nil {
		return nil, nil
	}
	appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
		state.Event{Type: state.EventPromptSubmitted})
	for _, ev := range events {
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID, ev)
	}
	return output, nil
}

// promptByteSize returns the byte length of the incoming prompt, or 0 if
// unavailable. The prompt itself is never persisted — only its size enters
// the local projection math.
func promptByteSize(input *schema.ClaudeHookInput) int {
	if input == nil {
		return 0
	}
	return len(input.Prompt)
}

// shouldShowProjectionWarning gates projection warnings on the same cooldown
// as context warnings so the user is not nagged on every prompt. Projection
// is suppressed entirely below the watch threshold — there is no value in
// warning about output reserve when context is comfortably empty.
func shouldShowProjectionWarning(snap *schema.Snapshot, now time.Time) bool {
	if snap.LiveContext == nil || snap.LiveContext.UsedPercentage == nil {
		return false
	}
	if *snap.LiveContext.UsedPercentage < engine.ContextWatchEnterThreshold() {
		return false
	}
	if snap.Warnings.LastContextShownAt != nil &&
		now.Sub(*snap.Warnings.LastContextShownAt) < engine.ContextWarningCooldown {
		// Already showed a context-family warning recently — give the user
		// time to act before re-flagging.
		return false
	}
	return true
}

// shouldShowContextWarning determines if a context warning should be shown
// based on cooldown and severity escalation.
func shouldShowContextWarning(snap *schema.Snapshot, severity string, now time.Time) bool {
	if snap.Warnings.LastContextShownAt == nil {
		return true
	}
	if severityOrder(severity) > severityOrder(snap.Warnings.ContextSeverity) {
		return true
	}
	return now.Sub(*snap.Warnings.LastContextShownAt) >= engine.ContextWarningCooldown
}

func severityOrder(s string) int {
	switch s {
	case schema.WarningSeverityCritical:
		return 3
	case schema.WarningSeverityWarn:
		return 2
	case schema.WarningSeverityWatch:
		return 1
	default:
		return 0
	}
}

// HandlePreCompact records pre-compaction state: the active context total,
// the hook trigger, and the initiation time.
func (a *ClaudeAdapter) HandlePreCompact(input *schema.ClaudeHookInput, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, nil,
		func(snap *schema.Snapshot) error {
			snap.Compaction.Pending = true
			snap.Compaction.AwaitingPostObservation = false
			snap.Compaction.PreTokens = nil
			snap.Compaction.Trigger = nil
			snap.Compaction.InitiatedAt = &now
			if input != nil && input.Trigger != "" {
				trigger := input.Trigger
				snap.Compaction.Trigger = &trigger
			}
			if pre := ActiveContextTokens(snap); pre > 0 {
				snap.Compaction.PreTokens = &pre
			}
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		trigger := ""
		if input != nil {
			trigger = input.Trigger
		}
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
			state.Event{Type: state.EventCompactionStarted, Detail: trigger})
	}
	return err
}

// HandlePostCompact marks compaction as awaiting the next changed status-line
// observation, which completes the reduction calculation.
func (a *ClaudeAdapter) HandlePostCompact(input *schema.ClaudeHookInput, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	return state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, nil,
		func(snap *schema.Snapshot) error {
			snap.Compaction.Pending = false
			beginCacheEpoch(snap, "compaction", now)
			if snap.Compaction.PreTokens != nil {
				snap.Compaction.AwaitingPostObservation = true
			}
			if input != nil && input.Trigger != "" && snap.Compaction.Trigger == nil {
				trigger := input.Trigger
				snap.Compaction.Trigger = &trigger
			}
			snap.Session.LastEventAt = now
			return nil
		})
}

// HandleStopFailure records a structured failure from the StopFailure hook.
func (a *ClaudeAdapter) HandleStopFailure(input *schema.ClaudeHookInput, sessionID string) error {
	if sessionID == "" || input.Error == "" {
		return nil
	}
	category := sanitizeFailureCategory(input.Error)
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, nil,
		func(snap *schema.Snapshot) error {
			inactive := false
			snap.Activity.TurnActive = &inactive
			snap.Activity.TurnEndedAt = &now
			snap.LastFailure = &schema.FailureRecord{
				Category:   category,
				ObservedAt: now,
				Source:     "claude_stop_failure",
			}
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
			state.Event{Type: state.EventTurnFailed, Detail: category})
	}
	return err
}

// HandlePostModelSwitch records a model discontinuity and starts a fresh
// cache-analysis epoch. The hook payload's model is authoritative when it is
// present; the next status-line observation remains a second line of defense.
func (a *ClaudeAdapter) HandlePostModelSwitch(input *schema.ClaudeHookInput, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, nil,
		func(snap *schema.Snapshot) error {
			if input != nil && input.Model != "" {
				snap.Model.ID = secure.SanitizeField(input.Model)
				snap.Model.MetadataSource = "client_hook"
			}
			beginCacheEpoch(snap, "model_switch", now)
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
			state.Event{Type: state.EventModelSwitch, Model: inputModel(input)})
	}
	return err
}

func inputModel(input *schema.ClaudeHookInput) string {
	if input == nil {
		return ""
	}
	return secure.SanitizeField(input.Model)
}

// HandleSessionEnd marks a session as completed.
func (a *ClaudeAdapter) HandleSessionEnd(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, nil,
		func(snap *schema.Snapshot) error {
			inactive := false
			snap.Session.Status = schema.SessionCompleted
			snap.Session.EndedAt = &now
			snap.Session.LastEventAt = now
			snap.Activity.TurnActive = &inactive
			return nil
		})
	if err == nil {
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
			state.Event{Type: state.EventSessionEnded})
	}
	return err
}

// HandleStop marks the turn as inactive without ending the session.
func (a *ClaudeAdapter) HandleStop(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	err := state.UpdateSnapshot(a.Paths, schema.ClientClaudeCode, sessionID, nil,
		func(snap *schema.Snapshot) error {
			inactive := false
			snap.Activity.TurnActive = &inactive
			snap.Activity.TurnEndedAt = &now
			snap.Session.LastEventAt = now
			return nil
		})
	if err == nil {
		appendEventBestEffort(a.Paths, schema.ClientClaudeCode, sessionID,
			state.Event{Type: state.EventTurnStopped})
	}
	return err
}

// sanitizeFailureCategory maps a raw StopFailure.Error string to a short,
// shareable category. The raw error is never persisted — only the category.
func sanitizeFailureCategory(raw string) string {
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
	// Unknown failures get a generic bucket so the raw text is never stored.
	return "unknown"
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// appendEventBestEffort records a sanitized lifecycle event. Any failure is
// swallowed because event logging must never block the client.
func appendEventBestEffort(paths state.Paths, clientType, sessionID string, ev state.Event) {
	_ = paths.EnsureSessionDir(clientType, sessionID)
	_ = state.AppendEvent(paths, clientType, sessionID, ev)
	_ = state.RotateEvents(paths, clientType, sessionID)
}
