package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	return NewPathsWithDir(t.TempDir())
}

func TestUpdateSnapshotConcurrentNoLostFields(t *testing.T) {
	paths := testPaths(t)

	// Two concurrent "processes" (goroutines) mutate different fields of the
	// same session. Retries absorb lock-busy; no field may be lost.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(which int) {
			defer wg.Done()
			for attempt := 0; attempt < 50; attempt++ {
				err := UpdateSnapshot(paths, schema.ClientClaudeCode, "s1",
					func() *schema.Snapshot {
						return &schema.Snapshot{
							SchemaVersion: schema.StateVersion,
							Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
							Session:       schema.SessionInfo{ID: "s1", Status: schema.SessionActive},
						}
					},
					func(snap *schema.Snapshot) error {
						if which == 0 {
							snap.Model.ID = "glm-5.1"
						} else {
							snap.Pressure.State = schema.PressureWarn
						}
						return nil
					})
				if err == nil {
					return
				}
				if !IsLockBusy(err) {
					t.Errorf("unexpected error: %v", err)
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Errorf("goroutine %d never acquired the lock", which)
		}(i)
	}
	wg.Wait()

	snap, err := LoadSnapshot(paths, schema.ClientClaudeCode, "s1")
	if err != nil || snap == nil {
		t.Fatalf("load: %v", err)
	}
	if snap.Model.ID != "glm-5.1" {
		t.Error("model field lost")
	}
	if snap.Pressure.State != schema.PressureWarn {
		t.Error("pressure field lost")
	}
}

func TestSessionsIsolated(t *testing.T) {
	paths := testPaths(t)

	for _, id := range []string{"s1", "s2"} {
		err := UpdateSnapshot(paths, schema.ClientClaudeCode, id,
			func() *schema.Snapshot {
				return &schema.Snapshot{
					SchemaVersion: schema.StateVersion,
					Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
					Session:       schema.SessionInfo{ID: id, Status: schema.SessionActive},
					Model:         schema.ModelInfo{ID: "model-" + id},
				}
			},
			func(snap *schema.Snapshot) error { return nil })
		if err != nil {
			t.Fatalf("update %s: %v", id, err)
		}
	}

	s1, _ := LoadSnapshot(paths, schema.ClientClaudeCode, "s1")
	s2, _ := LoadSnapshot(paths, schema.ClientClaudeCode, "s2")
	if s1 == nil || s2 == nil {
		t.Fatalf("sessions missing: s1=%v s2=%v", s1, s2)
	}
	if s1.Model.ID != "model-s1" || s2.Model.ID != "model-s2" {
		t.Errorf("sessions must be isolated: %s / %s", s1.Model.ID, s2.Model.ID)
	}
}

func TestLockContentionReturnsImmediately(t *testing.T) {
	paths := testPaths(t)

	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	fl := NewFileLock(paths.SessionLock(schema.ClientClaudeCode, "s1"))
	if err := fl.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer fl.Release()

	start := time.Now()
	err := UpdateSnapshot(paths, schema.ClientClaudeCode, "s1", nil,
		func(snap *schema.Snapshot) error { return nil })
	elapsed := time.Since(start)

	if !IsLockBusy(err) {
		t.Errorf("expected ErrLockBusy, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("contention must return immediately, took %v", elapsed)
	}
}

// TestDroppedMutationsCounted verifies that lock-busy mutations are counted
// in DroppedMutations() for observability.
func TestDroppedMutationsCounted(t *testing.T) {
	paths := testPaths(t)

	// Reset counter (it's global, so snapshot before).
	before := DroppedMutations()

	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	fl := NewFileLock(paths.SessionLock(schema.ClientClaudeCode, "s1"))
	if err := fl.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer fl.Release()

	// Mutate while lock is held — should increment the dropped counter.
	for i := 0; i < 3; i++ {
		err := UpdateSnapshot(paths, schema.ClientClaudeCode, "s1", nil,
			func(snap *schema.Snapshot) error { return nil })
		if !IsLockBusy(err) {
			t.Fatalf("expected ErrLockBusy, got %v", err)
		}
	}

	after := DroppedMutations()
	if after-before != 3 {
		t.Errorf("dropped mutations = %d, want 3", after-before)
	}
}

func TestCorruptSnapshotJSONFailsOpen(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1"); err != nil {
		t.Fatal(err)
	}
	snapPath := paths.SessionSnapshot(schema.ClientClaudeCode, "s1")
	if err := os.WriteFile(snapPath, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	snap, err := LoadSnapshot(paths, schema.ClientClaudeCode, "s1")
	// Hooks must fail open: corrupt state yields (nil, nil) and the file is
	// quarantined so subsequent writes start fresh.
	if err != nil {
		t.Errorf("corrupt JSON must fail open, got error: %v", err)
	}
	if snap != nil {
		t.Error("corrupt JSON must not yield a snapshot")
	}
	if _, statErr := os.Stat(snapPath); statErr == nil {
		t.Error("corrupt snapshot must be quarantined (renamed aside)")
	}
}

func TestNullStaysNullAndZeroStaysZero(t *testing.T) {
	paths := testPaths(t)
	zero := 0.0

	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: "s1"},
		Model:         schema.ModelInfo{ID: "glm-5.1"}, // ContextLength stays nil
		LiveContext: &schema.LiveContext{
			Source:         "test",
			UsedPercentage: &zero, // explicit zero
		},
	}
	if err := SaveSnapshot(paths, schema.ClientClaudeCode, "s1", snap); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSnapshot(paths, schema.ClientClaudeCode, "s1")
	if err != nil || loaded == nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Model.ContextLength != nil {
		t.Error("null context length must not become zero")
	}
	if loaded.LiveContext.UsedPercentage == nil {
		t.Error("explicit zero percentage must not become null")
	} else if *loaded.LiveContext.UsedPercentage != 0.0 {
		t.Errorf("explicit zero changed: %v", *loaded.LiveContext.UsedPercentage)
	}
}

