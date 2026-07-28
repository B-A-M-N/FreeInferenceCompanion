package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

const (
	DefaultCacheDir   = ".cache/freeinference-companion"
	DefaultConfigFile = "config.toml"
)

// ChecksumFileSuffix is appended to cache files to store their SHA-256 checksum.
const ChecksumFileSuffix = ".sha256"

// Paths resolves filesystem paths for state storage.
type Paths struct {
	CacheDir string
}

// NewPaths creates a Paths rooted at the default cache directory under HOME.
// Respects FI_CACHE_DIR environment variable if set.
func NewPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("home dir: %w", err)
	}
	cacheDir := os.Getenv("FI_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = filepath.Join(home, DefaultCacheDir)
	}
	return Paths{
		CacheDir: cacheDir,
	}, nil
}

// NewPathsWithDir creates a Paths with an explicit cache directory.
func NewPathsWithDir(cacheDir string) Paths {
	return Paths{CacheDir: cacheDir}
}

// sessionKey returns a SHA-256 hash of the session ID to prevent
// path traversal attacks when session IDs contain directory separators.
func sessionKey(sessionID string) string {
	h := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(h[:])
}

// SessionDir returns the directory for a given client type and session ID.
func (p Paths) SessionDir(clientType, sessionID string) string {
	return filepath.Join(p.CacheDir, "sessions", clientType, sessionKey(sessionID))
}

// SessionSnapshot returns the path to the per-session snapshot.json.
func (p Paths) SessionSnapshot(clientType, sessionID string) string {
	return filepath.Join(p.SessionDir(clientType, sessionID), "snapshot.json")
}

// SessionEvents returns the path to the per-session events.jsonl.
func (p Paths) SessionEvents(clientType, sessionID string) string {
	return filepath.Join(p.SessionDir(clientType, sessionID), "events.jsonl")
}

// SessionLock returns the path to the per-session advisory lock.
func (p Paths) SessionLock(clientType, sessionID string) string {
	return filepath.Join(p.SessionDir(clientType, sessionID), "lock")
}

// GlobalDir returns the global cache directory.
func (p Paths) GlobalDir() string {
	return filepath.Join(p.CacheDir, "global")
}

// GlobalHealth returns the path to the cached health data.
func (p Paths) GlobalHealth() string {
	return filepath.Join(p.GlobalDir(), "health.json")
}

// GlobalModels returns the path to the cached model catalog.
func (p Paths) GlobalModels() string {
	return filepath.Join(p.GlobalDir(), "models.json")
}

// GlobalAccountUsage returns the path to the cached account usage.
func (p Paths) GlobalAccountUsage() string {
	return filepath.Join(p.GlobalDir(), "account-usage.json")
}

// GlobalCircuitBreakersLock returns the path to the circuit breaker state lock.
func (p Paths) GlobalCircuitBreakersLock() string {
	return filepath.Join(p.GlobalDir(), "circuit-breakers.lock")
}

// GlobalCircuitBreakers returns the path to the circuit breaker state.
func (p Paths) GlobalCircuitBreakers() string {
	return filepath.Join(p.GlobalDir(), "circuit-breakers.json")
}

// GlobalSessionIndex returns the path to the session index.
func (p Paths) GlobalSessionIndex() string {
	return filepath.Join(p.GlobalDir(), "sessions.json")
}

// GlobalSessionIndexLock returns the path to the session index lock.
func (p Paths) GlobalSessionIndexLock() string {
	return filepath.Join(p.GlobalDir(), "sessions.lock")
}

// RefreshLock returns the cross-process lock path for a refresh worker
// (e.g. "models", "health").
func (p Paths) RefreshLock(worker string) string {
	return filepath.Join(p.GlobalDir(), "refresh-"+worker+".lock")
}

