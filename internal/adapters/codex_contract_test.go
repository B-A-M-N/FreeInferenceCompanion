package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexHookVendorFixturesRemainAdditiveAndPromptFree(t *testing.T) {
	a := NewCodexAdapter(testPaths(t))
	fixtures, err := filepath.Glob(filepath.Join("testdata", "codex-hooks", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 7 {
		t.Fatalf("fixture count = %d, want at least 7", len(fixtures))
	}
	for _, fixture := range fixtures {
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		input, err := a.ParseHookInput(strings.NewReader(string(data)))
		if err != nil {
			t.Fatalf("parse %s: %v", fixture, err)
		}
		if input.SessionID == "" || input.HookEventName == "" {
			t.Fatalf("fixture %s missing required lifecycle fields: %+v", fixture, input)
		}
		if filepath.Base(fixture) == "user-prompt-submit.json" && input.Prompt == "" {
			t.Fatal("prompt fixture did not retain input prompt for dispatch")
		}
	}

	// Keep the fixture itself valid JSON as well as valid for the flat parser;
	// additive vendor fields are intentionally ignored by the Go decoder.
	var raw map[string]any
	data, err := os.ReadFile(filepath.Join("testdata", "codex-hooks", "user-prompt-submit.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["prompt"] != "must never be persisted" {
		t.Fatal("prompt fixture sanity check failed")
	}
}
