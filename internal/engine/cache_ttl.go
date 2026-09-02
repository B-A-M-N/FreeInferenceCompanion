package engine

import (
	"fmt"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

const (
	// PromptCacheTTL is retained only for the deprecated estimator API and
	// compatibility with older callers. New warning paths require a
	// provider-confirmed CacheTTLSeconds value and never use this default to
	// claim that a cache expired.
	PromptCacheTTL = 5 * time.Minute

	// CacheTTLWarningCooldown suppresses repeat TTL warnings within one
	// idle episode. If the user was idle 6 minutes, sent a prompt (got
	// warned), then was idle 2 more minutes and sent another, we don't
	// want to nag again — they already know the cache is cold.
	CacheTTLWarningCooldown = 30 * time.Minute

	// MinActiveTokensForTTLWarning gates the TTL warning on meaningful
	// context. A 3-message chat has nothing worth caching, so warning
	// about cache expiry would be noise.
	MinActiveTokensForTTLWarning = 10000
)

// CacheTTLDecision summarizes the cache TTL warning evaluation.
type CacheTTLDecision struct {
	Warn        bool
	IdleMinutes int
}

// EvaluateCacheTTLExpiry is the legacy estimator. It is retained for source
// compatibility, but new code should use EvaluateCacheTTLExpiryV2, which
// refuses to infer expiry without provider-confirmed TTL data.
//
// The companion cannot observe the actual cache state. This compatibility
// wrapper delegates to the provider-confirmed implementation below and will
// not infer eviction from a local idle timer.
//
// The caller MUST pass the previous LastEventAt (before overwriting it with
// now), not the current snapshot's value — the hook handler updates
// LastEventAt to now before evaluating warnings, so reading it from the
// snapshot would always report zero idle time.
//
// Gates:
//   - Provider must be confirmed FreeInference.
//   - Active context must be meaningful (>= MinActiveTokensForTTLWarning).
//   - lastEventAt must be non-zero.
//   - Cooldown is checked by the caller (this function is pure evaluation).
func EvaluateCacheTTLExpiry(snap *schema.Snapshot, activeTokens int64, lastEventAt time.Time, now time.Time) CacheTTLDecision {
	return EvaluateCacheTTLExpiryV2(snap, activeTokens, lastEventAt, now)
}

// ShouldShowCacheTTLWarning applies the cooldown gate. A TTL warning shows
// when either:
//   - No prior TTL warning has been shown, or
//   - The cooldown has elapsed since the last one.
func ShouldShowCacheTTLWarning(snap *schema.Snapshot, now time.Time) bool {
	if snap == nil {
		return false
	}
	if snap.Warnings.LastCacheTTLShownAt == nil {
		return true
	}
	return now.Sub(*snap.Warnings.LastCacheTTLShownAt) >= CacheTTLWarningCooldown
}

// CacheTTLWarningMessage builds the user-facing message for a cache TTL
// expiry warning. Neutral guidance: the next request may need to rebuild
// the cached prefix, so the user should consider whether preserving the
// current context is worth that one-time processing cost.
//
// DEPRECATED: Use CacheTTLWarningMessageV2 for honest language about
// whether the TTL is known or estimated.
func CacheTTLWarningMessage(idleMinutes int, activeTokens int64) string {
	tokens := formatTokenCountBrief(activeTokens)
	return fmt.Sprintf(
		"FreeInference: prompt cache may have expired (idle %dm without confirmed TTL data). "+
			"Your next request may need to rebuild the cached prefix (~%s of context).",
		idleMinutes, tokens)
}

// EvaluateCacheTTLExpiryV2 is the CacheTiming-aware variant of
// EvaluateCacheTTLExpiry. It uses snap.CacheTiming.LastInferenceObservedAt
// for idle-gap calculation instead of the caller-provided lastEventAt.
//
// WARNING: Only generates a warning when the provider has confirmed a
// cache TTL via CacheTTLSeconds. Without provider-confirmed TTL data, the
// companion cannot know the actual server-side cache policy and returns
// an empty (no-warning) decision.
//
// For backward compatibility: if CacheTiming is nil or
// LastInferenceObservedAt is zero, falls back to lastEventAt.
func EvaluateCacheTTLExpiryV2(snap *schema.Snapshot, activeTokens int64, lastEventAt time.Time, now time.Time) CacheTTLDecision {
	if snap == nil || lastEventAt.IsZero() {
		return CacheTTLDecision{}
	}
	if !isConfirmedFI(snap.Provider) {
		return CacheTTLDecision{}
	}
	if activeTokens < MinActiveTokensForTTLWarning {
		return CacheTTLDecision{}
	}

	// Use CacheTiming as the authoritative cache clock when available.
	idleBase := lastEventAt
	if snap.CacheTiming != nil && !snap.CacheTiming.LastInferenceObservedAt.IsZero() {
		idleBase = snap.CacheTiming.LastInferenceObservedAt
	}

	idle := now.Sub(idleBase)

	// Only warn when the provider has confirmed a cache TTL. Without
	// provider-supplied CacheTTLSeconds, the companion cannot know the
	// actual server-side cache policy — any hardcoded default would be
	// speculative.
	if snap.CacheTiming == nil || snap.CacheTiming.CacheTTLSeconds == nil || *snap.CacheTiming.CacheTTLSeconds <= 0 {
		return CacheTTLDecision{}
	}

	cacheTTL := time.Duration(*snap.CacheTiming.CacheTTLSeconds) * time.Second

	if idle < cacheTTL {
		return CacheTTLDecision{}
	}

	return CacheTTLDecision{
		Warn:        true,
		IdleMinutes: int(idle.Minutes()),
	}
}

// CacheTTLWarningMessageV2 produces honest cache-TTL warning language that
// distinguishes known vs. unknown TTL data.
//
// If CacheTTLSeconds is nil: "FreeInference: prompt cache may have expired
// (idle Xm without confirmed TTL data). Your next request may need to rebuild
// the cached prefix."
//
// If CacheTTLSeconds is set: "FreeInference: prompt cache expired (provider
// TTL: Xs, idle Xm). Your next request may need to rebuild the cached prefix."
func CacheTTLWarningMessageV2(snap *schema.Snapshot, idleMinutes int, activeTokens int64) string {
	if snap == nil || snap.CacheTiming == nil || snap.CacheTiming.CacheTTLSeconds == nil {
		return fmt.Sprintf(
			"FreeInference: prompt cache may have expired (idle %dm without confirmed TTL data). "+
				"Your next request may need to rebuild the cached prefix.",
			idleMinutes)
	}

	providerTTL := *snap.CacheTiming.CacheTTLSeconds
	return fmt.Sprintf(
		"FreeInference: prompt cache expired (provider TTL: %ds, idle %dm). "+
			"Your next request may need to rebuild the cached prefix.",
		providerTTL, idleMinutes)
}

// isConfirmedFI is a local copy of the provider check to avoid an import
// cycle (engine -> adapters would be circular). This mirrors
// adapters.IsConfirmedFreeInference.
func isConfirmedFI(p schema.ProviderInfo) bool {
	return p.Confirmed && p.Name == schema.ProviderFreeInference
}

// formatTokenCountBrief renders a token count as e.g. "127K" or "1.2M".
func formatTokenCountBrief(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%dK", n/1_000)
	}
	return fmt.Sprintf("%d", n)
}
