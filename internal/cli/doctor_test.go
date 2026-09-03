package cli

import "testing"

func TestValidCodexHookDefinitionParsesCommandEntries(t *testing.T) {
	valid := []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"\"${PLUGIN_ROOT}/scripts/run-hook.sh\" SessionStart"}]}]}}`)
	if !validCodexHookDefinition(valid) {
		t.Fatal("valid Codex command hook was rejected")
	}
	for _, invalid := range [][]byte{
		[]byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"broken"}]}]}}`),
		[]byte("{\"hooks\":{\"SessionStart\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo \\\"${PLUGIN_ROOT}/scripts/run-hook.sh\\\"\"}]}]}}"),
		[]byte("{\"hooks\":{\"SessionStart\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"\\\"${PLUGIN_ROOT}/scripts/run-hook.sh-extra\\\" SessionStart\"}]}]}}"),
		[]byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"description","value":"${PLUGIN_ROOT}/scripts/run-hook.sh"}]}]}}`),
		[]byte(`{"hooks":`),
	} {
		if validCodexHookDefinition(invalid) {
			t.Fatalf("invalid Codex hook definition was accepted: %s", invalid)
		}
	}
}
