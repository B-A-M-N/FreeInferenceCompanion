package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	cfg := defaultConfig()
	if cfg.SchemaVersion != SchemaVersion {
		t.Errorf("schema = %d", cfg.SchemaVersion)
	}
}

func TestLoadDefaultsOnMissingFile(t *testing.T) {
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != SchemaVersion {
		t.Errorf("schema = %d, want %d", cfg.SchemaVersion, SchemaVersion)
	}
	if !cfg.Tracing.Enabled {
		t.Error("tracing should default enabled for Companion launches")
	}
}

func TestLoadRejectsOversizedTrailingAndFutureConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1}`+string(make([]byte, maxConfigBytes))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("oversized config must be rejected")
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("trailing config data must be rejected")
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("future config schema must be rejected")
	}
}

func TestLoadRejectsSymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "outside.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("config symlink must be rejected")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	cfg := defaultConfig()
	cfg.Context.WatchEnter = 65.0
	cfg.Context.CriticalEnter = 95.0
	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Context.WatchEnter != 65.0 {
		t.Errorf("watch = %f, want 65", loaded.Context.WatchEnter)
	}
	if loaded.Context.CriticalEnter != 95.0 {
		t.Errorf("critical = %f, want 95", loaded.Context.CriticalEnter)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("FI_WATCH_ENTER", "45.0")
	t.Setenv("FI_CRITICAL_ENTER", "85.0")
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
	cfg.Context.WatchEnter = 75.0
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
	Save(&d)
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
	Save(&d)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0600 != 0600 {
		t.Errorf("dir perms = %o, no owner rw", info.Mode().Perm())
	}
}

func TestSetField(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	cfg := defaultConfig()
	cfg.Context.WatchLeave = 45.0
	if err := SetField(&cfg, "context.watch_enter", "55.0"); err != nil {
		t.Fatal(err)
	}
	if err := SetField(&cfg, "privacy.diagnostic_probes", "false"); err != nil {
		t.Fatal(err)
	}
	if err := SetField(&cfg, "reporting.level", "standard"); err != nil {
		t.Fatal(err)
	}
	if err := SetField(&cfg, "tracing.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Context.WatchEnter != 55.0 {
		t.Errorf("watch = %f, want 55", loaded.Context.WatchEnter)
	}
	if loaded.Privacy.DiagnosticProbes != false {
		t.Errorf("diagnostic_probes should be false")
	}
	if loaded.Reporting.Level != "standard" {
		t.Errorf("reporting level = %q, want standard", loaded.Reporting.Level)
	}
	if loaded.Tracing.Enabled {
		t.Error("tracing.enabled should be false")
	}
}

func TestTracingEnvironmentOverride(t *testing.T) {
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("FI_TRACING", "false")
	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if eff.Tracing.Enabled.Value || eff.Tracing.Enabled.Source != SourceEnv || !eff.Tracing.Enabled.Valid {
		t.Fatalf("unexpected tracing effective value: %#v", eff.Tracing.Enabled)
	}
}

func TestReportingLevelValidationAndEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	t.Setenv("FI_REPORTING_LEVEL", "summary")
	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if eff.Reporting.Level.Value != "summary" || eff.Reporting.Level.Source != SourceEnv || !eff.Reporting.Level.Valid {
		t.Errorf("reporting level = %#v, want valid environment summary", eff.Reporting.Level)
	}

	cfg := defaultConfig()
	if err := SetField(&cfg, "reporting.level", "verbose"); err == nil {
		t.Fatal("expected invalid reporting level to be rejected")
	}
	cfg.Reporting.Level = "verbose"
	if err := Validate(&cfg); err == nil {
		t.Fatal("expected invalid persisted reporting level to fail validation")
	}
}

func TestValidateRejectsCrossFieldThresholds(t *testing.T) {
	cfg := defaultConfig()
	cfg.Context.WarnEnter = cfg.Context.WatchEnter
	if err := Validate(&cfg); err == nil {
		t.Fatal("expected invalid context threshold ordering")
	}
	cfg = defaultConfig()
	cfg.Cache.WarnThreshold = cfg.Cache.RecoveredThreshold
	if err := Validate(&cfg); err == nil {
		t.Fatal("expected invalid cache threshold ordering")
	}
}

func TestEffectiveConfigRejectsNonFiniteCacheEnvAndZeroCooldown(t *testing.T) {
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("FI_CACHE_WARN_THRESHOLD", "NaN")
	t.Setenv("FI_CACHE_COOLDOWN_MINS", "0")
	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if eff.Cache.WarnThreshold.Valid || eff.Cache.CooldownMins.Valid {
		t.Fatalf("invalid effective cache values were accepted: %#v %#v", eff.Cache.WarnThreshold, eff.Cache.CooldownMins)
	}

	cfg := defaultConfig()
	cfg.Cache.WarnThreshold = math.Inf(1)
	if err := Validate(&cfg); err == nil {
		t.Fatal("Validate accepted positive infinity")
	}
}

func TestEffectiveConfigMarksCrossFieldEnvInvariant(t *testing.T) {
	t.Setenv("FI_CONFIG_DIR", t.TempDir())
	t.Setenv("FI_WATCH_ENTER", "90")
	t.Setenv("FI_WARN_ENTER", "80")
	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if eff.Context.WatchEnter.Valid || eff.Context.WarnEnter.Valid || len(eff.Invalid) == 0 {
		t.Fatalf("cross-field invariant was not surfaced: %#v", eff)
	}
}

func TestConfigEnvPrecedenceOverFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	cfg := defaultConfig()
	cfg.Context.WatchEnter = 90.0
	cfg.Context.WarnEnter = 95.0
	cfg.Context.CriticalEnter = 99.0
	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FI_WATCH_ENTER", "45.0")
	t.Setenv("FI_WARN_ENTER", "55.0")
	mgr, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	eff, err := mgr.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if eff.Context.WatchEnter.Value != 45.0 {
		t.Errorf("watch from env = %f, want 45", eff.Context.WatchEnter.Value)
	}
	if eff.Context.WatchEnter.Source != SourceEnv {
		t.Errorf("watch source = %s, want env", eff.Context.WatchEnter.Source)
	}
	if eff.Context.WarnEnter.Value != 55.0 {
		t.Errorf("warn from env = %f, want 55", eff.Context.WarnEnter.Value)
	}
	if eff.Context.WarnEnter.Source != SourceEnv {
		t.Errorf("warn source = %s, want env", eff.Context.WarnEnter.Source)
	}
	if eff.Context.CriticalEnter.Value != 99.0 {
		t.Errorf("critical from file = %f, want 99", eff.Context.CriticalEnter.Value)
	}
	if eff.Context.CriticalEnter.Source != SourceConfig {
		t.Errorf("critical source = %s, want config", eff.Context.CriticalEnter.Source)
	}
}

func TestSourceDefaultWhenNoFile(t *testing.T) {
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
	if eff.Context.WatchEnter.Source != SourceDefault {
		t.Errorf("watch source = %s, want default", eff.Context.WatchEnter.Source)
	}
	if eff.Context.WatchEnter.Value != 70.0 {
		t.Errorf("watch = %f, want 70", eff.Context.WatchEnter.Value)
	}
	if eff.Privacy.DiagnosticProbes.Source != SourceDefault {
		t.Errorf("probes source = %s, want default", eff.Privacy.DiagnosticProbes.Source)
	}
}

func TestZeroValuePersisted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	cfg := defaultConfig()
	cfg.Privacy.DiagnosticProbes = false
	if err := Save(&cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Privacy.DiagnosticProbes != false {
		t.Errorf("diagnostic_probes should be false")
	}
}
