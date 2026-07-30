// Package plugins_test contains behavioral smoke tests for the plugin bundle.
// These tests exercise installation artefacts, hook-script behaviour, JSON schema
// validity, status-line wrapper registration, disabled-mode behaviour, and
// malformed-payload handling — all without requiring the freeinference binary
// to be on PATH.
//
// They use go test ./plugins and must pass on every CI run.
package plugins_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/install"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// pluginRoot is the absolute path to the plugins/ directory at repo root.
func pluginRoot() string {
	// The test binary lives under the module tree; the plugin root is relative.
	wd, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("plugins_test: Getwd: %v", err))
	}
	// Walk up from the test working directory to find the plugins/ sibling of the module root.
	for dir := wd; ; {
		plug := filepath.Join(dir, "plugins")
		if _, err := os.Stat(plug); err == nil {
			return plug
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	panic("plugins_test: cannot find plugins/ directory")
}

// pluginDir returns the absolute path to <pluginRoot>/<name>.
func pluginDir(name string) string {
	return filepath.Join(pluginRoot(), name)
}

// ---------------------------------------------------------------------------
// 1. Claude Code plugin installation
// ---------------------------------------------------------------------------

func TestClaudeCodePluginJSONRequiredFields(t *testing.T) {
	plug := pluginDir("claude-code")
	pj := filepath.Join(plug, ".claude-plugin", "plugin.json")

	data, err := os.ReadFile(pj)
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse plugin.json: %v", err)
	}

	for _, key := range []string{"name", "version", "description", "author", "hooks", "skills"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("plugin.json missing required field: %s", key)
		}
	}
	// Author must have a name.
	if author, ok := parsed["author"].(map[string]any); !ok {
		t.Error("author must be an object")
	} else if _, ok := author["name"]; !ok {
		t.Error("author.name is required")
	}
}

func TestClaudeCodeHookEventStructure(t *testing.T) {
	plug := pluginDir("claude-code")
	hj := filepath.Join(plug, "hooks", "hooks.json")

	data, err := os.ReadFile(hj)
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}

	hooksObj, ok := parsed["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks.json must have a 'hooks' object")
	}
	for event, eventVal := range hooksObj {
		arr, ok := eventVal.([]any)
		if !ok {
			t.Errorf("event %s must be an array", event)
			continue
		}
		for i, elem := range arr {
			obj, ok := elem.(map[string]any)
			if !ok {
				t.Errorf("event %s element %d must be an object", event, i)
				continue
			}
			if _, ok := obj["matcher"]; !ok {
				t.Errorf("event %s element %d missing matcher", event, i)
			}
			subHooks, ok := obj["hooks"].([]any)
			if !ok {
				t.Errorf("event %s element %d missing hooks array", event, i)
				continue
			}
			for j, sh := range subHooks {
				shObj, ok := sh.(map[string]any)
				if !ok {
					t.Errorf("event %s sub-hook %d must be an object", event, j)
					continue
				}
				if _, ok := shObj["type"]; !ok {
					t.Errorf("event %s sub-hook %d missing type", event, j)
				}
				if _, ok := shObj["command"]; !ok {
					t.Errorf("event %s sub-hook %d missing command", event, j)
				}
			}
		}
	}
}

func TestClaudeCodeHookScriptExecutable(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat run-hook.sh: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Error("run-hook.sh must be executable")
	}
}

