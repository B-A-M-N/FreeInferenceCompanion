package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func indexTestSnapshot(client, sessionID, model, status string, lastEvent time.Time) *schema.Snapshot {
	return &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: client},
		Session: schema.SessionInfo{
			ID:          sessionID,
			StartedAt:   lastEvent.Add(-time.Hour),
			LastEventAt: lastEvent,
			Status:      status,
		},
		Model: schema.ModelInfo{ID: model},
	}
}

func TestSessionIndexUpdateAndLoad(t *testing.T) {
	paths := testPaths(t)
	now := time.Now()

	if err := UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "s1", "glm-5.1", schema.SessionActive, now)); err != nil {
		t.Fatalf("update index: %v", err)
	}

	idx, err := LoadSessionIndex(paths)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	if len(idx.Sessions) != 1 {
		t.Fatalf("entries = %d", len(idx.Sessions))
	}
	e := idx.Sessions[0]
	if e.SessionID != "s1" || e.Client != schema.ClientClaudeCode || e.ModelID != "glm-5.1" {
		t.Errorf("unexpected entry: %+v", e)
	}
	if e.SessionKey == "" {
		t.Error("session key must be recorded")
	}

	// Update same session → still one entry, newer timestamp.
	later := now.Add(time.Minute)
	if err := UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "s1", "glm-5.1", schema.SessionCompleted, later)); err != nil {
		t.Fatal(err)
	}
	idx, _ = LoadSessionIndex(paths)
	if len(idx.Sessions) != 1 {
		t.Fatalf("entries after update = %d", len(idx.Sessions))
	}
	if idx.Sessions[0].Status != schema.SessionCompleted {
		t.Errorf("status = %s", idx.Sessions[0].Status)
	}
}

func TestResolveSessionMostRecentActiveForClient(t *testing.T) {
	paths := testPaths(t)
	now := time.Now()

	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "old", "m1", schema.SessionCompleted, now.Add(-time.Hour)))
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientCodex, "codex-active", "m2", schema.SessionActive, now.Add(-time.Minute)))
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "claude-active", "m3", schema.SessionActive, now.Add(-2*time.Hour)))

	e, err := ResolveSession(paths, schema.ClientClaudeCode, "")
	if err != nil || e == nil {
		t.Fatalf("resolve: %v %v", e, err)
	}
	if e.SessionID != "claude-active" {
		t.Errorf("resolved %s, want claude-active", e.SessionID)
	}

	e, err = ResolveSession(paths, schema.ClientCodex, "")
	if err != nil || e == nil {
		t.Fatalf("resolve: %v %v", e, err)
	}
	if e.SessionID != "codex-active" {
		t.Errorf("resolved %s, want codex-active", e.SessionID)
	}
}

func TestResolveSessionExplicitID(t *testing.T) {
	paths := testPaths(t)
	now := time.Now()
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "s1", "m1", schema.SessionActive, now))
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "s2", "m2", schema.SessionActive, now.Add(time.Minute)))

	e, err := ResolveSession(paths, "", "s1")
	if err != nil || e == nil {
		t.Fatalf("resolve: %v %v", e, err)
	}
	if e.SessionID != "s1" {
		t.Errorf("resolved %s", e.SessionID)
	}
}

func TestResolveSessionAmbiguous(t *testing.T) {
	paths := testPaths(t)
	now := time.Now()
	// Two active sessions updated within seconds of each other.
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "a", "m1", schema.SessionActive, now))
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientCodex, "b", "m2", schema.SessionActive, now.Add(-time.Second)))

	_, err := ResolveSession(paths, "", "")
	if !errors.Is(err, ErrAmbiguousSession) {
		t.Errorf("expected ambiguity, got %v", err)
	}
}

func TestResolveSessionEmptyIndex(t *testing.T) {
	paths := testPaths(t)
	e, err := ResolveSession(paths, "", "")
	if err != nil {
		t.Fatalf("empty index must not error: %v", err)
	}
	if e != nil {
		t.Errorf("empty index must resolve to nil, got %+v", e)
	}
}

func TestCorruptIndexFailsOpen(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONAtomically(paths.GlobalSessionIndex(), map[string]string{"bad": "shape"}); err != nil {
		t.Fatal(err)
	}
	// Overwrite with actual garbage.
	if err := os.MkdirAll(filepath.Dir(paths.GlobalSessionIndex()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.GlobalSessionIndex(), []byte("{junk"), 0600); err != nil {
		t.Fatal(err)
	}

	idx, err := LoadSessionIndex(paths)
	if err != nil {
		t.Fatalf("corrupt index must fail open: %v", err)
	}
	if len(idx.Sessions) != 0 {
		t.Errorf("corrupt index should load empty, got %d entries", len(idx.Sessions))
	}
}

// TestPruneStaleIndexEntries verifies that CleanupStaleSessions removes index
// entries for sessions whose directories no longer exist.
func TestPruneStaleIndexEntries(t *testing.T) {
	paths := testPaths(t)
	now := time.Now()

	// Add two sessions to the index.
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "live", "m1", schema.SessionActive, now))
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "dead", "m2", schema.SessionActive, now))

	// Only create the directory for "live".
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "live"); err != nil {
		t.Fatal(err)
	}

	// Prune. "dead" has no directory and should be removed from the index.
	if err := pruneStaleIndexEntries(paths, now); err != nil {
		t.Fatalf("prune: %v", err)
	}

	idx, _ := LoadSessionIndex(paths)
	if len(idx.Sessions) != 1 {
		t.Fatalf("after prune, entries = %d, want 1", len(idx.Sessions))
	}
	if idx.Sessions[0].SessionID != "live" {
		t.Errorf("remaining entry = %q, want live", idx.Sessions[0].SessionID)
	}
}

// TestPruneStaleIndexEntriesRemovesOld verifies that index entries older than
// MaxSessionAge are pruned.
func TestPruneStaleIndexEntriesRemovesOld(t *testing.T) {
	paths := testPaths(t)
	now := time.Now()

	// Add a session whose last event is very old.
	_ = UpdateSessionIndex(paths, indexTestSnapshot(schema.ClientClaudeCode, "old", "m1", schema.SessionCompleted, now.Add(-60*24*time.Hour)))

	// Create its directory so it passes the directory check but fails age.
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "old"); err != nil {
		t.Fatal(err)
	}

	if err := pruneStaleIndexEntries(paths, now); err != nil {
		t.Fatalf("prune: %v", err)
	}

	idx, _ := LoadSessionIndex(paths)
	if len(idx.Sessions) != 0 {
		t.Fatalf("after age prune, entries = %d, want 0", len(idx.Sessions))
	}
}
