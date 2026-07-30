// Package runtime is the authoritative source for runtime activation and
// endpoint configuration. It centralizes the rules that decide whether the
// companion is "active" for the current process, and separates the runtime
// provider endpoint (the HTTPS server the coding agent actually talks to)
// from the companion management API (the catalog/health endpoints the
// background refreshers call).
//
// Activation is evaluated ONCE per process invocation by Evaluate(). The
// result is passed into adapters, refreshers, and the CLI. Layers must NOT
// re-read environment variables to make activation decisions.
//
// Hard contract — activation requires ALL of:
//  1. FI_DISABLED != 1
//  2. A runtime endpoint is explicitly present (no defaults)
//  3. The endpoint normalizes successfully (HTTPS, no userinfo/fragment/query)
//  4. The endpoint belongs to the approved FreeInference host set
//  5. A supported runtime credential is present
//  6. No conflicting runtime endpoint is configured
//
// FI_PROVIDER=freeinference does NOT activate. It is recorded as attribution
// metadata only. FI_UNSAFE_FORCE_ACTIVATION=1 is a dev-only override that is
// always recorded in InactiveReason/Origin for auditability.
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ActivationReason is a machine-readable reason code explaining why activation
// succeeded or failed. Always populated on the inactive path; empty on active.
type ActivationReason string

const (
	ReasonActive                 ActivationReason = ""
	ReasonDisabled               ActivationReason = "disabled"
	ReasonEndpointAndKeyRequired ActivationReason = "endpoint_and_key_required"
	ReasonEndpointOnly           ActivationReason = "endpoint_only_no_key"
	ReasonKeyOnly                ActivationReason = "key_only_no_endpoint"
	ReasonProviderFlagOnly       ActivationReason = "provider_flag_only"
	ReasonEndpointInvalid        ActivationReason = "endpoint_invalid"
	ReasonEndpointNotApproved    ActivationReason = "endpoint_not_approved"
	ReasonCredentialInvalid      ActivationReason = "credential_invalid"
	ReasonConflictingEndpoints   ActivationReason = "conflicting_runtime_endpoints"
	ReasonUnsafeForced           ActivationReason = "unsafe_force_activation"
)

// RuntimeKind identifies which coding-agent runtime the activation targets.
type RuntimeKind string

const (
	RuntimeNone      RuntimeKind = ""
	RuntimeAnthropic RuntimeKind = "anthropic"     // ANTHROPIC_BASE_URL
	RuntimeOpenAI    RuntimeKind = "openai"        // OPENAI_BASE_URL
	RuntimeFreeInfer RuntimeKind = "freeinference" // FREEINFERENCE_BASE_URL
)

// CredentialSource identifies which environment variable supplied the
// runtime credential. Empty when no credential is present.
type CredentialSource string

const (
	CredNone                CredentialSource = ""
	CredFreeInferenceAPIKey CredentialSource = "FREEINFERENCE_API_KEY"
	CredAnthropicAPIKey     CredentialSource = "ANTHROPIC_API_KEY"
	CredOpenAIAPIKey        CredentialSource = "OPENAI_API_KEY"
)

// Activation is the authoritative activation result. Pass this object into
// adapters, refreshers, and CLI commands rather than re-reading env vars.
type Activation struct {
	// Active is true iff every hard-contract requirement passed.
	Active bool `json:"active"`
	// Disabled is true when FI_DISABLED=1 short-circuited evaluation.
	Disabled bool `json:"disabled"`
	// UnsafeForced is true when FI_UNSAFE_FORCE_ACTIVATION=1 overrode the gate.
	// Recorded so downstream code and audits can distinguish a real activation
	// from a dev override.
	UnsafeForced bool `json:"unsafe_forced,omitempty"`

	// EndpointPresent: an env var supplied a runtime endpoint.
	EndpointPresent bool `json:"endpoint_present"`
	// EndpointValid: the endpoint normalized successfully.
	EndpointValid bool `json:"endpoint_valid"`
	// KeyPresent: a supported runtime credential env var was non-empty.
	KeyPresent bool `json:"key_present"`

	// EndpointSource: the env var name that supplied the runtime endpoint.
	EndpointSource string `json:"endpoint_source,omitempty"`
	// CredentialSource: the env var name that supplied the credential.
	CredentialSource CredentialSource `json:"credential_source,omitempty"`
	// Origin: the sanitized scheme://host of the active endpoint (empty if not valid).
	Origin string `json:"origin,omitempty"`
	// RuntimeKind: which runtime matched (anthropic/openai/freeinference).
	RuntimeKind RuntimeKind `json:"runtime_kind,omitempty"`
	// InactiveReason: machine code from ActivationReason.
	InactiveReason ActivationReason `json:"inactive_reason,omitempty"`
	// ModelID: observed model (record-only — never affects activation).
	ModelID string `json:"model_id,omitempty"`
	// capturedCredential stores the credential value at evaluation time.
	// This ensures Identity() never re-reads environment variables.
	capturedCredential string `json:"-"`
}