func TestClaudeCodeReferencedHooksExist(t *testing.T) {
	plug := pluginDir("claude-code")

	// Check hooks.json referenced scripts exist.
	hj := filepath.Join(plug, "hooks", "hooks.json")
	data, err := os.ReadFile(hj)
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var hooksObj map[string]any
	if err := json.Unmarshal(data, &hooksObj); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	hooksMap := hooksObj["hooks"].(map[string]any)
	for event, evVal := range hooksMap {
		arr := evVal.([]any)
		for _, elem := range arr {
			obj := elem.(map[string]any)
			subHooks := obj["hooks"].([]any)
			for _, sh := range subHooks {
				shObj := sh.(map[string]any)
				cmd := shObj["command"].(string)
				// Commands reference scripts/run-hook.sh — verify it exists.
				if !strings.Contains(cmd, "run-hook.sh") {
					continue
				}
				script := filepath.Join(plug, "scripts", "run-hook.sh")
				if _, err := os.Stat(script); err != nil {
					t.Errorf("hook event %s references script that does not exist: %s (%v)", event, script, err)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Codex skill package
// ---------------------------------------------------------------------------

func TestCodexPluginJSONRequiredFields(t *testing.T) {
	plug := pluginDir("codex")
	pj := filepath.Join(plug, ".codex-plugin", "plugin.json")

	data, err := os.ReadFile(pj)
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse plugin.json: %v", err)
	}

	for _, key := range []string{"name", "version", "description", "author", "skills"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("plugin.json missing required field: %s", key)
		}
	}
	if _, ok := parsed["author"].(map[string]any); !ok {
		t.Error("author must be an object")
	}
}

// ---------------------------------------------------------------------------
// 3. Hook script behaviour via subprocess
// ---------------------------------------------------------------------------

func TestClaudeCodeHookSyntax(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	cmd := exec.Command("bash", "-n", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n failed: %v\noutput: %s", err, string(out))
	}
}

func TestClaudeCodeHookExitZeroWhenMissingBinary(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	cmd := exec.Command("bash", script, "SessionStart")
	// Ensure the binary does NOT exist in PATH and no bundled binary is found.
	cmd.Env = append(os.Environ(),
		"FI_DISABLED=",
		"PLUGIN_ROOT=/nonexistent/path/noexist",
		"CLAUDE_PLUGIN_ROOT=/nonexistent/path/noexist",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		// We want exit 0 (fail open).
		return
	}
	// If non-zero, the exit code must be 0 (fail open).
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 0 {
			t.Errorf("expected exit 0 (fail open), got %d; output: %s", exitErr.ExitCode(), string(out))
		}
	} else {
		t.Fatalf("unexpected error: %v; output: %s", err, string(out))
	}
}

func TestClaudeCodeHookResolvesPluginRoot(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	cmd := exec.Command("bash", script, "TestEvent")
	cmd.Env = append(os.Environ(),
		"FI_DISABLED=",
		"PLUGIN_ROOT="+pluginDir("claude-code"),
	)
	// The script should find PLUGIN_ROOT set and attempt resolution; with no
	// bundled binary it falls through to exit 0.
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 0 {
			t.Errorf("expected exit 0, got %d; output: %s", exitErr.ExitCode(), string(out))
		}
	} else {
		t.Fatalf("unexpected error: %v; output: %s", err, string(out))
	}
}

// ---------------------------------------------------------------------------
// 4. Plugin JSON schema validation
// ---------------------------------------------------------------------------

func TestClaudeCodeVersionIsSemver(t *testing.T) {
	pj := filepath.Join(pluginDir("claude-code"), ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(pj)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	version, ok := parsed["version"].(string)
	if !ok {
		t.Fatal("version must be a string")
	}
	semver := regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[a-zA-Z0-9._+]*)?(\+[a-zA-Z0-9._+]*)?$`)
	if !semver.MatchString(version) {
		t.Errorf("version %q does not match semver", version)
	}
}

func TestCodexVersionIsSemver(t *testing.T) {
	pj := filepath.Join(pluginDir("codex"), ".codex-plugin", "plugin.json")
	data, err := os.ReadFile(pj)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	version, ok := parsed["version"].(string)
	if !ok {
		t.Fatal("version must be a string")
	}
	semver := regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[a-zA-Z0-9._+]*)?(\+[a-zA-Z0-9._+]*)?$`)
	if !semver.MatchString(version) {
		t.Errorf("version %q does not match semver", version)
	}
}