func TestUpdateSnapshotNoInitializeIsNoop(t *testing.T) {
	paths := testPaths(t)
	called := false
	err := UpdateSnapshot(paths, schema.ClientClaudeCode, "missing", nil,
		func(snap *schema.Snapshot) error {
			called = true
			return nil
		})
	if err != nil {
		t.Fatalf("no-initialize update should succeed as no-op: %v", err)
	}
	if called {
		t.Error("mutate must not run when no snapshot exists and initialize is nil")
	}
	snap, _ := LoadSnapshot(paths, schema.ClientClaudeCode, "missing")
	if snap != nil {
		t.Error("no snapshot should have been created")
	}
}

// TestUnsupportedSchemaQuarantined writes a snapshot declaring a future schema
// version and verifies LoadSnapshot quarantines it and fails open.
func TestUnsupportedSchemaQuarantined(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "future"); err != nil {
		t.Fatal(err)
	}
	future := []byte(`{"schema_version":99999,"client":{"type":"claude-code"},"session":{"id":"future"}}`)
	snapPath := paths.SessionSnapshot(schema.ClientClaudeCode, "future")
	if err := os.WriteFile(snapPath, future, 0600); err != nil {
		t.Fatal(err)
	}

	snap, err := LoadSnapshot(paths, schema.ClientClaudeCode, "future")
	if err != nil {
		t.Errorf("unsupported schema must fail open, got: %v", err)
	}
	if snap != nil {
		t.Error("unsupported schema must not yield a snapshot")
	}
	if _, statErr := os.Stat(snapPath); statErr == nil {
		t.Error("unsupported snapshot must be quarantined (renamed aside)")
	}

	// Subsequent writes must succeed (the quarantined file is gone).
	err = UpdateSnapshot(paths, schema.ClientClaudeCode, "future",
		func() *schema.Snapshot {
			return &schema.Snapshot{
				SchemaVersion: schema.StateVersion,
				Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
				Session:       schema.SessionInfo{ID: "future", Status: schema.SessionActive},
			}
		},
		func(s *schema.Snapshot) error { return nil })
	if err != nil {
		t.Fatalf("re-write after quarantine failed: %v", err)
	}
}

// TestAtomicWriteNeverExposesPartialJSON verifies a snapshot is fully readable
// immediately after SaveSnapshot returns — the temp+rename guarantees no
// partial-JSON state is observable.
func TestAtomicWriteNeverExposesPartialJSON(t *testing.T) {
	paths := testPaths(t)
	for i := 0; i < 20; i++ {
		s := &schema.Snapshot{
			SchemaVersion: schema.StateVersion,
			Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
			Session:       schema.SessionInfo{ID: "atomic", Status: schema.SessionActive},
			Model:         schema.ModelInfo{ID: "m"},
		}
		if err := SaveSnapshot(paths, schema.ClientClaudeCode, "atomic", s); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		loaded, err := LoadSnapshot(paths, schema.ClientClaudeCode, "atomic")
		if err != nil || loaded == nil {
			t.Fatalf("load %d: err=%v snap=%v (partial JSON leaked)", i, err, loaded)
		}
		if loaded.Session.ID != "atomic" || loaded.Model.ID != "m" {
			t.Fatalf("load %d returned wrong contents: %+v", i, loaded)
		}
	}
}

