package codexusage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRollout(t *testing.T, home, name, body string) string {
	t.Helper()
	path := filepath.Join(home, "sessions", "2026", "09", "11", name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadFileUsesLatestTokenCountAndMetadata(t *testing.T) {
	home := t.TempDir()
	path := writeRollout(t, home, "rollout-latest.jsonl", `
{"timestamp":"2026-09-11T18:00:00Z","type":"session_meta","payload":{"session_id":"session-123","cwd":"file:///work/repo"}}
{"timestamp":"2026-09-11T18:00:01Z","type":"turn_context","payload":{"model":"deepseek-v4-flash","cwd":"/work/repo"}}
{"timestamp":"2026-09-11T18:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":700,"cache_write_input_tokens":100,"output_tokens":25,"reasoning_output_tokens":5,"total_tokens":1025},"total_token_usage":{"input_tokens":1000,"cached_input_tokens":700,"cache_write_input_tokens":100,"output_tokens":25,"total_tokens":1025},"model_context_window":950000}}}
{"timestamp":"2026-09-11T18:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1200,"cached_input_tokens":900,"cache_write_input_tokens":100,"output_tokens":30,"reasoning_output_tokens":6,"total_tokens":1230},"total_token_usage":{"input_tokens":2200,"cached_input_tokens":1600,"cache_write_input_tokens":200,"output_tokens":55,"total_tokens":2255},"model_context_window":950000}}}
`)
	usage, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if usage.SessionID != "session-123" || usage.Model != "deepseek-v4-flash" || usage.CWD != "/work/repo" {
		t.Fatalf("metadata = %+v", usage)
	}
	if usage.ContextWindow != 950000 || usage.Last.InputTokens == nil || *usage.Last.InputTokens != 1200 {
		t.Fatalf("latest usage = %+v", usage)
	}
	if fresh := usage.FreshInputTokens(); fresh == nil || *fresh != 200 {
		t.Fatalf("fresh input = %v, want 200", fresh)
	}
	if usage.ObservedAt != (time.Date(2026, 9, 11, 18, 0, 3, 0, time.UTC)) {
		t.Fatalf("observed at = %s", usage.ObservedAt)
	}
}

func TestLatestChoosesNewestRollout(t *testing.T) {
	home := t.TempDir()
	older := writeRollout(t, home, "rollout-older.jsonl", `{"timestamp":"2026-09-11T17:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10}}}}`)
	newer := writeRollout(t, home, "rollout-newer.jsonl", `{"timestamp":"2026-09-11T18:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20}}}}`)
	oldTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	usage, err := Latest(home)
	if err != nil {
		t.Fatal(err)
	}
	if usage.RolloutPath != newer || usage.Last.InputTokens == nil || *usage.Last.InputTokens != 20 {
		t.Fatalf("latest = %+v, want %s", usage, newer)
	}
}

func TestLatestAndReadFileReportMissingUsage(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "rollout-empty.jsonl", `{"type":"session_meta","payload":{"session_id":"s"}}`)
	_, err := Latest(home)
	if !errors.Is(err, ErrNoUsage) {
		t.Fatalf("error = %v, want ErrNoUsage", err)
	}
	_, err = FindLatest(filepath.Join(home, "missing"))
	if !errors.Is(err, ErrNoRollout) {
		t.Fatalf("missing error = %v, want ErrNoRollout", err)
	}
}