// Identity is a stable, non-secret fingerprint of the active runtime. Two
// processes with the same Identity see the same provider-level state. The
// fingerprint incorporates the endpoint origin, runtime kind, and a salted
// HMAC of the credential so that switching the API key produces a different
// identity even on the same endpoint. Persist this in snapshots and the
// session index so rendering can require an exact current-runtime match.
type Identity struct {
	EndpointOrigin string      `json:"endpoint_origin"`
	RuntimeKind    RuntimeKind `json:"runtime_kind"`
	CredentialFP   string      `json:"credential_fp"`
}

// DirName returns a filesystem-safe identifier for this identity. It is
// derived from the endpoint origin, runtime kind, and credential fingerprint
// so that different endpoints or keys produce different state directories.
// Uses length-prefixed encoding to avoid ambiguity in concatenation.
func (id Identity) DirName() string {
	if id.EndpointOrigin == "" && id.RuntimeKind == "" && id.CredentialFP == "" {
		return ""
	}
	// Length-prefix each field to avoid concatenation ambiguity.
	// Format: <len1><field1><len2><field2><len3><field3>
	var buf []byte
	buf = appendLenPrefixed(buf, id.EndpointOrigin)
	buf = appendLenPrefixed(buf, string(id.RuntimeKind))
	buf = appendLenPrefixed(buf, id.CredentialFP)
	h := sha256.Sum256(buf)
	return hex.EncodeToString(h[:])[:16]
}