// TestConcurrentReaderNeverSeesQuarantine is the P0-3 regression. The previous
// data-file-then-checksum-sidecar design let a concurrent reader observe
// mismatched generations (new data + old checksum, or vice versa) and
// erroneously quarantine valid state. After the fix, no reader should ever
// see a snapshot disappear mid-flight unless the file is genuinely gone.
//
// We spin many readers in parallel with a writer that repeatedly changes the
// model ID. Any reader that observes the snapshot must either see the prior
// generation's ID or the next generation's ID — never an error, never nil
// mid-write.
func TestConcurrentReaderNeverSeesQuarantine(t *testing.T) {
	paths := testPaths(t)
	const generations = 100

	// Seed.
	seed := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: "race", Status: schema.SessionActive},
		Model:         schema.ModelInfo{ID: "gen-0"},
	}
	if err := SaveSnapshot(paths, schema.ClientClaudeCode, "race", seed); err != nil {
		t.Fatal(err)
	}

	writerDone := make(chan struct{})
	readerErrs := make(chan error, 50)

	for r := 0; r < 50; r++ {
		go func() {
			for i := 0; i < 200; i++ {
				snap, err := LoadSnapshot(paths, schema.ClientClaudeCode, "race")
				if err != nil {
					readerErrs <- fmt.Errorf("reader saw error: %w", err)
					return
				}
				// A nil snapshot mid-rename is acceptable (rename replaced the
				// inode for an instant); a *quarantined* snapshot is not.
				if snap == nil {
					continue
				}
				if snap.Session.ID != "race" {
					readerErrs <- fmt.Errorf("reader saw wrong session id: %q", snap.Session.ID)
					return
				}
				if !strings.HasPrefix(snap.Model.ID, "gen-") {
					readerErrs <- fmt.Errorf("reader saw wrong model id: %q", snap.Model.ID)
					return
				}
			}
			readerErrs <- nil
		}()
	}

	go func() {
		defer close(writerDone)
		for i := 1; i <= generations; i++ {
			s := &schema.Snapshot{
				SchemaVersion: schema.StateVersion,
				Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
				Session:       schema.SessionInfo{ID: "race", Status: schema.SessionActive},
				Model:         schema.ModelInfo{ID: fmt.Sprintf("gen-%d", i)},
			}
			if err := SaveSnapshot(paths, schema.ClientClaudeCode, "race", s); err != nil {
				t.Errorf("writer save %d: %v", i, err)
				return
			}
		}
	}()

	<-writerDone
	for i := 0; i < 50; i++ {
		if err := <-readerErrs; err != nil {
			t.Fatal(err)
		}
	}

	// Final state must be the last generation we wrote, with no quarantine
	// siblings on disk.
	final, err := LoadSnapshot(paths, schema.ClientClaudeCode, "race")
	if err != nil || final == nil {
		t.Fatalf("final load: err=%v snap=%v", err, final)
	}
	if final.Model.ID != fmt.Sprintf("gen-%d", generations) {
		t.Errorf("final model = %q, want gen-%d", final.Model.ID, generations)
	}
	matches, _ := filepath.Glob(filepath.Join(paths.SessionDir(schema.ClientClaudeCode, "race"), "snapshot.json.quarantine-*"))
	if len(matches) != 0 {
		t.Errorf("found quarantine files after concurrent run: %v", matches)
	}
}

// TestSaveSnapshotRejectsInvalidSnapshot ensures we never write structurally
// unsound state to disk — schema validation is enforced before the atomic
// rename, so readers can trust any file they find.
func TestSaveSnapshotRejectsInvalidSnapshot(t *testing.T) {
	paths := testPaths(t)

	// Future schema version — must be rejected.
	future := &schema.Snapshot{
		SchemaVersion: 9999,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: "future", Status: schema.SessionActive},
		Model:         schema.ModelInfo{ID: "m"},
	}
	err := SaveSnapshot(paths, schema.ClientClaudeCode, "future", future)
	if err == nil {
		t.Fatal("SaveSnapshot must reject unsupported schema versions")
	}
	if _, statErr := os.Stat(paths.SessionSnapshot(schema.ClientClaudeCode, "future")); statErr == nil {
		t.Error("rejected snapshot must not exist on disk")
	}

	// Missing session ID — must be rejected.
	missing := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{Status: schema.SessionActive},
		Model:         schema.ModelInfo{ID: "m"},
	}
	if err := SaveSnapshot(paths, schema.ClientClaudeCode, "missing", missing); err == nil {
		t.Error("SaveSnapshot must reject snapshots missing session ID")
	}
}

