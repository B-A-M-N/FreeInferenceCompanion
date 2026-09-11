package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

func TestHandlePreToolUsePreservesAdditionalInputAndIsStateless(t *testing.T) {
	input := map[string]any{
		"session_id":  "s",
		"tool_name":   "Bash",
		"tool_use_id": "u",
		"model":       "test-model",
		"tool_input": map[string]any{
			"command":           `git commit -m "fix"`,
			"description":       "commit work",
			"timeout":           30,
			"run_in_background": false,
			"future":            map[string]any{"keep": true},
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Attribution.CommitMode = "standalone"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	handlePreToolUse("claude-code", runtime.ClientClaudeCode, strings.NewReader(string(raw)), &stdout, runtime.Activation{Active: true})
	if stdout.String() == "" {
		t.Fatal("expected updatedInput")
	}
	var output struct {
		UpdatedInput map[string]json.RawMessage `json:"updatedInput"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	commandRaw, ok := output.UpdatedInput["command"]
	if !ok {
		t.Fatalf("updatedInput missing command: %s", stdout.String())
	}
	var command string
	if err := json.Unmarshal(commandRaw, &command); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "Support-FreeInference") {
		t.Fatalf("attribution missing: %s", command)
	}
	var extra struct {
		Description     string         `json:"description"`
		Timeout         int            `json:"timeout"`
		RunInBackground bool           `json:"run_in_background"`
		Future          map[string]any `json:"future"`
	}
	fixtureToolInput, err := json.Marshal(input["tool_input"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixtureToolInput, &extra); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"description", "timeout", "run_in_background", "future"} {
		if _, ok := output.UpdatedInput[key]; !ok {
			t.Fatalf("updatedInput lost field %s: %s", key, stdout.String())
		}
	}
	if extra.Description != "commit work" || extra.Timeout != 30 || extra.RunInBackground || extra.Future["keep"] != true {
		t.Fatalf("input fixture lost fields: %#v", extra)
	}
}