// EnsureDirs creates all required directories under the cache root.
// Uses 0700 permissions for security (only the user can access session data).
func (p Paths) EnsureDirs() error {
	dirs := []string{
		p.CacheDir,
		p.GlobalDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// EnsureSessionDir creates the per-session directory with restricted permissions.
func (p Paths) EnsureSessionDir(clientType, sessionID string) error {
	return os.MkdirAll(p.SessionDir(clientType, sessionID), 0700)
}

// ============================================================
// Atomic file operations
// ============================================================

// WriteJSONAtomically writes v as JSON to path using a temp file + rename.
// The temp file is created in the same directory so rename is atomic.
// Sets file permissions to 0600 (owner read/write only).
//
// Durability note: this function does not call fsync on the temp file or the
// parent directory. Atomic rename protects against concurrent readers and
// against process termination, but a hard power loss could lose the most
// recent write. The companion's state is best-effort by design (hooks fail
// open), so we prioritize latency over crash durability. If durability is
// later required, add fsync of the temp file and parent dir before rename.
//
// Integrity note: this function previously wrote a SHA-256 checksum sidecar
// and readers verified it before decoding. That design was unsound under
// concurrent read/write because the data file and the checksum file could
// not be renamed together atomically — a reader could observe new data with
// the previous checksum (or vice versa) and falsely conclude the file was
// corrupt, quarantining valid state. Integrity is now enforced by schema
// validation on LoadSnapshot instead, which is strict and race-free.
func WriteJSONAtomically(path string, v any) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "tmp-*.json")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// ReadJSON reads and decodes JSON from path into v.
// Returns os.ErrNotExist if the file does not exist.
func ReadJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

// ============================================================
// Session state load/save
// ============================================================

// LoadSnapshot reads the per-session snapshot. Returns nil without error if no snapshot exists.
// On corrupt or unsupported state, the file is quarantined (renamed) and
// (nil, nil) is returned so hooks continue with no state — they never block.
func LoadSnapshot(paths Paths, clientType, sessionID string) (*schema.Snapshot, error) {
	path := paths.SessionSnapshot(clientType, sessionID)
	var s schema.Snapshot
	if err := ReadJSON(path, &s); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		// JSON or checksum failure → quarantine so future writes succeed.
		quarantineSnapshot(path, schema.QuarantineReason(err))
		return nil, nil
	}
	if err := schema.MigrateSnapshot(&s); err != nil {
		quarantineSnapshot(path, schema.QuarantineReason(err))
		return nil, nil
	}
	if err := schema.ValidateSnapshot(&s); err != nil {
		quarantineSnapshot(path, schema.QuarantineReason(err))
		return nil, nil
	}
	return &s, nil
}

// quarantineSnapshot renames a corrupt snapshot aside so subsequent writes
// start fresh. The quarantine sibling is best-effort: any error is ignored
// because the caller already chose to fail open.
func quarantineSnapshot(path, reason string) {
	if path == "" {
		return
	}
	dst := path + ".quarantine-" + reason
	_ = os.Rename(path, dst)
	// Legacy checksum sidecars are no longer written, but old installs may
	// still have one. Clean it up so it cannot mask a fresh write later.
	_ = os.Remove(path + ChecksumFileSuffix)
}

// SaveSnapshot writes the per-session snapshot atomically with restricted permissions.
// The snapshot is validated against the current schema before it is written so
// that structurally unsound mutations can never land on disk — readers can
// trust that any file present is at least shape-valid, and quarantine logic
// only runs for genuinely unexpected corruption (truncation, mid-write crash,
// hand-edited files).
func SaveSnapshot(paths Paths, clientType, sessionID string, s *schema.Snapshot) error {
	if err := schema.ValidateSnapshot(s); err != nil {
		return fmt.Errorf("validate snapshot before save: %w", err)
	}
	if err := paths.EnsureSessionDir(clientType, sessionID); err != nil {
		return err
	}
	path := paths.SessionSnapshot(clientType, sessionID)
	return WriteJSONAtomically(path, s)
}