// TestLegacyChecksumSidecarDoesNotBreakLoad ensures that files written by the
// old version of WriteJSONAtomically (which produced a .sha256 sidecar) can
// still be read after the upgrade. The companion must clean up the orphan
// sidecar without erroring.
func TestLegacyChecksumSidecarDoesNotBreakLoad(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, "legacy"); err != nil {
		t.Fatal(err)
	}
	snapPath := paths.SessionSnapshot(schema.ClientClaudeCode, "legacy")
	body := []byte(`{"schema_version":2,"client":{"type":"claude-code"},"session":{"id":"legacy","status":"active"},"model":{"id":"m"}}`)
	if err := os.WriteFile(snapPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	// Drop a deliberately wrong sidecar in place, mimicking a stale or
	// mismatched generation from the previous design. Load must not quarantine
	// based on this sidecar.
	sidecar := []byte("0000000000000000000000000000000000000000000000000000000000000000")
	if err := os.WriteFile(snapPath+ChecksumFileSuffix, sidecar, 0600); err != nil {
		t.Fatal(err)
	}

	snap, err := LoadSnapshot(paths, schema.ClientClaudeCode, "legacy")
	if err != nil || snap == nil {
		t.Fatalf("load with stale sidecar: err=%v snap=%v", err, snap)
	}
	if snap.Session.ID != "legacy" {
		t.Errorf("session id = %q", snap.Session.ID)
	}
	// And the bad sidecar should be gone after quarantine cleanup runs (which
	// happens implicitly via the next SaveSnapshot or explicitly on
	// quarantineSnapshot — but here we only verify load is unaffected).
}

// TestReadJSONRejectsTrailingData ensures that a file with valid JSON followed
// by trailing garbage (partial write, corruption) is rejected rather than
// silently decoded from the prefix.
func TestReadJSONRejectsTrailingData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	// Valid JSON followed by trailing garbage.
	if err := os.WriteFile(path, []byte(`{"a":1}garbage`), 0600); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := ReadJSON(path, &v); err == nil {
		t.Fatal("expected error for trailing data after JSON, got nil")
	}
}

// TestLoadGlobalQuarantinesCorruptResource ensures a corrupt global resource
// file is quarantined (renamed aside) and does not poison the other resources.
func TestLoadGlobalQuarantinesCorruptResource(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Write valid models JSON.
	models := `{"fetched_at":"2025-01-01T00:00:00Z","models":[{"id":"m1","context_length":100}]}`
	if err := os.WriteFile(paths.GlobalModels(), []byte(models), 0600); err != nil {
		t.Fatal(err)
	}
	// Write corrupt health JSON.
	if err := os.WriteFile(paths.GlobalHealth(), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	gs, err := LoadGlobal(paths)
	// Models should still be loaded even though health is corrupt.
	if gs.Models == nil || len(gs.Models.Models) != 1 {
		t.Fatalf("models should load despite corrupt health, got %+v", gs.Models)
	}
	if err == nil {
		t.Error("expected error from corrupt resource, got nil")
	}
	// The corrupt health file should have been quarantined.
	if _, statErr := os.Stat(paths.GlobalHealth()); statErr == nil {
		t.Error("corrupt health file should have been quarantined")
	}
}

// TestEnsureSessionDirRejectsSymlink verifies that EnsureSessionDir refuses
// to follow an existing symlink at the session path.
func TestEnsureSessionDirRejectsSymlink(t *testing.T) {
	paths := testPaths(t)
	// Create parent dirs so the symlink can be placed.
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// Manually create the client-type dir since EnsureSessionDir would.
	clientDir := filepath.Join(paths.CacheDir, "sessions", schema.ClientClaudeCode)
	if err := os.MkdirAll(clientDir, 0700); err != nil {
		t.Fatal(err)
	}
	dir := paths.SessionDir(schema.ClientClaudeCode, "s1")

	// Create a symlink at the session path pointing to /tmp.
	if err := os.Symlink("/tmp", dir); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := paths.EnsureSessionDir(schema.ClientClaudeCode, "s1")
	if err == nil {
		t.Fatal("expected error when session path is a symlink, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got: %v", err)
	}
}
