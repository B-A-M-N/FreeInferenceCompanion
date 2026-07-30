package render

import (
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// SurfaceEligibility captures every gate that must pass before any FreeInference
// surface renders visible output. When any gate fails, the output must be
// exactly zero bytes — no newline, no placeholder, no state directory creation.
//
// All persistent rendering (status line, compact footer, expanded report)
// must consult Visible() before writing anything to stdout.
type SurfaceEligibility struct {
	RuntimeActive     bool // runtime.Evaluate().Active is true
	ClientMatches     bool // the host client matches the activation client
	SessionMatches    bool // the snapshot belongs to the current session
	SessionActive     bool // session status is "active"
	ActivationMatch   bool // snapshot ActivationID equals current ActivationID
	ObservationFresh  bool // the latest host observation is recent enough
	ProviderConfirmed bool // provider is confirmed FreeInference
}

// FreshnessCutoff is the maximum age of a host observation before the surface
// is considered stale. Current-session status input always overrides recency.
const FreshnessCutoff = 2 * time.Minute

// EvaluateEligibility builds a SurfaceEligibility from the available signals.
// Parameters that are unknown (nil snap, empty activationID) produce false
// gates — the surface stays invisible until evidence confirms it should show.
func EvaluateEligibility(
	runtimeActive bool,
	clientType string,
	sessionID string,
	snap *schema.Snapshot,
	currentActivationID string,
	now time.Time,
) SurfaceEligibility {
	e := SurfaceEligibility{
		RuntimeActive: runtimeActive,
	}

	if snap == nil {
		return e
	}

	// Provider must be confirmed FreeInference.
	e.ProviderConfirmed = snap.Provider.Confirmed &&
		snap.Provider.Name == schema.ProviderFreeInference

	// Client must match the snapshot's client type.
	e.ClientMatches = clientType == "" || clientType == snap.Client.Type

	// Session must match (empty sessionID means "any current session" —
	// only applies for interactive mode, not embedded).
	if sessionID != "" {
		e.SessionMatches = snap.Session.ID == sessionID
	} else {
		e.SessionMatches = true
	}

	// Session status must be active.
	e.SessionActive = snap.Session.Status == schema.SessionActive

	// Activation identity must match the snapshot's activation.
	// Both must be non-empty and equal when currentActivationID is provided.
	// When currentActivationID is empty (inactive runtime), fail-closed:
	// only show data if the snapshot is also from an inactive/previous session.
	if currentActivationID == "" {
		// Fail-closed: no identity match when current is empty
		e.ActivationMatch = snap.ActivationID == ""
	} else if snap.ActivationID == "" {
		// Current has identity but snapshot does not — fail-closed
		e.ActivationMatch = false
	} else {
		e.ActivationMatch = snap.ActivationID == currentActivationID
	}

	// Observation freshness: the last status observation must be recent.
	if snap.LiveContext != nil && !snap.LiveContext.ObservedAt.IsZero() {
		observed := schema.SanitizeTimestamp(snap.LiveContext.ObservedAt, now)
		e.ObservationFresh = now.Sub(observed) <= FreshnessCutoff
	} else if !snap.Session.LastEventAt.IsZero() {
		// Fall back to last event time if no live context observation.
		lastEvent := schema.SanitizeTimestamp(snap.Session.LastEventAt, now)
		e.ObservationFresh = now.Sub(lastEvent) <= FreshnessCutoff
	} else {
		// No timestamps at all — treat as fresh on first contact.
		e.ObservationFresh = true
	}

	return e
}

// Visible reports whether the surface should produce any output at all.
// All gates must pass. When this returns false, the renderer must write
// exactly zero bytes.
func (e SurfaceEligibility) Visible() bool {
	return e.RuntimeActive &&
		e.ClientMatches &&
		e.SessionMatches &&
		e.SessionActive &&
		e.ActivationMatch &&
		e.ObservationFresh &&
		e.ProviderConfirmed
}