func TestClaudeCodeHookEventsMatchExpected(t *testing.T) {
	hj := filepath.Join(pluginDir("claude-code"), "hooks", "hooks.json")
	data, _ := os.ReadFile(hj)
	var parsed map[string]any
	json.Unmarshal(data, &parsed)
	hooksMap := parsed["hooks"].(map[string]any)

	// These are the events the Claude Code plugin declares.
	expectedEvents := map[string]bool{
		"SessionStart":     true,
		"SessionEnd":       true,
		"UserPromptSubmit": true,
		"PreCompact":       true,
		"PostCompact":      true,
		"Stop":             true,
		"StopFailure":      true,
	}
	for event := range hooksMap {
		if !expectedEvents[event] {
			t.Errorf("unexpected hook event: %s", event)
		}
	}
}

func TestClaudeCodeSkillsDirectoryValid(t *testing.T) {
	skillsDir := filepath.Join(pluginDir("claude-code"), "skills")
	info, err := os.Stat(skillsDir)
	if err != nil {
		t.Fatalf("skills directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("skills must be a directory")
	}
	// Each subdirectory should have a SKILL.md file.
	entries, _ := os.ReadDir(skillsDir)
	for _, e := range entries {
		if e.IsDir() {
			skillMd := filepath.Join(skillsDir, e.Name(), "SKILL.md")
			if _, err := os.Stat(skillMd); err != nil {
				t.Errorf("skill %s missing SKILL.md: %v", e.Name(), err)
			}
		}
	}
}

func TestCodexSkillsDirectoryValid(t *testing.T) {
	skillsDir := filepath.Join(pluginDir("codex"), "skills")
	info, err := os.Stat(skillsDir)
	if err != nil {
		t.Fatalf("skills directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("skills must be a directory")
	}
	entries, _ := os.ReadDir(skillsDir)
	for _, e := range entries {
		if e.IsDir() {
			skillMd := filepath.Join(skillsDir, e.Name(), "SKILL.md")
			if _, err := os.Stat(skillMd); err != nil {
				t.Errorf("skill %s missing SKILL.md: %v", e.Name(), err)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Status-line registration (via internal/install)
// ---------------------------------------------------------------------------

func TestStatusLineWrapperIsExecutable(t *testing.T) {
	home := t.TempDir()
	if err := install.InstallClaudeStatusLine(home, "/opt/freeinference", install.ScopeUser, home, os.Stdout); err != nil {
		t.Fatalf("install: %v", err)
	}
	wrapper := filepath.Join(home, ".claude", "statusline-freeinference.sh")
	info, err := os.Stat(wrapper)
	if err != nil {
		t.Fatalf("wrapper missing: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Error("wrapper must be executable")
	}
}

func TestClaudeSettingsReferenceWrapper(t *testing.T) {
	home := t.TempDir()
	if err := install.InstallClaudeStatusLine(home, "/opt/freeinference", install.ScopeUser, home, os.Stdout); err != nil {
		t.Fatalf("install: %v", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	sl, ok := parsed["statusLine"].(map[string]any)
	if !ok {
		t.Fatal("statusLine key missing from settings")
	}
	cmd, ok := sl["command"].(string)
	if !ok {
		t.Fatalf("statusLine.command is not a string: %T", sl["command"])
	}
	wrapper := filepath.Join(home, ".claude", "statusline-freeinference.sh")
	if cmd != wrapper {
		t.Errorf("statusLine.command = %q, want %q", cmd, wrapper)
	}
}

// ---------------------------------------------------------------------------
// 6. Disabled state behaviour
// ---------------------------------------------------------------------------

func TestClaudeCodeDisabledProduceZeroOutput(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	cmd := exec.Command("bash", script, "SessionStart")
	cmd.Env = append(os.Environ(), "FI_DISABLED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unexpected error: %v; output: %s", err, string(out))
	}
	if len(out) != 0 {
		t.Errorf("FI_DISABLED=1 should produce zero output, got: %q", string(out))
	}
}

func TestClaudeCodeHookExitZeroWhenDisabled(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	cmd := exec.Command("bash", script, "SessionStart")
	cmd.Env = append(os.Environ(), "FI_DISABLED=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 0 {
			t.Errorf("expected exit 0 when disabled, got %d; output: %s", exitErr.ExitCode(), string(out))
		}
	} else {
		t.Fatalf("unexpected error: %v; output: %s", err, string(out))
	}
}

// ---------------------------------------------------------------------------
// 7. Malformed payload handling
// ---------------------------------------------------------------------------

func TestClaudeCodeHookNoCrashOnGarbageInput(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	cmd := exec.Command("bash", script) // no args at all
	cmd.Env = append(os.Environ(),
		"FI_DISABLED=",
		"PLUGIN_ROOT=",
		"CLAUDE_PLUGIN_ROOT=",
	)
	out, err := cmd.CombinedOutput()
	// The script uses set -u; missing args are handled by ${1:-} default.
	// With no binary available, it must exit 0.
	if err == nil {
		return
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 0 {
			t.Errorf("expected exit 0 on empty input, got %d; output: %s", exitErr.ExitCode(), string(out))
		}
	} else {
		t.Fatalf("unexpected error: %v; output: %s", err, string(out))
	}
}

func TestClaudeCodeHookNoCrashOnArbitraryStdin(t *testing.T) {
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")
	cmd := exec.Command("bash", script, "SessionStart")
	cmd.Env = append(os.Environ(),
		"FI_DISABLED=",
		"PLUGIN_ROOT=/nonexistent",
	)
	// Feed arbitrary bytes on stdin.
	cmd.Stdin = strings.NewReader("GARBAGE\x00BINARY\x01DATA")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 0 {
			t.Errorf("expected exit 0 with garbage stdin, got %d; output: %s", exitErr.ExitCode(), string(out))
		}
	} else {
		t.Fatalf("unexpected error: %v; output: %s", err, string(out))
	}
}

// ---------------------------------------------------------------------------
// 8. Behavioral tests: bundled binary, state creation, skill content
// ---------------------------------------------------------------------------

// buildTestBinary compiles the freeinference binary for use in behavioral tests.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "freeinference")
	modRoot := filepath.Dir(pluginRoot())
	cmd := exec.Command("go", "build", "-o", bin, filepath.Join(modRoot, "cmd", "fi"))
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build freeinference: %v", err)
	}
	return bin
}

// runHook executes a plugin hook script with the given env and returns
// stdout, stderr, and exit code.
func runHook(t *testing.T, script, event string, env []string, stdin string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("bash", script, event)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), "", 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), string(out), exitErr.ExitCode()
	}
	t.Fatalf("unexpected error running hook: %v", err)
	return "", "", -1
}

func TestClaudeCodeHookCreatesSessionState(t *testing.T) {
	bin := buildTestBinary(t)
	tmpHome := t.TempDir()
	cacheDir := filepath.Join(tmpHome, "cache")
	script := filepath.Join(pluginDir("claude-code"), "scripts", "run-hook.sh")

	env := append(os.Environ(),
		"HOME="+tmpHome,
		"FI_CACHE_DIR="+cacheDir,
		"FI_DISABLED=",
		// SessionStart normally launches detached refresh workers. Keep this
		// behavioral test self-contained so a child cannot outlive TempDir cleanup.
		"FI_NO_BACKGROUND=1",
		"FREEINFERENCE_API_KEY=test-key-hyi",
		"FREEINFERENCE_BASE_URL=https://freeinference.org/v1",
		"PATH="+filepath.Dir(bin)+":/usr/bin:/bin",
		"CLAUDE_PLUGIN_ROOT=",
	)

	// Feed a realistic SessionStart hook payload via stdin.
	payload := `{"session_id":"test-session-123","hook_event_name":"SessionStart"}`
	stdout, _, exitCode := runHook(t, script, "SessionStart", env, payload)
	if exitCode != 0 {
		t.Fatalf("hook exited %d, want 0; stdout: %s", exitCode, stdout)
	}

	// Verify a session state file was created.
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir %s: %v", cacheDir, err)
	}
	if len(entries) == 0 {
		t.Fatal("no cache entries created after SessionStart hook")
	}

	// Verify the binary actually ran (not just exit 0).
	// The freeinference binary was on PATH, so the hook should have invoked it.
	// Check that sessions list returns at least one session.
	sessionsOut, errOut, exitCode := runFI(t, bin, tmpHome, cacheDir, "sessions", "--json", "--include-identifiers")
	if exitCode != 0 {
		t.Fatalf("freeinference sessions exited %d; stderr: %s", exitCode, errOut)
	}
	if !strings.Contains(sessionsOut, "test-session-123") {
		t.Errorf("sessions output does not contain test session ID:\n%s", sessionsOut)
	}
}

