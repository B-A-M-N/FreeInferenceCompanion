package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAttributionSetJSONEmitsOnlyOneDocument(t *testing.T) {
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	var stdout, stderr strings.Builder
	if code := cmdAttribution([]string{"set", " APPEND ", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("set code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("complete stdout is not one JSON document: %q: %v", stdout.String(), err)
	}
	if payload["commit_mode"] != "append" {
		t.Fatalf("canonical mode = %#v", payload)
	}
}

func TestIntegrationsDiscoverJSONUsesStableSnakeCase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	var stdout, stderr strings.Builder
	if code := cmdIntegrations([]string{"discover", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("discover code=%d stderr=%q", code, stderr.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("discover JSON: %v", err)
	}
	for _, item := range payload {
		for _, key := range []string{"client", "config_root", "source"} {
			if _, ok := item[key]; !ok {
				t.Fatalf("missing stable field %q in %#v", key, item)
			}
		}
	}
}
