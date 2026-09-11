package clientenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectCodexModelCatalogDetectsBlockedCapabilities(t *testing.T) {
	home := t.TempDir()
	catalog := `{"models":[
		{"id":"a","include_skills_usage_instructions":true,"include_plugin_usage_instructions":true},
		{"id":"b","include_skills_usage_instructions":false,"include_plugin_usage_instructions":false,"include_apps_usage_instructions":false}
	]}`
	if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(catalog), 0600); err != nil {
		t.Fatal(err)
	}
	caps, err := InspectCodexModelCatalog(home)
	if err != nil || caps.Models != 2 || caps.SkillsAllowed || caps.PluginsAllowed || caps.AppsAllowed {
		t.Fatalf("capabilities=%+v err=%v", caps, err)
	}
	if len(caps.Blockers) != 2 || !strings.Contains(strings.Join(caps.Blockers, ","), "skills") || !strings.Contains(strings.Join(caps.Blockers, ","), "plugin") {
		t.Fatalf("blockers=%+v", caps.Blockers)
	}
}

func TestInspectCodexModelCatalogMissingIsPermissive(t *testing.T) {
	caps, err := InspectCodexModelCatalog(t.TempDir())
	if err != nil || !caps.SkillsAllowed || !caps.PluginsAllowed || caps.Models != 0 {
		t.Fatalf("capabilities=%+v err=%v", caps, err)
	}
}
