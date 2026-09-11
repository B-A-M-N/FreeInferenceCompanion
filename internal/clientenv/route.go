package clientenv

import (
	"encoding/json"
	"path/filepath"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
)

// SelectsFreeInferenceCodexRoute reports whether the effective Codex provider
// uses the approved /v1 runtime route.
func SelectsFreeInferenceCodexRoute(rawURL string) bool {
	return api.IsFIRoute(rawURL, api.CodexRoutePath)
}

// SelectsFreeInferenceClaudeRoute reports whether the effective Claude route
// uses the supported Anthropic-compatible /anthropic runtime path.
func SelectsFreeInferenceClaudeRoute(rawURL string) bool {
	return api.IsFIRoute(rawURL, api.ClaudeRoutePath)
}

// IsFreeInferenceClaudeSettings inspects the actual selected Claude endpoint.
// Malformed or unreadable configuration fails closed.
func IsFreeInferenceClaudeSettings(path string) bool {
	baseURL, ok := claudeSettingsBaseURL(path)
	return ok && SelectsFreeInferenceClaudeRoute(baseURL)
}

// IsLegacyFreeInferenceClaudeSettings reports the pre-integration Claude route
// so discovery can explain why an otherwise recognizable profile was skipped.
func IsLegacyFreeInferenceClaudeSettings(path string) bool {
	baseURL, ok := claudeSettingsBaseURL(path)
	return ok && IsLegacyFreeInferenceClaudeRoute(baseURL)
}

// IsLegacyFreeInferenceClaudeRoute reports the former OpenAI-compatible route
// that Claude profiles used before the Anthropic-compatible endpoint existed.
func IsLegacyFreeInferenceClaudeRoute(rawURL string) bool {
	return api.IsFIRoute(rawURL, api.CodexRoutePath)
}

func claudeSettingsBaseURL(path string) (string, bool) {
	data, err := readBoundedNoFollow(path)
	if err != nil {
		return "", false
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return "", false
	}
	return settings.Env["ANTHROPIC_BASE_URL"], true
}

// IsFreeInferenceCodexConfig inspects the effective selected Codex provider.
// It uses BurntSushi/toml and fails closed for malformed or unreadable files.
func IsFreeInferenceCodexConfig(path string) bool {
	data, err := readBoundedNoFollow(path)
	if err != nil {
		return false
	}
	_, baseURL, ok := parseSelectedCodexProvider(string(data))
	if !ok {
		return false
	}
	return SelectsFreeInferenceCodexRoute(baseURL)
}

// DiagnoseClaudeRoute reports a simple stable state for CLI diagnostics. It
// deliberately avoids duplicating route-policy parsing.
func DiagnoseClaudeRoute(root string) (string, error) {
	if looksLikeClaudeSettings(filepath.Join(root, "settings.json")) {
		return "verified", nil
	}
	if IsLegacyFreeInferenceClaudeSettings(filepath.Join(root, "settings.json")) {
		return "legacy-v1", nil
	}
	return "unverified", nil
}
