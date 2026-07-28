package adapters

import (
	"net/url"
	"os"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ProviderDetection is the structured result of provider detection.
type ProviderDetection struct {
	Name      string `json:"name"` // "freeinference" or "unknown"
	Confirmed bool   `json:"confirmed"`
	Source    string `json:"source"` // mechanism that produced the result
	BaseURL   string `json:"base_url,omitempty"`
}

// ToProviderInfo converts a detection result into the persisted schema form.
func (d ProviderDetection) ToProviderInfo() schema.ProviderInfo {
	return schema.ProviderInfo{
		Name:      d.Name,
		Confirmed: d.Confirmed,
		Source:    d.Source,
		BaseURL:   d.BaseURL,
	}
}

// freeInferenceDomain is the canonical FreeInference hostname.
const freeInferenceDomain = "freeinference.org"

// IsFreeInferenceURL reports whether rawURL points at a FreeInference host.
// Uses proper URL parsing rather than substring matching.
func IsFreeInferenceURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	host = strings.ToLower(host)
	return host == freeInferenceDomain || strings.HasSuffix(host, "."+freeInferenceDomain)
}

// isFreeInferenceURL is the internal lowercase variant for backward compat.
func isFreeInferenceURL(rawURL string) bool {
	return IsFreeInferenceURL(rawURL)
}

// DetectProvider determines whether the current environment is configured
// to talk to FreeInference. Detection order:
//
//  1. Explicit FI_PROVIDER=freeinference
//  2. FREEINFERENCE_BASE_URL pointing at a FreeInference host
//  3. ANTHROPIC_BASE_URL pointing at a FreeInference host
//  4. OPENAI_BASE_URL pointing at a FreeInference host
//  5. FREEINFERENCE_API_KEY with no conflicting provider configuration
//  6. Otherwise unknown
func DetectProvider() ProviderDetection {
	// 1. Explicit override
	if strings.EqualFold(os.Getenv("FI_PROVIDER"), schema.ProviderFreeInference) {
		return ProviderDetection{
			Name:      schema.ProviderFreeInference,
			Confirmed: true,
			Source:    "FI_PROVIDER",
		}
	}

	// 2-4. URL-based detection
	candidates := []struct {
		source string
		value  string
	}{
		{"FREEINFERENCE_BASE_URL", os.Getenv("FREEINFERENCE_BASE_URL")},
		{"ANTHROPIC_BASE_URL", os.Getenv("ANTHROPIC_BASE_URL")},
		{"OPENAI_BASE_URL", os.Getenv("OPENAI_BASE_URL")},
	}
	for _, candidate := range candidates {
		if isFreeInferenceURL(candidate.value) {
			return ProviderDetection{
				Name:      schema.ProviderFreeInference,
				Confirmed: true,
				Source:    candidate.source,
				BaseURL:   candidate.value,
			}
		}
	}

	// 5. API key with no conflicting provider configuration.
	// A conflicting configuration is an explicit base URL pointing elsewhere.
	// Include FREEINFERENCE_BASE_URL in the conflict check — otherwise
	// FREEINFERENCE_BASE_URL=https://attacker.example with FREEINFERENCE_API_KEY
	// would still be classified as confirmed FreeInference.
	conflict := false
	for _, env := range []string{"FREEINFERENCE_BASE_URL", "ANTHROPIC_BASE_URL", "OPENAI_BASE_URL"} {
		v := os.Getenv(env)
		if v != "" && !isFreeInferenceURL(v) {
			conflict = true
			break
		}
	}
	if !conflict && os.Getenv("FREEINFERENCE_API_KEY") != "" {
		return ProviderDetection{
			Name:      schema.ProviderFreeInference,
			Confirmed: true,
			Source:    "FREEINFERENCE_API_KEY",
		}
	}

	return ProviderDetection{
		Name:      schema.ProviderUnknown,
		Confirmed: false,
		Source:    "unresolved",
	}
}

// IsConfirmedFreeInference reports whether the persisted provider info
// authorizes FreeInference-specific warnings and health indicators.
func IsConfirmedFreeInference(p schema.ProviderInfo) bool {
	return p.Confirmed && p.Name == schema.ProviderFreeInference
}
