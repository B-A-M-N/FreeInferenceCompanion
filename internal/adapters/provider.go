package adapters

import (
	"os"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ProviderDetection is the structured result of provider detection.
//
// DEPRECATED in favor of runtime.Activation. Retained as a thin adapter so
// existing call sites compile during the migration; new code should take a
// runtime.Activation parameter directly. The Confirmed field here is derived
// from runtime.Activation.Active — it does not implement the old permissive
// endpoint-only or key-only rules. Those were incorrect: see P0-1.
type ProviderDetection struct {
	Name      string `json:"name"` // "freeinference" or "unknown"
	Confirmed bool   `json:"confirmed"`
	Source    string `json:"source"` // mechanism that produced the result
	BaseURL   string `json:"base_url,omitempty"`
}

// ToProviderInfo converts a detection result into the persisted schema form.
// BaseURL is the sanitized origin (scheme://host) only — never the raw
// environment value, which may carry userinfo, query strings, or fragments.
func (d ProviderDetection) ToProviderInfo() schema.ProviderInfo {
	return schema.ProviderInfo{
		Name:      d.Name,
		Confirmed: d.Confirmed,
		Source:    d.Source,
		BaseURL:   d.BaseURL,
	}
}

// IsFreeInferenceURL reports whether rawURL points at a FreeInference host.
// Uses the shared endpoint normalizer so the same rules (reject userinfo,
// fragments, invalid schemes) apply everywhere.
func IsFreeInferenceURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	id, err := api.NormalizeEndpoint(rawURL)
	if err != nil {
		return false
	}
	return id.IsFI
}

// DetectProvider is the legacy entry point. It delegates to
// runtime.Evaluate() so all callers share the strict activation contract:
// endpoint AND key AND approved host required, no model-prefix blacklist.
//
// DEPRECATED: pass runtime.Activation into your function instead of calling
// this. The function is retained for the few call sites that have not yet
// been migrated.
func DetectProvider() ProviderDetection {
	return detectionFromActivation(runtime.Evaluate())
}

// DetectProviderForModel is the legacy model-aware entry point. The model ID
// is recorded but never affects activation — a validated endpoint+key pair is
// authoritative. FreeInference deployments may legitimately serve models with
// vendor prefixes (deepseek-, llama-, mistral-, etc.) and the previous
// blacklist would have mis-classified them.
//
// DEPRECATED: pass runtime.Activation into your function instead.
func DetectProviderForModel(modelID string) ProviderDetection {
	return detectionFromActivation(runtime.EvaluateWithModel(modelID))
}

func detectionFromActivation(a runtime.Activation) ProviderDetection {
	d := ProviderDetection{
		Name:      schema.ProviderUnknown,
		Confirmed: a.Active,
		Source:    string(a.InactiveReason),
		BaseURL:   a.Origin,
	}
	if a.Active {
		d.Name = schema.ProviderFreeInference
	}
	if a.EndpointSource != "" {
		d.Source = a.EndpointSource
	}
	if d.Source == "" {
		d.Source = string(a.CredentialSource)
	}
	if d.Source == "" {
		d.Source = "unresolved"
	}
	return d
}

// IsConfirmedFreeInference reports whether the persisted provider info
// authorizes FreeInference-specific warnings and health indicators.
func IsConfirmedFreeInference(p schema.ProviderInfo) bool {
	return p.Confirmed && p.Name == schema.ProviderFreeInference
}

// ActivationFromEnv is the canonical helper for layers that have not yet been
// refactored to receive a runtime.Activation from the CLI dispatcher. New
// code should accept a runtime.Activation parameter instead.
func ActivationFromEnv() runtime.Activation {
	return runtime.Evaluate()
}

// IsFreeInferenceHost is a re-export for packages that historically imported
// the test from adapters.
func IsFreeInferenceHost(host string) bool {
	return runtime.IsFreeInferenceHost(host)
}

// suppress unused-import warnings while preserving api.NormalizeEndpoint use.
var _ = strings.EqualFold
var _ = os.Getenv
