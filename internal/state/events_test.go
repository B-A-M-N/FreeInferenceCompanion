package state

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func TestAppendEventWritesSanitizedLine(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("Q", MaxDetailLen+50)
	if err := AppendEvent(paths, schema.ClientClaudeCode, "s1",
		Event{Type: EventSessionStarted, Detail: long}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.SessionEvents(schema.ClientClaudeCode, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "session_started") {
		t.Errorf("missing event type: %s", data)
	}
	// Truncation replaces the tail with "...". The untruncated run of Q's
	// would be MaxDetailLen+50 long; the persisted run must be shorter.
	if strings.Contains(string(data), strings.Repeat("Q", MaxDetailLen+1)) {
		t.Errorf("detail not truncated: still contains a run of %d Q's", MaxDetailLen+1)
	}
}

func TestAppendEventRejectsUnknownType(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(paths, schema.ClientClaudeCode, "s1",
		Event{Type: "prompt_text_leaked"}); err == nil {
		t.Error("unknown event type must be rejected")
	}
}

func TestEventPathsRejectUnknownClientWithoutCreatingDirectories(t *testing.T) {
	paths := testPaths(t)
	if err := AppendEvent(paths, "attacker", "s1", Event{Type: EventSessionStarted}); err == nil {
		t.Fatal("unknown client must be rejected")
	}
	if err := RotateEvents(paths, "attacker", "s1"); err == nil {
		t.Fatal("unknown client rotation must be rejected")
	}
	if _, err := os.Stat(paths.CacheDir + "/sessions"); !os.IsNotExist(err) {
		t.Fatalf("invalid client created cache directories: %v", err)
	}
}

func TestReadEventsRejectsOversizedLog(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	path := paths.SessionEvents(schema.ClientClaudeCode, "s1")
	if err := os.WriteFile(path, make([]byte, MaxEventBytesPerSession+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvents(paths, schema.ClientClaudeCode, "s1", 0); err == nil {
		t.Fatal("oversized event log must be rejected")
	}
}

func TestReadEventsReturnsChronological(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	for _, ty := range []string{EventSessionStarted, EventPromptSubmitted, EventTurnStopped} {
		if err := AppendEvent(paths, schema.ClientClaudeCode, "s1", Event{Type: ty}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := ReadEvents(paths, schema.ClientClaudeCode, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	if events[0].Type != EventSessionStarted || events[2].Type != EventTurnStopped {
		t.Errorf("order wrong: %v", events)
	}
}

// TestReadEventsMaxNewestFirst verifies that ReadEvents with max > 0 always
// returns newest-first, even when the file contains fewer events than max.
func TestReadEventsMaxNewestFirst(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	for _, ty := range []string{EventSessionStarted, EventPromptSubmitted, EventTurnStopped} {
		if err := AppendEvent(paths, schema.ClientClaudeCode, "s1", Event{Type: ty}); err != nil {
			t.Fatal(err)
		}
	}
	// max=10 but only 3 events exist — must still reverse.
	events, err := ReadEvents(paths, schema.ClientClaudeCode, "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	if events[0].Type != EventTurnStopped || events[2].Type != EventSessionStarted {
		t.Errorf("expected newest-first, got %v", events)
	}
}

func TestRotateEventsBoundsSize(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	// Write more lines than MaxEventsPerSession and force the file size past
	// the byte bound by giving each event a sizeable detail.
	for i := 0; i < MaxEventsPerSession+50; i++ {
		if err := AppendEvent(paths, schema.ClientClaudeCode, "s1",
			Event{Type: EventPromptSubmitted, Detail: strings.Repeat("a", 300)}); err != nil {
			t.Fatal(err)
		}
	}
	info, _ := os.Stat(paths.SessionEvents(schema.ClientClaudeCode, "s1"))
	if info.Size() < MaxEventBytesPerSession {
		t.Fatalf("precondition: file too small (%d bytes)", info.Size())
	}
	if err := RotateEvents(paths, schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	events, _ := ReadEvents(paths, schema.ClientClaudeCode, "s1", 0)
	if len(events) > MaxEventsPerSession {
		t.Errorf("rotation must cap line count: have %d", len(events))
	}
}

func TestCleanupStaleSessions(t *testing.T) {
	paths := testPaths(t)
	// Create an old session.
	if err := UpdateSnapshot(paths, schema.ClientClaudeCode, "old",
		func() *schema.Snapshot {
			return &schema.Snapshot{
				SchemaVersion: schema.StateVersion,
				Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
				Session:       schema.SessionInfo{ID: "old", Status: schema.SessionActive},
			}
		},
		func(s *schema.Snapshot) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// Force every file inside the session dir to look old.
	oldDir := paths.SessionDir(schema.ClientClaudeCode, "old")
	cutoff := time.Now().Add(-(MaxSessionAge + time.Hour))
	_ = markDirOld(oldDir, cutoff)

	// And a fresh session.
	if err := UpdateSnapshot(paths, schema.ClientClaudeCode, "fresh",
		func() *schema.Snapshot {
			return &schema.Snapshot{
				SchemaVersion: schema.StateVersion,
				Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
				Session:       schema.SessionInfo{ID: "fresh", Status: schema.SessionActive},
			}
		},
		func(s *schema.Snapshot) error { return nil }); err != nil {
		t.Fatal(err)
	}

	if err := CleanupStaleSessions(paths, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("stale session must be removed, got stat err: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir(schema.ClientClaudeCode, "fresh")); err != nil {
		t.Errorf("fresh session must survive, got stat err: %v", err)
	}
}

// markDirOld sets mtime on the directory and every file inside it.
func markDirOld(dir string, t time.Time) error {
	if err := os.Chtimes(dir, t, t); err != nil {
		return err
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.Chtimes(dir+"/"+e.Name(), t, t)
	}
	return nil
}

// TestEventAppendNonblocking verifies that AppendEvent does not block when the
// event lock is held — it drops the event and returns ErrLockBusy immediately.
// This is the hook-latency contract: a stalled lock holder must not stall a hook.
func TestEventAppendNonblocking(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}

	// Hold the event lock from this process.
	eventPath := paths.SessionEvents(schema.ClientClaudeCode, "s1")
	lockPath := eventPath + ".lock"
	fl := NewFileLock(lockPath)
	if err := fl.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer fl.Release()

	// AppendEvent must return immediately with ErrLockBusy, not block.
	start := time.Now()
	err := AppendEvent(paths, schema.ClientClaudeCode, "s1",
		Event{Type: EventPromptSubmitted})
	elapsed := time.Since(start)

	if !IsLockBusy(err) {
		t.Errorf("expected ErrLockBusy, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("AppendEvent blocked for %v, must return immediately", elapsed)
	}
	if DroppedEvents() < 1 {
		t.Errorf("droppedEvents = %d, want >= 1", DroppedEvents())
	}
}

// TestConcurrentEventAppend verifies that concurrent appends from multiple
// goroutines do not lose data and that rotation does not block.
func TestConcurrentEventAppend(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}

	before := DroppedEvents()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = AppendEvent(paths, schema.ClientClaudeCode, "s1",
					Event{Type: EventPromptSubmitted})
			}
		}()
	}
	wg.Wait()

	events, err := ReadEvents(paths, schema.ClientClaudeCode, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Some events may be dropped under lock contention, but the total
	// (persisted + dropped) must equal 500.
	dropped := DroppedEvents() - before
	if int64(len(events))+dropped != 500 {
		t.Errorf("events(%d) + dropped(%d) = %d, want 500", len(events), dropped, int64(len(events))+dropped)
	}
}
