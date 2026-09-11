package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
)

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

func TestIntegrationsHelpAndUnknownCommand(t *testing.T) {
	var out, errOut strings.Builder
	if code := cmdIntegrations([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "freeinference integrations list") {
		t.Fatalf("help code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := cmdIntegrations([]string{"bogus"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unknown integrations command") {
		t.Fatalf("unknown code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestDoctorReportsDiscoveredEnvironmentIntegrations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	claudeRoot := filepath.Join(home, ".config", "claude-code", "fi-profile-a")
	if err := os.MkdirAll(filepath.Join(claudeRoot, "plugins", "freeinference-companion", ".claude-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(claudeRoot, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://freeinference.org/v1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	pluginRoot := filepath.Join(claudeRoot, "plugins", "freeinference-companion")
	if err := os.WriteFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), []byte(`{"name":"freeinference-companion"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "hooks.json"), []byte(`{"hooks":{"SessionStart":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "scripts", "run-hook.sh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	checks := checkClientEnvironmentIntegrations()
	foundPass := false
	foundCanonicalUnknown := false
	for _, check := range checks {
		if strings.Contains(check.name, claudeRoot) && check.result.State == api.CheckPass && strings.Contains(check.result.Detail, "hooks and executable runner installed") {
			foundPass = true
		}
		if strings.Contains(check.name, filepath.Join(home, ".codex")) && check.result.State == api.CheckUnknown {
			foundCanonicalUnknown = true
		}
	}
	if !foundPass || !foundCanonicalUnknown {
		t.Fatalf("environment checks = %+v", checks)
	}
}

func TestIntegrationsAddParsesAndValidatesArguments(t *testing.T) {
	var out, errOut strings.Builder
	if code := cmdIntegrations([]string{"add"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "--client and --root") {
		t.Fatalf("missing args code=%d err=%q", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := cmdIntegrations([]string{"add", "--client", "bogus", "--root", "/tmp/x"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "unsupported client") {
		t.Fatalf("bad client code=%d err=%q", code, errOut.String())
	}
}

func TestIntegrationsRemoveParsesArguments(t *testing.T) {
	var out, errOut strings.Builder
	if code := cmdIntegrations([]string{"remove"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "--client and --root") {
		t.Fatalf("missing args code=%d err=%q", code, errOut.String())
	}
}

func TestDoctorDecomposesCodexEnvironmentChecks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	codexRoot := filepath.Join(home, ".custom-codex")
	config := "model_provider = \"fi\"\n\n[model_providers.fi]\nbase_url = \"https://freeinference.org/v1\"\n"
	if err := os.MkdirAll(codexRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexRoot, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	pluginRoot := filepath.Join(codexRoot, "plugins", "freeinference-companion")
	for _, dir := range []string{filepath.Join(pluginRoot, ".codex-plugin"), filepath.Join(pluginRoot, "hooks"), filepath.Join(pluginRoot, "scripts")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"freeinference-companion"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "hooks.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"${PLUGIN_ROOT}/scripts/run-hook.sh SessionStart"}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "scripts", "run-hook.sh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	checks := checkClientEnvironmentIntegrations()
	var plugin, registration, hook, feature bool
	for _, check := range checkNamesContaining(checks, codexRoot) {
		switch {
		case strings.HasSuffix(check.name, "Codex environment "+codexRoot) && check.result.State == api.CheckPass:
			plugin = true
		case strings.HasSuffix(check.name, " registration") && check.result.State == api.CheckWarn:
			registration = true
		case strings.HasSuffix(check.name, " hook") && check.result.State == api.CheckPass:
			hook = true
		case strings.HasSuffix(check.name, " hooks feature") && check.result.State == api.CheckPass:
			feature = true
		}
	}
	if !plugin || !registration || !hook || !feature {
		t.Fatalf("decomposed Codex checks = %+v", checks)
	}
}

func checkNamesContaining(checks []doctorCheck, needle string) []doctorCheck {
	var result []doctorCheck
	for _, check := range checks {
		if strings.Contains(check.name, needle) {
			result = append(result, check)
		}
	}
	return result
}
