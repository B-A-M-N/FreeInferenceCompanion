package install

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexTUIInstallPreservesAndRestoresFooter(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := `model_provider = "freeinference"

[tui]
status_line = ["model", "git-branch"] # user footer

[profiles.fast]
model = "glm-5.1"
`
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	status, err := InspectCodexTUI(home, configPath)
	if err != nil {
		t.Fatalf("inspect installed: %v", err)
	}
	if status.Status != "installed" || !status.Installed || !status.Referenced {
		t.Fatalf("installed status = %+v", status)
	}
	for _, item := range []string{"model", "git-branch", "model-with-reasoning", "context-remaining", "current-dir"} {
		if !containsString(status.StatusLine, item) {
			t.Errorf("installed footer missing %q: %v", item, status.StatusLine)
		}
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `[profiles.fast]`) || !strings.Contains(string(updated), "# user footer") {
		t.Error("install did not preserve unrelated config or inline comment")
	}

	if err := UninstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Errorf("uninstall did not restore original config\nwant:\n%s\ngot:\n%s", original, restored)
	}
}

func TestCodexTUIInstallRefusesDrift(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[tui]\nstatus_line = [\"model\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[tui]\nstatus_line = [\"my-custom-footer\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := UninstallCodexTUI(home, configPath, io.Discard); !errors.Is(err, ErrDriftedCodexTUI) {
		t.Fatalf("uninstall drift error = %v", err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); !errors.Is(err, ErrDriftedCodexTUI) {
		t.Fatalf("reinstall drift error = %v", err)
	}
}

func TestCodexTUIUninstallRefusesDifferentConfigPath(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	otherPath := filepath.Join(home, ".codex", "other.toml")
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(otherPath, []byte("[tui]\nstatus_line = [\"model\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := UninstallCodexTUI(home, otherPath, io.Discard); err == nil {
		t.Fatal("uninstall must reject a configuration path different from metadata")
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("mismatched uninstall changed the installed config: %v", err)
	}
}

func TestCodexTUIInstallCreatesNativeFooterWhenMissing(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := InstallCodexTUI(home, configPath, io.Discard); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	if !strings.Contains(contents, "[tui]") || !strings.Contains(contents, "context-remaining") {
		t.Fatalf("native footer missing: %s", contents)
	}
	status, err := InspectCodexTUI(home, configPath)
	if err != nil || status.Status != "installed" {
		t.Fatalf("status = %+v, err=%v", status, err)
	}
}

func TestCodexTUIRejectsMultilineStatusArray(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	original := "[tui]\nstatus_line = [\n  \"model\",\n  \"context-remaining\"\n]\n"
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexTUI(home, configPath, io.Discard); err == nil {
		t.Fatal("multiline status_line should be rejected")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Error("rejected install modified config")
	}
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}
