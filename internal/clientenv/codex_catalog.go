package clientenv

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
)

// CodexModelCapabilities reports whether a generated model catalog allows
// Codex to surface plugin and skill usage instructions.
type CodexModelCapabilities struct {
	Path           string   `json:"path"`
	Models         int      `json:"models"`
	SkillsAllowed  bool     `json:"skills_allowed"`
	PluginsAllowed bool     `json:"plugins_allowed"`
	AppsAllowed    bool     `json:"apps_allowed"`
	Blockers       []string `json:"blockers,omitempty"`
}

// InspectCodexModelCatalog reads <CODEX_HOME>/models.json read-only and
// reports the strictest capability settings across all entries. A missing
// catalog is not a blocker: native default catalogs may expose plugins.
func InspectCodexModelCatalog(codexHome string) (CodexModelCapabilities, error) {
	path := filepath.Join(codexHome, "models.json")
	result := CodexModelCapabilities{Path: path, SkillsAllowed: true, PluginsAllowed: true}
	f, err := config.OpenNoFollow(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("model catalog is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 2<<20+1))
	if err != nil {
		return result, err
	}
	if len(data) > 2<<20 {
		return result, errors.New("model catalog exceeds the supported size limit")
	}
	var catalog struct {
		Models []struct {
			ID                             string `json:"id"`
			IncludeSkillsUsageInstructions *bool  `json:"include_skills_usage_instructions"`
			IncludePluginUsageInstructions *bool  `json:"include_plugin_usage_instructions"`
			IncludeAppsUsageInstructions   *bool  `json:"include_apps_usage_instructions"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return result, errors.New("model catalog is invalid")
	}
	result.Models = len(catalog.Models)
	for _, model := range catalog.Models {
		if model.IncludeSkillsUsageInstructions != nil && !*model.IncludeSkillsUsageInstructions {
			result.SkillsAllowed = false
		}
		if model.IncludePluginUsageInstructions != nil && !*model.IncludePluginUsageInstructions {
			result.PluginsAllowed = false
		}
		if model.IncludeAppsUsageInstructions != nil {
			result.AppsAllowed = result.AppsAllowed || *model.IncludeAppsUsageInstructions
		}
	}
	if !result.SkillsAllowed {
		result.Blockers = append(result.Blockers, "include_skills_usage_instructions=false")
	}
	if !result.PluginsAllowed {
		result.Blockers = append(result.Blockers, "include_plugin_usage_instructions=false")
	}
	return result, nil
}
