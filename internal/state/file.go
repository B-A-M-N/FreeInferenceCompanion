package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

const (
	DefaultCacheDir   = ".cache/freeinference-companion"
	DefaultConfigFile = "config.toml"
)

// ChecksumFileSuffix is appended to cache files to store their SHA-256 checksum.
const ChecksumFileSuffix = ".sha256"

// droppedMutations counts session-snapshot mutations that were skipped because
// the per-session lock was held by another process (ErrLockBusy). Exposed via
// DroppedMutations() for observability. Under contention this counter rises;
// in normal single-client operation it stays at zero.
var droppedMutations int64

// DroppedMutations returns the count of snapshot mutations dropped due to lock
// contention since process start. A rising count under load indicates that the
// nonblocking lock is discarding updates; the companion's fail-open contract
// means these are not correctness bugs but observability gaps.
func DroppedMutations() int64 {
	return atomic.LoadInt64(&droppedMutations)
}

// Paths resolves filesystem paths for state storage.
type Paths struct {
	CacheDir     string
	ActivationID string // provider identity; empty for session-only paths
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

// NewNamespacedPaths returns a copy of p with ActivationID set. Global
// state paths (health, models, account-usage, circuit-breakers, session
// index) are placed under providers/<activationID>/global/ so that
// different endpoints/credentials never share state. Session state
// remains on the unnamespaced path because sessions are independent of
// which provider runtime is active.
func (p Paths) NewNamespacedPaths(activationID string) Paths {
	return Paths{
		CacheDir:     p.CacheDir,
		ActivationID: activationID,
	}
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

// GlobalDir returns the global cache directory, namespaced by activation ID
// when present. When ActivationID is empty, the legacy (unnamespaced) path
// is returned for backward compatibility.
func (p Paths) GlobalDir() string {
	if p.ActivationID != "" {
		return filepath.Join(p.CacheDir, "providers", p.ActivationID, "global")
	}
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

// SessionIndexDir returns the directory for the session index. This is stored
// in a fixed (unnamespaced) location so that session discovery works
// regardless of the current activation state. Provider-level state
// (health, models, circuit-breakers) is namespaced; sessions are not.
func (p Paths) SessionIndexDir() string {
	return filepath.Join(p.CacheDir, "sessions-index")
}

// GlobalSessionIndex returns the path to the session index.
func (p Paths) GlobalSessionIndex() string {
	return filepath.Join(p.SessionIndexDir(), "sessions.json")
}

// GlobalSessionIndexLock returns the path to the session index lock.
func (p Paths) GlobalSessionIndexLock() string {
	return filepath.Join(p.SessionIndexDir(), "sessions.lock")
}

// RefreshLock returns the cross-process lock path for a refresh worker
// (e.g. "models", "health").
func (p Paths) RefreshLock(worker string) string {
	return filepath.Join(p.GlobalDir(), "refresh-"+worker+".lock")
}

// EnsureDirs creates all required directories under the cache root.
// Uses 0700 permissions for security (only the user can access session data).
// Rejects symlinks to prevent symlink-following attacks where an attacker
// could redirect state writes to an unintended location.
//
// When ActivationID is set, global state (health, models, etc.) is placed
// under providers/<id>/global/. When unset, it falls back to the legacy
// global/ directory for backward compatibility.
func (p Paths) EnsureDirs() error {
	dirs := []string{
		p.CacheDir,
		p.SessionIndexDir(),
		p.GlobalDir(),
	}
	for _, d := range dirs {
		if err := ensureSecureDirAll(d); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// EnsureGlobalDir creates the global cache directory if it does not already
// exist. Uses 0700 permissions and validates the full path against symlinks
// and other hostile entries. This is used by index/global writers that need
// the global directory without creating the full cache tree.
func (p Paths) EnsureGlobalDir() error {
	if err := ensureSecureDirAll(p.GlobalDir()); err != nil {
		return fmt.Errorf("mkdir %s: %w", p.GlobalDir(), err)
	}
	return nil
}

// EnsureSessionDir creates the per-session directory with restricted permissions.
// Creates the full path tree (sessions/<clientType>/<sessionKey>) and validates
// every component against symlinks and hostile entries.
func (p Paths) EnsureSessionDir(clientType, sessionID string) error {
	dir := p.SessionDir(clientType, sessionID)
	if err := ensureSecureDirAll(dir); err != nil {
		return err
	}
	return nil
}

// ensureSecureDirAll creates the full directory tree at dir, validating every
// component from the leaf up to the first existing ancestor. Each created
// directory uses 0700 permissions. Symlinks, device nodes, sockets, and other
// unexpected file types are rejected at every level.
//
// This is the trusted creator for top-level directories (cache root, global)
// where the full path tree may need to be created. For leaf directories inside
// already-validated trees, use ensureSecureDir.
func ensureSecureDirAll(dir string) error {
	dir = filepath.Clean(dir)

	// Check for an existing entry at the target.
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to follow symlink at %s", dir)
		}
		if info.IsDir() {
			return os.Chmod(dir, 0700)
		}
		return fmt.Errorf("path exists and is not a directory: %s", dir)
	}

	// Walk the expected path (it doesn't exist yet), validating that every
	// existing ancestor is safe.
	if err := walkAndValidatePath(dir); err != nil {
		return err
	}

	// Now create the full tree — all parents are validated as safe.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Enforce 0700 even if MkdirAll skipped an existing ancestor with laxer mode.
	return os.Chmod(dir, 0700)
}

// ensureSecureDir creates a directory after verifying no symlink exists at
// the target path. If a symlink is found, it is NOT followed — the error is
// returned. If the directory already exists and is not a symlink, it is a no-op.
//
// Security contract:
// walkAndValidatePath checks every component of abs from leaf to the first
// existing ancestor. Each component is Lstat'd — symlinks, device nodes,
// sockets, and pipes are all rejected. The trusted root itself is excluded
// from the walk (it is assumed to be created by EnsureDirs from a known path).
//
// This prevents os.MkdirAll / os.Mkdir from silently following a symlink in a
// parent directory, which would redirect state writes outside the cache root.
func walkAndValidatePath(dir string) error {
	original := dir
	for {
		info, err := os.Lstat(dir)
		if err != nil {
			// Not found — stop walking and the remaining path components will be
			// created by Mkdir. This is the normal case: we walk up from the
			// leaf until we hit an existing ancestor.
			break
		}
		// Reject symlinks at every level.
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to follow symlink in path: %s", dir)
		}
		// Reject device nodes, sockets, named pipes.
		if info.Mode()&os.ModeDevice != 0 {
			return fmt.Errorf("refusing path containing device node: %s", dir)
		}
		if info.Mode()&os.ModeType > 0 && !info.IsDir() {
			return fmt.Errorf("refusing path containing non-directory entry: %s (%s)", dir, info.Mode())
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			break
		}
		dir = parent
	}
	// Now walk back down from the trusted root to validate any intermediate
	// components that the upward walk may have skipped because they didn't
	// exist. We walk from the cache root down to the parent of the target,
	// checking each component along the way.
	//
	// First find the trusted root (the first existing ancestor of original).
	cacheRoot := original
	for {
		info, err := os.Lstat(cacheRoot)
		if err != nil {
			// Not found — walk to parent.
			parent := filepath.Dir(cacheRoot)
			if parent == cacheRoot {
				// Reached filesystem root without finding an existing ancestor.
				// The full tree will be created; the upward walk already
				// validated any existing components.
				return nil
			}
			cacheRoot = parent
			continue
		}
		if info.IsDir() {
			break
		}
		parent := filepath.Dir(cacheRoot)
		if parent == cacheRoot {
			return nil
		}
		cacheRoot = parent
	}
	// Walk from cacheRoot down to the parent of the target directory.
	// Every component on this path is checked for symlinks.
	targetParent := filepath.Dir(filepath.Clean(original))
	if strings.HasPrefix(targetParent, cacheRoot) || targetParent == cacheRoot {
		// Walk each component from cacheRoot down to targetParent.
		rest := targetParent[len(cacheRoot):]
		rest = strings.TrimLeft(rest, "/")
		components := strings.Split(rest, "/")
		check := cacheRoot
		for i, comp := range components {
			if comp == "" {
				continue
			}
			check = filepath.Join(check, comp)
			info, err := os.Lstat(check)
			if err != nil {
				// Component doesn't exist yet — stop checking here.
				// Remaining components will be created by MkdirAll.
				break
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to follow symlink in path: %s", check)
			}
			if info.Mode()&os.ModeDevice != 0 {
				return fmt.Errorf("refusing path containing device node: %s", check)
			}
			if i == len(components)-1 {
				// Last component — should be a directory.
				if !info.IsDir() {
					return fmt.Errorf("refusing path containing non-directory entry: %s (%s)", check, info.Mode())
				}
			}
			// Not the last component — must be a directory to continue walking.
			if i < len(components)-1 && !info.IsDir() {
				return fmt.Errorf("refusing path containing non-directory entry: %s (%s)", check, info.Mode())
			}
		}
	}
	return nil
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
// Rejects files with trailing garbage after the JSON value (likely a
// partial write or corruption) — the decode will not reach EOF.
func ReadJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Verify there is no trailing non-whitespace content. A truncated file
	// or one with appended garbage from a failed write would otherwise
	// decode successfully on the prefix and silently drop the corruption.
	if dec.More() {
		return fmt.Errorf("trailing data after JSON value in %s", path)
	}
	return nil
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
// should treat that as fail-open. Dropped mutations are counted in
// droppedMutations for observability.
func UpdateSnapshot(paths Paths, clientType, sessionID string, initialize func() *schema.Snapshot, mutate func(*schema.Snapshot) error) error {
	// EnsureSessionDir creates the session directory (with full symlink
	// validation) which also contains the lock file.
	if err := paths.EnsureSessionDir(clientType, sessionID); err != nil {
		return err
	}
	lockPath := paths.SessionLock(clientType, sessionID)
	fl := NewFileLock(lockPath)
	if err := fl.Acquire(); err != nil {
		if IsLockBusy(err) {
			atomic.AddInt64(&droppedMutations, 1)
		}
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
// Corrupt files are quarantined aside so future refreshes start clean.
func LoadGlobal(paths Paths) (*schema.GlobalState, error) {
	gs := &schema.GlobalState{}

	// Each resource loads independently. A failure on one does not prevent
	// the others from being read. This means a corrupt models file will
	// not discard valid circuit-breaker state.

	var loadErr error
	if err := readJSONQuarantine(paths.GlobalHealth(), &gs.Health, "health"); err != nil {
		loadErr = err
	}
	if err := readJSONQuarantine(paths.GlobalModels(), &gs.Models, "models"); err != nil {
		loadErr = err
	}
	if err := readJSONQuarantine(paths.GlobalAccountUsage(), &gs.AccountUsage, "account-usage"); err != nil {
		loadErr = err
	}
	if err := readJSONQuarantine(paths.GlobalCircuitBreakers(), &gs.CircuitBreakers, "circuit-breakers"); err != nil {
		loadErr = err
	}
	return gs, loadErr
}

// readJSONQuarantine reads JSON from path into v, quarantining the file if it
// cannot be decoded. The resource name is used in the quarantine filename.
func readJSONQuarantine(path string, v any, resourceName string) error {
	if err := ReadJSON(path, v); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// Corrupt file: quarantine it so future refreshes do not see stale
		// or invalid data.
		quarantineGlobalFile(path, resourceName, schema.QuarantineReason(err))
		return fmt.Errorf("quarantined %s: %w", resourceName, err)
	}
	return nil
}

// quarantineGlobalFile renames a corrupt global resource file aside so
// subsequent refreshes start fresh. Best-effort — errors are ignored.
func quarantineGlobalFile(path, resourceName, reason string) {
	if path == "" {
		return
	}
	dst := path + ".quarantine-" + resourceName + "-" + reason
	_ = os.Rename(path, dst)
	_ = os.Remove(path + ChecksumFileSuffix)
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
	// EnsureDirs creates both cache root and global dir with full validation.
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	lockPath := paths.GlobalCircuitBreakersLock()
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
