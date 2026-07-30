package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	cfg := defaultConfig()
	if cfg.SchemaVersion != SchemaVersion {
		t.Errorf("schema = %d", cfg.SchemaVersion)
	}
	if cfg.Context.WatchEnter >= cfg.Context.WarnEnter {
		t.Error("watch/watch thresholds not ordered")
	}
	if cfg.Context.WarnEnter >= cfg.Context.CriticalEnter {
		t.Error("warn/critical thresholds not ordered")
	}
	if cfg.Cache.WarnThreshold <= 0 || cfg.Cache.WarnThreshold >= 1 {
		t.Errorf("bad cache threshold: %f", cfg.Cache.WarnThreshold)
	}
}

func TestLoadDefaultsOnMissingFile(t *testing.T) {
	// Point config dir at a temp directory that doesn't exist yet
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != SchemaVersion {
		t.Errorf("schema = %d, want %d", cfg.SchemaVersion, SchemaVersion)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	cfg := defaultConfig()
	cfg.Context.WatchEnter = 50.0
	cfg.Context.CriticalEnter = 95.0

	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Context.WatchEnter != 50.0 {
		t.Errorf("watch = %f", loaded.Context.WatchEnter)
	}
	if loaded.Context.CriticalEnter != 95.0 {
		t.Errorf("critical = %f", loaded.Context.CriticalEnter)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "45.0")
	t.Setenv("FI_CRITICAL_ENTER", "85.0")

	// Use an isolated config dir with defaults
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}

	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}

	if eff.Context.WatchEnter.Value != 45.0 {
		t.Errorf("watch = %f, want 45", eff.Context.WatchEnter.Value)
	}
	if eff.Context.WatchEnter.Source != SourceEnv {
		t.Errorf("watch source = %s, want env", eff.Context.WatchEnter.Source)
	}
	if eff.Context.CriticalEnter.Value != 85.0 {
		t.Errorf("critical = %f, want 85", eff.Context.CriticalEnter.Value)
	}
	if eff.Context.CriticalEnter.Source != SourceEnv {
		t.Errorf("critical source = %s, want env", eff.Context.CriticalEnter.Source)
	}
}

func TestInvalidEnvShowsError(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "not-a-number")
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if eff.Context.WatchEnter.Valid {
		t.Error("invalid env should be invalid")
	}
	if eff.Context.WatchEnter.Error == "" {
		t.Error("invalid env should have error message")
	}
	// Should fall back to default value
	if eff.Context.WatchEnter.Value != 70.0 {
		t.Errorf("fallback = %f, want default 70", eff.Context.WatchEnter.Value)
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	cfg := defaultConfig()
	cfg.Context.OutputReserve = 32000

	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}

	// Verify the config file exists and is valid JSON
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty config file")
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Context.OutputReserve != 32000 {
		t.Errorf("output = %d, want 32000", loaded.Context.OutputReserve)
	}
}

func TestResetToDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	cfg := defaultConfig()
	cfg.Context.WatchEnter = 99.0
	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}

	if err := ResetToDefault(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Context.WatchEnter != 70.0 {
		t.Errorf("after reset watch = %f, want 70", loaded.Context.WatchEnter)
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	d := defaultConfig()
	if err := Save(&d); err != nil {
	}

	configPath := filepath.Join(dir, "config.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perms = %o, want 0600", info.Mode().Perm())
	}
}

func TestDirPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	d := defaultConfig()
	if err := Save(&d); err != nil {
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0600 != 0600 {
		t.Errorf("dir perms = %o, does not have owner read/write", info.Mode().Perm())
	}
}

func TestConfigEnvPrecedenceOverFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)

	// Write a config file with specific values.
	cfg := defaultConfig()
	cfg.Context.WatchEnter = 90.0
	cfg.Context.WarnEnter = 95.0
	cfg.Context.CriticalEnter = 99.0
	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}

	// Set env vars that override file values.
	t.Setenv("FI_WATCH_ENTER", "45.0")
	t.Setenv("FI_WARN_ENTER", "55.0")
	// FI_CRITICAL_ENTER is NOT set — should fall back to file value 99.0.

	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}

	// Env overrides file.
	if eff.Context.WatchEnter.Value != 45.0 {
		t.Errorf("watch from env = %f, want 45", eff.Context.WatchEnter.Value)
	}
	if eff.Context.WatchEnter.Source != SourceEnv {
		t.Errorf("watch source = %s, want %s", eff.Context.WatchEnter.Source, SourceEnv)
	}
	if eff.Context.WarnEnter.Value != 55.0 {
		t.Errorf("warn from env = %f, want 55", eff.Context.WarnEnter.Value)
	}
	if eff.Context.WarnEnter.Source != SourceEnv {
		t.Errorf("warn source = %s, want %s", eff.Context.WarnEnter.Source, SourceEnv)
	}
	// Not overridden by env — comes from file.
	if eff.Context.CriticalEnter.Value != 99.0 {
		t.Errorf("critical from file = %f, want 99", eff.Context.CriticalEnter.Value)
	}
	if eff.Context.CriticalEnter.Source != SourceConfig {
		t.Errorf("critical source = %s, want %s", eff.Context.CriticalEnter.Source, SourceConfig)
	}
}