// UpdateSnapshot applies a mutation to the per-session snapshot under a
// non-blocking cross-process advisory lock. All session mutations must go
// through this function so concurrent hook/status-line processes cannot
// lose each other's changes.
//
// If no snapshot exists and initialize is non-nil, a new snapshot is created
// via initialize() and then mutated. If no snapshot exists and initialize is
// nil, UpdateSnapshot is a no-op and returns nil.
//
// Returns ErrLockBusy when another process holds the session lock; callers
// should treat that as fail-open.
func UpdateSnapshot(paths Paths, clientType, sessionID string, initialize func() *schema.Snapshot, mutate func(*schema.Snapshot) error) error {
	lockPath := paths.SessionLock(clientType, sessionID)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return err
	}
	fl := NewFileLock(lockPath)
	if err := fl.Acquire(); err != nil {
		return err
	}
	defer fl.Release()

	snap, err := LoadSnapshot(paths, clientType, sessionID)
	if err != nil {
		return err
	}
	if snap == nil {
		if initialize == nil {
			return nil
		}
		snap = initialize()
		if snap == nil {
			return nil
		}
	}
	if err := mutate(snap); err != nil {
		return err
	}
	if err := SaveSnapshot(paths, clientType, sessionID, snap); err != nil {
		return err
	}
	// Best-effort index update; never fail the mutation over indexing.
	_ = UpdateSessionIndex(paths, snap)
	return nil
}

// ============================================================
// Global state load/save
// ============================================================

// LoadGlobal reads the global state. Each resource is loaded independently —
// a corrupt or unreadable file for one resource does not poison the others.
// Missing files are silently skipped (the corresponding field stays nil).
// JSON decode errors for a single resource are logged to diagnostics but do
// not prevent the remaining resources from loading.
func LoadGlobal(paths Paths) (*schema.GlobalState, error) {
	gs := &schema.GlobalState{}

	// Each resource loads independently. A failure on one does not prevent
	// the others from being read. This means a corrupt models file will
	// not discard valid circuit-breaker state.

	var loadErr error
	if err := ReadJSON(paths.GlobalHealth(), &gs.Health); err != nil && !os.IsNotExist(err) {
		loadErr = err
	}
	if err := ReadJSON(paths.GlobalModels(), &gs.Models); err != nil && !os.IsNotExist(err) {
		loadErr = err
	}
	if err := ReadJSON(paths.GlobalAccountUsage(), &gs.AccountUsage); err != nil && !os.IsNotExist(err) {
		loadErr = err
	}
	if err := ReadJSON(paths.GlobalCircuitBreakers(), &gs.CircuitBreakers); err != nil && !os.IsNotExist(err) {
		loadErr = err
	}
	return gs, loadErr
}

// SaveHealth writes the health cache atomically.
func SaveHealth(paths Paths, h *schema.HealthCache) error {
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	return WriteJSONAtomically(paths.GlobalHealth(), h)
}

// SaveModels writes the models cache atomically.
func SaveModels(paths Paths, m *schema.ModelsCache) error {
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	return WriteJSONAtomically(paths.GlobalModels(), m)
}

// SaveAccountUsage writes the account usage cache atomically.
func SaveAccountUsage(paths Paths, a *schema.AccountUsage) error {
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	return WriteJSONAtomically(paths.GlobalAccountUsage(), a)
}

// SaveCircuitBreakers writes the circuit breaker state atomically.
func SaveCircuitBreakers(paths Paths, cbs []schema.CircuitBreaker) error {
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	return WriteJSONAtomically(paths.GlobalCircuitBreakers(), cbs)
}

// UpdateCircuitBreakers applies a mutation to the circuit breaker state under a
// cross-process lock. This prevents the lost-update race where two workers
// (models and health) both read-modify-write the same circuit-breakers file
// concurrently. The lock is held for the duration of the mutation. Unlike the
// session locks (which are non-blocking because they serve hooks), this lock
// blocks briefly — it is only called from background refreshers, where a short
// wait is acceptable to guarantee no updates are dropped.
func UpdateCircuitBreakers(paths Paths, mutate func(cbs []schema.CircuitBreaker) ([]schema.CircuitBreaker, error)) error {
	lockPath := paths.GlobalCircuitBreakersLock()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return err
	}
	// Blocking acquire: background workers can wait; hooks never call this.
	fl := NewFileLock(lockPath)
	if err := fl.AcquireBlocking(); err != nil {
		return err
	}
	defer fl.Release()

	var cbs []schema.CircuitBreaker
	if err := ReadJSON(paths.GlobalCircuitBreakers(), &cbs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load circuit breakers: %w", err)
	}

	updated, err := mutate(cbs)
	if err != nil {
		return err
	}
	return SaveCircuitBreakers(paths, updated)
}
