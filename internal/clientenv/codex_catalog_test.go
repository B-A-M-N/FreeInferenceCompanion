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
	if len(caps.Blockers) != 3 || !strings.Contains(strings.Join(caps.Blockers, ","), "skills") || !strings.Contains(strings.Join(caps.Blockers, ","), "plugin") || !strings.Contains(strings.Join(caps.Blockers, ","), "apps") {
		t.Fatalf("blockers=%+v", caps.Blockers)
	}
}

func TestInspectCodexModelCatalogMissingIsPermissive(t *testing.T) {
	caps, err := InspectCodexModelCatalog(t.TempDir())
	if err != nil || !caps.SkillsAllowed || !caps.PluginsAllowed || caps.Models != 0 {
		t.Fatalf("capabilities=%+v err=%v", caps, err)
	}
}

func TestInspectCodexModelCatalogForSelectedModelDoesNotUseGlobalStrictest(t *testing.T) {
	home := t.TempDir()
	catalog := `{"models":[
		{"id":"permissive","include_skills_usage_instructions":true,"include_plugin_usage_instructions":true,"include_apps_usage_instructions":true},
		{"id":"restricted","include_skills_usage_instructions":false,"include_plugin_usage_instructions":false,"include_apps_usage_instructions":false}
	]}`
	if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(catalog), 0600); err != nil {
		t.Fatal(err)
	}
	caps, err := InspectCodexModelCatalogForModel(home, "permissive")
	if err != nil || caps.Scope != "selected" || caps.SelectedModel != "permissive" || !caps.SkillsAllowed || !caps.PluginsAllowed || !caps.AppsAllowed {
		t.Fatalf("permissive selected capabilities=%+v err=%v", caps, err)
	}
	caps, err = InspectCodexModelCatalogForModel(home, "restricted")
	if err != nil || caps.SkillsAllowed || caps.PluginsAllowed || caps.AppsAllowed {
		t.Fatalf("restricted selected capabilities=%+v err=%v", caps, err)
	}
	if _, err := InspectCodexModelCatalogForModel(home, "missing"); err == nil {
		t.Fatal("unknown selected model was accepted")
	}
}

func TestInspectCodexModelCatalogFailsClosedForMalformedBreadth(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "models.json")
	for name, contents := range map[string]string{
		"wrong field type": `{"models":[{"id":"x","include_plugin_usage_instructions":"no"}]}`,
		"invalid json":     `{"models":[`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			caps, err := InspectCodexModelCatalog(home)
			if err == nil || caps.SkillsAllowed || caps.PluginsAllowed || caps.AppsAllowed {
				t.Fatalf("malformed catalog capabilities=%+v err=%v", caps, err)
			}
		})
	}
	if err := os.WriteFile(path, []byte(`{"models":`+strings.Repeat(" ", 2<<20)+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	if caps, err := InspectCodexModelCatalog(home); err == nil || caps.SkillsAllowed || caps.PluginsAllowed || caps.AppsAllowed {
		t.Fatalf("oversized catalog capabilities=%+v err=%v", caps, err)
	}
}
