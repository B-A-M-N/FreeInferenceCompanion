package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCodexTraceHeaderPreservesConfigAndComments(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	original := "# keep this comment\nmodel_provider = \"freeinference\"\n\n[model_providers.freeinference]\nbase_url = \"https://freeinference.org/v1\"\nenv_key = \"CODEX_FI_KEY\"\n\n[other]\nvalue = \"untouched\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := EnsureCodexTraceHeader(path, "freeinference")
	if err != nil || !result.Ready || !result.Modified {
		t.Fatalf("ensure = %#v, %v", result, err)
	}
	updated, _ := os.ReadFile(path)
	text := string(updated)
	if !strings.Contains(text, "# keep this comment") || !strings.Contains(text, "value = \"untouched\"") || !strings.Contains(text, "[model_providers.freeinference.env_http_headers]") || !strings.Contains(text, "\"X-Session-ID\" = \"FI_TRACE_SESSION_ID\"") {
		t.Fatalf("surgical merge lost content or mapping:\n%s", text)
	}
}

func TestEnsureCodexTraceHeaderNeverReplacesExistingMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "[model_providers.freeinference]\nbase_url=\"https://freeinference.org/v1\"\nenv_key=\"CODEX_FI_KEY\"\n\n[model_providers.freeinference.env_http_headers]\n\"x-session-id\" = \"USER_TRACE_ID\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := EnsureCodexTraceHeader(path, "freeinference")
	if err == nil || result.Ready || !result.Existing {
		t.Fatalf("conflicting mapping should fail open: %#v, %v", result, err)
	}
	updated, _ := os.ReadFile(path)
	if string(updated) != original {
		t.Fatal("existing Codex mapping was modified")
	}
}

func TestInspectCodexTraceHeaderIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[model_providers.freeinference]\nenv_http_headers = { \"X-Session-ID\" = \"FI_TRACE_SESSION_ID\", \"X-Other\" = \"OTHER_ENV\" }\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	configured, conflict, err := InspectCodexTraceHeader(path, "freeinference")
	if err != nil || !configured || conflict {
		t.Fatalf("inspect = %t, %t, %v", configured, conflict, err)
	}
	updated, _ := os.ReadFile(path)
	if string(updated) != contents {
		t.Fatal("inspect changed Codex config")
	}
}

func TestCodexTraceBackupRestoreIsReversible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "model_provider = \"freeinference\"\n[model_providers.freeinference]\nbase_url=\"https://freeinference.org/v1\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BackupCodexTraceConfig(path); err != nil {
		t.Fatal(err)
	}
	if mapping, err := EnsureCodexTraceHeader(path, "freeinference"); err != nil || !mapping.Ready {
		t.Fatalf("ensure = %#v, %v", mapping, err)
	}
	if err := RestoreCodexTraceConfig(path, "freeinference"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("restored config=%q err=%v", data, err)
	}
	if _, err := os.Stat(path + codexTraceBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("backup remains after restore: %v", err)
	}
}

func FuzzInlineHeaderMapDoesNotPanic(f *testing.F) {
	f.Add(`{"X-Session-ID" = "FI_TRACE_SESSION_ID"}`)
	f.Add("{")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 8192 {
			return
		}
		_, _ = parseInlineHeaderMap(input)
	})
}
