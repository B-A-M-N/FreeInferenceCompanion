package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
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
	if !strings.Contains(text, "# keep this comment") || !strings.Contains(text, "value = \"untouched\"") || !strings.Contains(text, "[model_providers.freeinference.env_http_headers]") {
		t.Fatalf("surgical merge lost content or mapping:\n%s", text)
	}
	for _, mapping := range tracing.CodexHeaderMappings() {
		want := "\"" + mapping.Header + "\" = \"" + mapping.Env + "\""
		if !strings.Contains(text, want) {
			t.Fatalf("Codex mapping %q missing:\n%s", want, text)
		}
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

func TestInspectCodexTraceHeadersReportsIncompleteMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[model_providers.freeinference.env_http_headers]\n\"X-Session-ID\" = \"FI_TRACE_SESSION_ID\"\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	mapping, err := InspectCodexTraceHeaders(path, "freeinference")
	if err != nil || mapping.Ready || len(mapping.Missing) != 3 || len(mapping.Conflicts) != 0 {
		t.Fatalf("incomplete mapping = %#v, err=%v", mapping, err)
	}
}

func TestEnsureCodexTraceHeadersRejectsStaticConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[model_providers.freeinference.env_http_headers]\n\"X-Session-ID\" = \"FI_TRACE_SESSION_ID\"\n\"X-FI-Client\" = \"other\"\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	mapping, err := EnsureCodexTraceHeaders(path, "freeinference")
	if err == nil || mapping.Ready || len(mapping.Conflicts) != 1 || mapping.Conflicts[0] != tracing.ClientHeader {
		t.Fatalf("static conflict = %#v, err=%v", mapping, err)
	}
	updated, _ := os.ReadFile(path)
	if string(updated) != contents {
		t.Fatal("conflicting Codex mapping was modified")
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

func TestCodexTraceRestoreRefusesChangedStaticMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "model_provider = \"freeinference\"\n[model_providers.freeinference]\nbase_url=\"https://freeinference.org/v1\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetupCodexTraceConfig(path, "freeinference"); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(updated), `"X-FI-Client" = "FI_TRACE_CLIENT"`, `"X-FI-Client" = "USER_CLIENT"`, 1)
	if changed == string(updated) {
		t.Fatal("test fixture did not change static mapping")
	}
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreCodexTraceConfig(path, "freeinference"); err == nil {
		t.Fatal("restore replaced a changed static mapping")
	}
	if _, err := os.Stat(path + codexTraceBackupSuffix); err != nil {
		t.Fatalf("backup was removed after refused restore: %v", err)
	}
}

func TestInspectCodexTraceHeadersRejectsDuplicateKnownMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[model_providers.freeinference.env_http_headers]\n\"X-Session-ID\" = \"FI_TRACE_SESSION_ID\"\n\"x-session-id\" = \"FI_TRACE_SESSION_ID\"\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectCodexTraceHeaders(path, "freeinference"); err == nil {
		t.Fatal("duplicate known mapping accepted")
	}
}

func TestCodexTraceSetupIsAtomicAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "model_provider = \"freeinference\"\n[model_providers.freeinference]\nbase_url=\"https://freeinference.org/v1\"\nenv_key=\"CODEX_FI_KEY\"\n"
	if err := os.WriteFile(path, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	first, err := SetupCodexTraceConfig(path, "freeinference")
	if err != nil || !first.Ready || !first.Modified {
		t.Fatalf("first setup = %#v, %v", first, err)
	}
	backup, err := os.ReadFile(path + codexTraceBackupSuffix)
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q, err=%v", backup, err)
	}
	second, err := SetupCodexTraceConfig(path, "freeinference")
	if err != nil || !second.Ready || second.Modified {
		t.Fatalf("second setup = %#v, %v", second, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("setup changed config mode: %v, %o", err, info.Mode().Perm())
	}
}

func TestCodexTraceFixtureCorpus(t *testing.T) {
	fixtures := []string{
		"model_provider=\"freeinference\"\r\n[model_providers.\"freeinference\"]\r\nbase_url=\"https://freeinference.org/v1\"\r\nenv_key=\"CODEX_FI_KEY\"\r\n",
		"[model_providers.freeinference]\nbase_url=\"https://freeinference.org/v1\"\nenv_key=\"CODEX_FI_KEY\"\nenv_http_headers = { \"X-Other\" = \"OTHER_ENV\" }\n",
		"[model_providers.freeinference]\nbase_url=\"https://freeinference.org/v1\"\nenv_key=\"CODEX_FI_KEY\"\n[model_providers.freeinference.env_http_headers]\n# keep\n\"X-Other\" = \"OTHER_ENV\"\n",
	}
	for i, fixture := range fixtures {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
				t.Fatal(err)
			}
			mapping, err := EnsureCodexTraceHeader(path, "freeinference")
			if err != nil || !mapping.Ready {
				t.Fatalf("fixture setup = %#v, %v", mapping, err)
			}
			configured, conflict, err := InspectCodexTraceHeader(path, "freeinference")
			if err != nil || !configured || conflict {
				t.Fatalf("fixture inspect = %t, %t, %v", configured, conflict, err)
			}
		})
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