func appendLenPrefixed(buf []byte, s string) []byte {
	// 4-byte big-endian length prefix
	l := uint32(len(s))
	buf = append(buf, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
	buf = append(buf, s...)
	return buf
}

// EndpointCandidate is one runtime endpoint env var and its parsed identity.
type EndpointCandidate struct {
	Source      string
	RuntimeKind RuntimeKind
	Raw         string
	Identity    *api.EndpointIdentity
}

// Evaluate reads the process environment ONCE and returns the authoritative
// activation result. This is the single entry point — every layer should
// call this (or receive its result) rather than reading env vars directly.
func Evaluate() Activation {
	return EvaluateWithModel("")
}

// EvaluateWithModel is the model-aware variant. The model ID is recorded but
// never changes the activation decision: a validated endpoint+key pair is
// authoritative regardless of model prefix.
func EvaluateWithModel(modelID string) Activation {
	a := Activation{ModelID: modelID}

	if os.Getenv("FI_DISABLED") == "1" || os.Getenv("FI_RUNTIME_INACTIVE") == "1" {
		a.Disabled = true
		a.InactiveReason = ReasonDisabled
		return a
	}

	// Dev override. Recorded for audit; not used in production.
	if os.Getenv("FI_UNSAFE_FORCE_ACTIVATION") == "1" {
		a.UnsafeForced = true
		a.Active = true
		a.Origin = "unsafe://force-activation"
		a.RuntimeKind = RuntimeFreeInfer
		a.InactiveReason = ReasonUnsafeForced
		return a
	}

	// Collect all runtime endpoint candidates. The coding agent reads whichever
	// matches its protocol — Anthropic-compatible runtimes read ANTHROPIC_BASE_URL,
	// OpenAI-compatible read OPENAI_BASE_URL, and FREEINFERENCE_BASE_URL is the
	// companion-native canonical name.
	candidates := collectEndpointCandidates()
	a.EndpointPresent = len(candidates) > 0

	// Reject conflicting endpoints (more than one non-empty, or any non-FI host
	// alongside an FI one). The coding agent can only honor one runtime.
	fiCandidate, conflict := selectFIEndpoint(candidates)
	if conflict {
		a.EndpointValid = true // they may all normalize, but they disagree
		a.InactiveReason = ReasonConflictingEndpoints
		return a
	}

	// Validate the FI candidate's normalization.
	if fiCandidate != nil {
		a.EndpointValid = fiCandidate.Identity != nil
		if fiCandidate.Identity != nil {
			a.Origin = fiCandidate.Identity.Origin
			a.RuntimeKind = fiCandidate.RuntimeKind
			a.EndpointSource = fiCandidate.Source
		} else {
			// Normalization failed — record which source was malformed.
			a.EndpointSource = fiCandidate.Source
			a.RuntimeKind = fiCandidate.RuntimeKind
			a.InactiveReason = ReasonEndpointInvalid
			return a
		}
	}

	// Collect any supported runtime credential. FREEINFERENCE_API_KEY is the
	// canonical companion credential; ANTHROPIC_API_KEY / OPENAI_API_KEY are
	// accepted only when the active endpoint is an approved FreeInference host
	// of the matching runtime kind.
	credSrc, credPresent := detectCredential(a.RuntimeKind)
	a.KeyPresent = credPresent
	a.CredentialSource = credSrc

	// Hard-contract gate: endpoint AND credential required, both validated.
	switch {
	case !a.EndpointPresent && !a.KeyPresent:
		a.InactiveReason = ReasonEndpointAndKeyRequired
		return a
	case a.EndpointPresent && !a.KeyPresent:
		a.InactiveReason = ReasonEndpointOnly
		return a
	case a.KeyPresent && !a.EndpointPresent:
		// Provider flag alone does not activate.
		a.InactiveReason = ReasonKeyOnly
		return a
	}

	// Both present and endpoint valid → active.
	a.Active = true
	// Capture credential at evaluation time so Identity() never re-reads env.
	a.capturedCredential = a.rawCredential()
	return a
}

// collectEndpointCandidates returns every non-empty runtime endpoint env var
// alongside its normalized identity (or nil if it failed normalization).
// FI_PROVIDER is not an endpoint; it is attribution only.
func collectEndpointCandidates() []EndpointCandidate {
	sources := []struct {
		name string
		kind RuntimeKind
	}{
		{"FREEINFERENCE_BASE_URL", RuntimeFreeInfer},
		{"ANTHROPIC_BASE_URL", RuntimeAnthropic},
		{"OPENAI_BASE_URL", RuntimeOpenAI},
	}
	var out []EndpointCandidate
	for _, s := range sources {
		raw := os.Getenv(s.name)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		c := EndpointCandidate{Source: s.name, RuntimeKind: s.kind, Raw: raw}
		if id, err := api.NormalizeEndpoint(raw); err == nil {
			c.Identity = id
		}
		out = append(out, c)
	}
	return out
}

// selectFIEndpoint picks the single approved FreeInference endpoint from the
// candidate set, or returns (nil, true) if there is a conflict.
//
// A conflict is any of:
//   - two or more candidates have different normalized origins (scheme://host)
//   - any candidate points at a non-FreeInference host
//
// When ALL candidates have the SAME normalized FreeInference origin, pick by
// priority: FREEINFERENCE_BASE_URL > ANTHROPIC_BASE_URL > OPENAI_BASE_URL.
func selectFIEndpoint(cands []EndpointCandidate) (*EndpointCandidate, bool) {
	if len(cands) == 0 {
		return nil, false
	}

	// Normalize all candidates and collect their origins.
	type normalizedCandidate struct {
		Candidate EndpointCandidate
		Origin    string
		IsFI      bool
	}
	var normalized []normalizedCandidate
	for _, c := range cands {
		nc := normalizedCandidate{Candidate: c}
		if c.Identity != nil {
			nc.Origin = c.Identity.Origin
			nc.IsFI = c.Identity.IsFI
		}
		normalized = append(normalized, nc)
	}

	// If multiple candidates exist, check for origin agreement.
	if len(normalized) > 1 {
		firstOrigin := normalized[0].Origin
		firstIsFI := normalized[0].IsFI
		allAgree := true
		for _, nc := range normalized[1:] {
			if nc.Origin != firstOrigin || nc.IsFI != firstIsFI {
				allAgree = false
				break
			}
		}
		if !allAgree {
			// Multiple candidates with different origins or FI status → conflict.
			return nil, true
		}
		// All agree on the same origin: pick by priority, but only if it's an FI host.
		// Multiple non-FI candidates with the same origin is still a conflict —
		// we must not route FREEINFERENCE_API_KEY to a non-FI host.
		priority := []string{"FREEINFERENCE_BASE_URL", "ANTHROPIC_BASE_URL", "OPENAI_BASE_URL"}
		for _, src := range priority {
			for i, nc := range normalized {
				if nc.Candidate.Source == src {
					if nc.Candidate.Identity != nil && nc.Candidate.Identity.IsFI {
						return &normalized[i].Candidate, false
					}
					// Candidate exists but is not an FI host → conflict.
					return &normalized[i].Candidate, true
				}
			}
		}
		// Fallback: first candidate (all have same origin).
		if normalized[0].Candidate.Identity != nil && normalized[0].Candidate.Identity.IsFI {
			return &normalized[0].Candidate, false
		}
		return &normalized[0].Candidate, true
	}

	// Single candidate.
	c := &normalized[0].Candidate
	if c.Identity == nil {
		// Malformed single endpoint — not a conflict, just invalid.
		return c, false
	}
	if !c.Identity.IsFI {
		// Non-FreeInference host. This is a conflict because the credential
		// would leak off-host; treat as inactive rather than risk routing
		// the FREEINFERENCE_API_KEY elsewhere.
		return c, true
	}
	return c, false
}

// detectCredential finds the supported runtime credential for the active kind.
// FREEINFERENCE_API_KEY is the canonical companion credential. ANTHROPIC_API_KEY
// and OPENAI_API_KEY are accepted only when the active runtime kind matches.
// This prevents a generic API key from being treated as FreeInference creds
// unless the endpoint is already confirmed.
func detectCredential(kind RuntimeKind) (CredentialSource, bool) {
	if k := os.Getenv("FREEINFERENCE_API_KEY"); k != "" {
		return CredFreeInferenceAPIKey, true
	}
	switch kind {
	case RuntimeAnthropic:
		if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
			return CredAnthropicAPIKey, true
		}
	case RuntimeOpenAI:
		if k := os.Getenv("OPENAI_API_KEY"); k != "" {
			return CredOpenAIAPIKey, true
		}
	}
	return CredNone, false
}