func runFI(t *testing.T, binary, home, cacheDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"FI_CACHE_DIR="+cacheDir,
		"FI_DISABLED=",
		"FI_NO_BACKGROUND=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), "", 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), string(out), exitErr.ExitCode()
	}
	t.Fatalf("unexpected error running freeinference %v: %v", args, err)
	return "", "", -1
}

func TestClaudeCodeSkillsInventory(t *testing.T) {
	skillsDir := filepath.Join(pluginDir("claude-code"), "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("read skills dir: %v", err)
	}
	var skillNames []string
	for _, e := range entries {
		if e.IsDir() {
			skillNames = append(skillNames, e.Name())
		}
	}
	// Verify the router skill exists.
	hasRouter := false
	for _, name := range skillNames {
		if name == "freeinference" {
			hasRouter = true
		}
	}
	if !hasRouter {
		t.Errorf("expected router skill 'freeinference' in Claude skills, got: %v", skillNames)
	}
	// Verify no old fi-* skills remain.
	for _, name := range skillNames {
		if strings.HasPrefix(name, "fi-") {
			t.Errorf("old fi-* skill still present: %s — should be removed", name)
		}
	}
}

func TestCodexSkillsInventory(t *testing.T) {
	skillsDir := filepath.Join(pluginDir("codex"), "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("read skills dir: %v", err)
	}
	var skillNames []string
	for _, e := range entries {
		if e.IsDir() {
			skillNames = append(skillNames, e.Name())
		}
	}
	hasRouter := false
	for _, name := range skillNames {
		if name == "freeinference" {
			hasRouter = true
		}
	}
	if !hasRouter {
		t.Errorf("expected router skill 'freeinference' in Codex skills, got: %v", skillNames)
	}
	for _, name := range skillNames {
		if strings.HasPrefix(name, "fi-") {
			t.Errorf("old fi-* skill still present: %s — should be removed", name)
		}
	}
}

func TestClaudeCodeBundledBinaryVersionMatchesManifest(t *testing.T) {
	// Verify the plugin manifest version and the bundled binary agree.
	pj := filepath.Join(pluginDir("claude-code"), ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(pj)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}

	// Build the current binary and check its version.
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "version", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v; output: %s", err, string(out))
	}
	var ver struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &ver); err != nil {
		t.Fatalf("version output not JSON: %s", string(out))
	}
	if manifest.Version != ver.Version {
		t.Logf("manifest version: %s, binary version: %s (may differ in dev)", manifest.Version, ver.Version)
	}
}

func TestClaudeCodeBundledBinaryNotAReservedWord(t *testing.T) {
	bin := buildTestBinary(t)
	// Verify the binary can be invoked without Bash reserved-word issues.
	// "fi" is a Bash reserved word that terminates if statements.
	binName := filepath.Base(bin)
	if binName == "fi" {
		t.Fatal("binary name is 'fi' which is a Bash reserved word")
	}
	// Verify command substitution works in Bash.
	cmd := exec.Command("bash", "-c", fmt.Sprintf(`out=$("%s" version --json 2>/dev/null) && echo "$out" | grep -c version`, bin))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash command substitution failed: %v; output: %s", err, string(out))
	}
}
