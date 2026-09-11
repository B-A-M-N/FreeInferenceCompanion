package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

func TestHandlePreToolUseUsesEffectiveAttributionResolution(t *testing.T) {
	input := map[string]any{
		"tool_name": "Bash",
		"tool_input": map[string]any{
			"command": `git commit -m "fix"`,
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	run := func(env string) string {
		t.Setenv("FI_CONFIG_DIR", t.TempDir())
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Attribution.CommitMode = "standalone"
		if err := config.Save(cfg); err != nil {
			t.Fatal(err)
		}
		// t.Setenv registers cleanup for every branch, including the disabled
		// case; never unset process environment state without restoration.
		t.Setenv("FI_ATTRIBUTION_COMMIT_MODE", env)
		var stdout strings.Builder
		handlePreToolUse("claude-code", runtime.ClientClaudeCode, strings.NewReader(string(raw)), &stdout, runtime.Activation{Active: true})
		return stdout.String()
	}
	if run("") == "" {
		t.Fatal("config standalone mode was ignored")
	}
	if run("standalone") == "" {
		t.Fatal("environment standalone override ignored")
	}
	if run("OFF") != "" {
		t.Fatal("case-insensitive environment off enabled attribution")
	}
	if run("invalid") != "" {
		t.Fatal("invalid environment enabled attribution")
	}
}

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
	var inputPayload struct {
		ToolInput map[string]json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &inputPayload); err != nil {
		t.Fatal(err)
	}
	for key, want := range inputPayload.ToolInput {
		if key == "command" {
			continue
		}
		got, ok := output.UpdatedInput[key]
		if !ok {
			t.Fatalf("updatedInput dropped %q", key)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("updatedInput changed %q: got %s want %s", key, got, want)
		}
	}
}
