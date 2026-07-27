package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bamn/freeinference-companion/pkg/schema"
)

const (
	DefaultCacheDir   = ".cache/freeinference-companion"
	DefaultConfigFile = "config.toml"
)

// Paths resolves filesystem paths for state storage.
type Paths struct {
	CacheDir string
}

// NewPaths creates a Paths rooted at the default cache directory under HOME.
func NewPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("home dir: %w", err)
	}
	return Paths{
		CacheDir: filepath.Join(home, DefaultCacheDir),
	}, nil
}

// NewPathsWithDir creates a Paths with an explicit cache directory.
func NewPathsWithDir(cacheDir string) Paths {
	return Paths{CacheDir: cacheDir}
}

// SessionDir returns the directory for a given client type and session ID.
func (p Paths) SessionDir(clientType, sessionID string) string {
	return filepath.Join(p.CacheDir, "sessions", clientType, sessionID)
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

// GlobalCircuitBreakers returns the path to the circuit breaker state.
func (p Paths) GlobalCircuitBreakers() string {
	return filepath.Join(p.GlobalDir(), "circuit-breakers.json")
}

// EnsureDirs creates all required directories under the cache root.
func (p Paths) EnsureDirs() error {
	dirs := []string{
		p.CacheDir,
		p.GlobalDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// EnsureSessionDir creates the per-session directory.
func (p Paths) EnsureSessionDir(clientType, sessionID string) error {
	return os.MkdirAll(p.SessionDir(clientType, sessionID), 0755)
}

// ============================================================
// Atomic file operations
// ============================================================

// WriteJSONAtomically writes v as JSON to path using a temp file + rename.
// This prevents readers from seeing a partially-written file.
func WriteJSONAtomically(path string, v interface{}) error {
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
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// ReadJSON reads and decodes JSON from path into v.
// Returns os.ErrNotExist if the file does not exist.
func ReadJSON(path string, v interface{}) error {
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
func LoadSnapshot(paths Paths, clientType, sessionID string) (*schema.Snapshot, error) {
	path := paths.SessionSnapshot(clientType, sessionID)
	var s schema.Snapshot
	if err := ReadJSON(path, &s); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	return &s, nil
}

// SaveSnapshot writes the per-session snapshot atomically.
func SaveSnapshot(paths Paths, clientType, sessionID string, s *schema.Snapshot) error {
	if err := paths.EnsureSessionDir(clientType, sessionID); err != nil {
		return err
	}
	path := paths.SessionSnapshot(clientType, sessionID)
	return WriteJSONAtomically(path, s)
}

// ============================================================
// Global state load/save
// ============================================================

// LoadGlobal reads the global state. Returns a zero-value if no file exists.
func LoadGlobal(paths Paths) (*schema.GlobalState, error) {
	gs := &schema.GlobalState{}

	if err := ReadJSON(paths.GlobalHealth(), &gs.Health); err != nil && !os.IsNotExist(err) {
		return gs, fmt.Errorf("load health: %w", err)
	}
	if err := ReadJSON(paths.GlobalModels(), &gs.Models); err != nil && !os.IsNotExist(err) {
		return gs, fmt.Errorf("load models: %w", err)
	}
	if err := ReadJSON(paths.GlobalAccountUsage(), &gs.AccountUsage); err != nil && !os.IsNotExist(err) {
		return gs, fmt.Errorf("load account usage: %w", err)
	}
	if err := ReadJSON(paths.GlobalCircuitBreakers(), &gs.CircuitBreakers); err != nil && !os.IsNotExist(err) {
		return gs, fmt.Errorf("load circuit breakers: %w", err)
	}
	return gs, nil
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