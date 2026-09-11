package clientenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
)

// CodexModelCapabilities reports whether a generated model catalog allows
// Codex to surface plugin and skill usage instructions.
type CodexModelCapabilities struct {
	Path           string   `json:"path"`
	Models         int      `json:"models"`
	SelectedModel  string   `json:"selected_model,omitempty"`
	Scope          string   `json:"scope"`
	SkillsAllowed  bool     `json:"skills_allowed"`
	PluginsAllowed bool     `json:"plugins_allowed"`
	AppsAllowed    bool     `json:"apps_allowed"`
	Blockers       []string `json:"blockers,omitempty"`
}

// InspectCodexModelCatalog reads <CODEX_HOME>/models.json read-only and
// reports the strictest capability settings across all entries. A missing
// catalog is not a blocker: native default catalogs may expose plugins.
func InspectCodexModelCatalog(codexHome string) (CodexModelCapabilities, error) {
	return inspectCodexModelCatalog(codexHome, "")
}

// InspectCodexModelCatalogForModel evaluates one selected model when the
// caller knows which model the Codex session will use. Without a selection,
// InspectCodexModelCatalog intentionally remains conservative and evaluates
// every catalog entry.
func InspectCodexModelCatalogForModel(codexHome, modelID string) (CodexModelCapabilities, error) {
	return inspectCodexModelCatalog(codexHome, strings.TrimSpace(modelID))
}

func inspectCodexModelCatalog(codexHome, selectedModel string) (CodexModelCapabilities, error) {
	path := filepath.Join(codexHome, "models.json")
	result := CodexModelCapabilities{Path: path, Scope: "all", SkillsAllowed: true, PluginsAllowed: true, AppsAllowed: true}
	if selectedModel != "" {
		result.SelectedModel = selectedModel
		result.Scope = "selected"
	}
	failClosed := func(err error) (CodexModelCapabilities, error) {
		result.SkillsAllowed, result.PluginsAllowed, result.AppsAllowed = false, false, false
		return result, err
	}
	f, err := config.OpenNoFollow(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return failClosed(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return failClosed(err)
	}
	if !info.Mode().IsRegular() {
		return failClosed(errors.New("model catalog is not a regular file"))
	}
	data, err := io.ReadAll(io.LimitReader(f, 2<<20+1))
	if err != nil {
		return failClosed(err)
	}
	if len(data) > 2<<20 {
		return failClosed(errors.New("model catalog exceeds the supported size limit"))
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
		return failClosed(errors.New("model catalog is invalid"))
	}
	result.Models = len(catalog.Models)
	matched := selectedModel == ""
	for _, model := range catalog.Models {
		if selectedModel != "" && model.ID != selectedModel {
			continue
		}
		matched = true
		if model.IncludeSkillsUsageInstructions != nil && !*model.IncludeSkillsUsageInstructions {
			result.SkillsAllowed = false
		}
		if model.IncludePluginUsageInstructions != nil && !*model.IncludePluginUsageInstructions {
			result.PluginsAllowed = false
		}
		if model.IncludeAppsUsageInstructions != nil && !*model.IncludeAppsUsageInstructions {
			result.AppsAllowed = false
		}
	}
	if !matched {
		return failClosed(fmt.Errorf("model %q is not present in the Codex catalog", selectedModel))
	}
	if !result.SkillsAllowed {
		result.Blockers = append(result.Blockers, "include_skills_usage_instructions=false")
	}
	if !result.PluginsAllowed {
		result.Blockers = append(result.Blockers, "include_plugin_usage_instructions=false")
	}
	if !result.AppsAllowed {
		result.Blockers = append(result.Blockers, "include_apps_usage_instructions=false")
	}
	return result, nil
}
