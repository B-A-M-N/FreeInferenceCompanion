package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/cli"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// TestSecretNeverPersistsOrRenders proves that secret-shaped strings
// (API keys, bearer tokens, env-var assignments) cannot leak through any
// state, event, report, or stdout path. This is the regression guard for
// the security model: if a future change adds a field that could carry a
// secret into persisted state or rendered output, this test fails.
func TestSecretNeverPersistsOrRenders(t *testing.T) {
	const apiKey = "hyi-secret-key-abcdef0123456789"
	const bearerKey = "Bearer hyi-secret-key-abcdef0123456789"

	t.Setenv("FI_PROVIDER", "freeinference")
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", apiKey)
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	// Use a fresh nested subdir the companion creates itself, so the dir-perm
	// assertion reflects the companion's contract (not t.TempDir's umask).
	t.Setenv("FI_CACHE_DIR", filepath.Join(t.TempDir(), "freeinference-cache"))

	exitCode := cli.Run([]string{"freeinference", "hook", "claude-code", "SessionStart"},
		strings.NewReader(`{"session_id":"secret-test","model":"glm-5.1"}`), &bytes.Buffer{}, &bytes.Buffer{})
	if exitCode != 0 {
		t.Fatalf("SessionStart hook returned %d, want 0", exitCode)
	}

	// Drive a status update so live context + events.jsonl get written.
	statusPayload := map[string]any{
		"session_id":      "secret-test",
		"transcript_path": "/home/user/" + bearerKey + "/file.txt",
		"model":           map[string]any{"id": "glm-5.1", "display_name": "Display " + bearerKey},
		"context_window":  map[string]any{"total_input_tokens": 168000, "total_output_tokens": 2000, "context_window_size": 200000, "used_percentage": 84.0},
	}
	statusJSON, _ := json.Marshal(statusPayload)
	var stdout bytes.Buffer
	exitCode = cli.Run([]string{"freeinference", "status", "--compact"},
		bytes.NewReader(statusJSON), &stdout, &bytes.Buffer{})
	if exitCode != 0 {
		t.Fatalf("status returned %d", exitCode)
	}

	// 1. The API key must never appear in stdout (status line).
	if strings.Contains(stdout.String(), apiKey) {
		t.Errorf("API key leaked into status stdout: %s", stdout.String())
	}

	// 2. Walk every persisted file under FI_CACHE_DIR and assert no secret.
	cacheDir := os.Getenv("FI_CACHE_DIR")
	err := filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), apiKey) {
			t.Errorf("API key leaked into persisted file %s", path)
		}
		// File mode must be 0600 (no group/world access).
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("file %s has perm %o, want 0600", path, perm)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// 3. Directory permissions must be 0700 for every dir the companion
	// itself created (i.e. everything at or below cacheDir, which the
	// companion owns). The parent of cacheDir is t.TempDir() whose perms
	// are governed by the test runner's umask, not our contract.
	err = filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if path == cacheDir {
			// The cacheDir itself is created via FI_CACHE_DIR; ensure
			// EnsureDirs applied our perm. The t.TempDir() parent is outside.
			if perm := info.Mode().Perm(); perm != 0700 {
				t.Errorf("cache dir %s has perm %o, want 0700", path, perm)
			}
			return nil
		}
		if perm := info.Mode().Perm(); perm != 0700 {
			t.Errorf("dir %s has perm %o, want 0700", path, perm)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dirs: %v", err)
	}

	// 4. Reports must not contain the key, regardless of format.
	for _, format := range []string{"markdown", "json"} {
		var out bytes.Buffer
		exitCode = cli.Run([]string{"freeinference", "report", "--format", format, "--session", "secret-test", "--client", "claude-code"},
			&bytes.Buffer{}, &out, &bytes.Buffer{})
		if exitCode != 0 {
			t.Errorf("report --format %s returned %d", format, exitCode)
		}
		if strings.Contains(out.String(), apiKey) {
			t.Errorf("API key leaked into %s report: %s", format, out.String())
		}
	}

	// 5. Snapshot --json must not contain the key.
	var snapOut bytes.Buffer
	exitCode = cli.Run([]string{"freeinference", "snapshot", "--json", "--session", "secret-test", "--client", "claude-code"},
		&bytes.Buffer{}, &snapOut, &bytes.Buffer{})
	if exitCode != 0 {
		t.Fatalf("snapshot returned %d", exitCode)
	}
	if strings.Contains(snapOut.String(), apiKey) {
		t.Errorf("API key leaked into snapshot JSON: %s", snapOut.String())
	}

	// 6. Render modes must not contain the key.
	for _, mode := range []string{"line", "expanded"} {
		var out bytes.Buffer
		exitCode = cli.Run([]string{"freeinference", "render", "--mode", mode, "--session", "secret-test", "--client", "claude-code"},
			&bytes.Buffer{}, &out, &bytes.Buffer{})
		if exitCode != 0 {
			t.Errorf("render --mode %s returned %d", mode, exitCode)
		}
		if strings.Contains(out.String(), apiKey) {
			t.Errorf("API key leaked into %s render: %s", mode, out.String())
		}
	}

	// 7. Events must not contain the key, even when raw reading the file.
	paths, _ := state.NewPaths()
	events, err := state.ReadEvents(paths, schema.ClientClaudeCode, "secret-test", 0)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	for _, ev := range events {
		encoded, _ := json.Marshal(ev)
		if strings.Contains(string(encoded), apiKey) {
			t.Errorf("API key leaked into event: %s", encoded)
		}
	}

	// 8. Doctor must not print the key.
	var docOut bytes.Buffer
	_ = cli.Run([]string{"freeinference", "doctor"},
		&bytes.Buffer{}, &docOut, &bytes.Buffer{})
	if strings.Contains(docOut.String(), apiKey) {
		t.Errorf("API key leaked into doctor output: %s", docOut.String())
	}
}