// IsFreeInferenceHost reports whether host is one of the approved FreeInference
// hostnames that may receive a runtime credential. Exposed so other layers
// (api client, install) share one definition.
func IsFreeInferenceHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if host == "freeinference.org" {
		return true
	}
	return strings.HasSuffix(host, ".freeinference.org")
}

// ProviderInfo converts an activation result into the persisted schema form.
// Origin is the sanitized scheme://host only — never the raw env value.
func (a Activation) ProviderInfo() schema.ProviderInfo {
	name := schema.ProviderUnknown
	if a.Active {
		name = schema.ProviderFreeInference
	}
	source := a.EndpointSource
	if source == "" {
		source = string(a.CredentialSource)
	}
	if source == "" {
		source = "unresolved"
	}
	return schema.ProviderInfo{
		Name:      name,
		Confirmed: a.Active,
		Source:    source,
		BaseURL:   a.Origin,
	}
}

// ErrSaltUnavailable is returned when the installation salt cannot be read or
// created. Callers should treat this as fail-closed (no identity derived).
var ErrSaltUnavailable = errors.New("installation salt unavailable")

// Identity returns the activation identity, or (zero, error) if no active
// runtime or the credential salt is unavailable. The credential fingerprint
// is HMAC-SHA256(credential, installation-salt) truncated to 16 bytes — never
// the raw key, never a plain unsalted hash. The salt is loaded (or created)
// lazily from disk with 0600 permissions.
//
// The credential fingerprint is derived from the credential captured during
// evaluation (not re-read from environment), ensuring the "evaluate once"
// contract is maintained.
func (a Activation) Identity(saltLoader SaltLoader) (Identity, error) {
	if !a.Active {
		return Identity{}, errors.New("identity unavailable: runtime not active")
	}
	cred := a.capturedCredential
	if cred == "" {
		return Identity{}, errors.New("identity unavailable: no credential")
	}
	salt, err := saltLoader()
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrSaltUnavailable, err)
	}
	return Identity{
		EndpointOrigin: a.Origin,
		RuntimeKind:    a.RuntimeKind,
		CredentialFP:   credentialFingerprint(cred, salt),
	}, nil
}

// rawCredential reads the active credential from the env. Internal — used
// only to derive the fingerprint. Never logged.
func (a Activation) rawCredential() string {
	switch a.CredentialSource {
	case CredFreeInferenceAPIKey:
		return os.Getenv("FREEINFERENCE_API_KEY")
	case CredAnthropicAPIKey:
		return os.Getenv("ANTHROPIC_API_KEY")
	case CredOpenAIAPIKey:
		return os.Getenv("OPENAI_API_KEY")
	}
	return ""
}
