// Package config provides typed, persistent configuration for the FreeInference
// Companion. Configuration is resolved with explicit precedence:
//
//  1. Environment variable (highest)
//  2. Persistent file (~/.config/freeinference-companion/config.json)
//  3. Built-in default (lowest)
//
// Every effective value tracks its source so the UI can show where a value came
// from and flag invalid or shadowed settings.
package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ============================================================
// Value source tracking
// ============================================================

// ValueSource identifies where an effective configuration value was resolved from.
type ValueSource string

const (
	SourceDefault ValueSource = "default"
	SourceConfig  ValueSource = "config_file"
	SourceEnv     ValueSource = "environment"
	SourceFlag    ValueSource = "command_line"
)

// EffectiveValue carries a resolved configuration value with provenance.
type EffectiveValue[T any] struct {
	Value    T           `json:"value"`
	Source   ValueSource `json:"source"`
	RawValue string      `json:"raw_value,omitempty"`
	Valid    bool        `json:"valid"`
	Error    string      `json:"error,omitempty"`
}

// ============================================================
// Configuration schema
// ============================================================

// SchemaVersion is the current config file schema version.
const SchemaVersion = 1

// Config is the typed, persistent configuration model.
type Config struct {
	SchemaVersion int            `json:"schema_version"`
	Context       ContextConfig  `json:"context"`
	Cache         CacheConfig    `json:"cache"`
	Refresh       RefreshConfig  `json:"refresh"`
	Provider      ProviderConfig `json:"provider"`
	Privacy       PrivacyConfig  `json:"privacy"`
}

// ContextConfig controls context pressure thresholds and warnings.
type ContextConfig struct {
	WatchEnter    float64 `json:"watch_enter,omitempty"`
	WarnEnter     float64 `json:"warn_enter,omitempty"`
	CriticalEnter float64 `json:"critical_enter,omitempty"`
	WatchLeave    float64 `json:"watch_leave,omitempty"`
	WarnLeave     float64 `json:"warn_leave,omitempty"`
	CriticalLeave float64 `json:"critical_leave,omitempty"`
	OutputReserve int     `json:"output_reserve,omitempty"`
}

// CacheConfig controls cache analysis and warning behavior.
type CacheConfig struct {
	WarnThreshold float64 `json:"warn_threshold,omitempty"`
	CooldownMins  int     `json:"cooldown_mins,omitempty"`
}

// RefreshConfig controls background refresh behavior.
type RefreshConfig struct {
	IntervalMins int `json:"interval_mins,omitempty"`
	StaleMins    int `json:"stale_mins,omitempty"`
}

// ProviderConfig controls provider detection behavior.
type ProviderConfig struct {
	AllowInsecureLocalhost bool `json:"allow_insecure_localhost,omitempty"`
}

// PrivacyConfig controls diagnostic and reporting behavior.
type PrivacyConfig struct {
	DiagnosticProbes bool `json:"diagnostic_probes,omitempty"`
}

// ============================================================
// Defaults
// ============================================================

func defaultConfig() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Context: ContextConfig{
			WatchEnter:    70.0,
			WarnEnter:     80.0,
			CriticalEnter: 90.0,
			WatchLeave:    60.0,
			WarnLeave:     65.0,
			CriticalLeave: 75.0,
			OutputReserve: 16000,
		},
		Cache: CacheConfig{
			WarnThreshold: 0.20,
			CooldownMins:  30,
		},
		Refresh: RefreshConfig{
			IntervalMins: 5,
			StaleMins:    15,
		},
		Provider: ProviderConfig{
			AllowInsecureLocalhost: false,
		},
		Privacy: PrivacyConfig{
			DiagnosticProbes: true,
		},
	}
}

// ============================================================
// Effective view
// ============================================================

// EffectiveConfig carries every config field with its effective value and source.
type EffectiveConfig struct {
	Context struct {
		WatchEnter    EffectiveValue[float64]
		WarnEnter     EffectiveValue[float64]
		CriticalEnter EffectiveValue[float64]
		WatchLeave    EffectiveValue[float64]
		WarnLeave     EffectiveValue[float64]
		CriticalLeave EffectiveValue[float64]
		OutputReserve EffectiveValue[int]
	}
	Cache struct {
		WarnThreshold EffectiveValue[float64]
		CooldownMins  EffectiveValue[int]
	}
	Refresh struct {
		IntervalMins EffectiveValue[int]
		StaleMins    EffectiveValue[int]
	}
	Provider struct {
		AllowInsecureLocalhost EffectiveValue[bool]
	}
	Privacy struct {
		DiagnosticProbes EffectiveValue[bool]
	}
	// Invalid records any config fields that failed to parse.
	Invalid []string
}

// ============================================================
// Env config helpers
// ============================================================

// envFloat reads an environment variable as float64. Returns (value, true) if
// the env var is set and valid. Returns (def, false) if unset or invalid.
// Unlike the old getEnvFloat, this also returns whether the value was set so
// callers can detect invalid values.
func envFloat(key string, def float64) (float64, bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, false, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def, true, fmt.Errorf("invalid float %q for %s: %w", v, key, err)
	}
	return f, true, nil
}

// envInt reads an environment variable as int. Returns (value, true) if set
// and valid, (def, false) if unset, or error if set but invalid.
func envInt(key string, def int) (int, bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, false, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def, true, fmt.Errorf("invalid int %q for %s: %w", v, key, err)
	}
	return i, true, nil
}

// envBool reads an environment variable as bool (1/true/yes or 0/false/no).
func envBool(key string, def bool) (bool, bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, false, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true, true, nil
	case "0", "false", "no":
		return false, true, nil
	default:
		return def, true, fmt.Errorf("invalid bool %q for %s", v, key)
	}
}

// ============================================================
// Configuration file paths
// ============================================================

// configDir returns the XDG config directory for the companion.
func configDir() (string, error) {
	if d := os.Getenv("FI_CONFIG_DIR"); d != "" {
		return d, nil
	}
	// XDG_CONFIG_HOME or ~/.config
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg != "" {
		return filepath.Join(xdg, "freeinference-companion"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "freeinference-companion"), nil
}

// ConfigPath returns the path to the JSON configuration file.
func ConfigPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// ============================================================
// Load/Save
// ============================================================

// Load reads configuration from the persistent file, falling back to defaults
// if the file does not exist. Returns the config plus any load errors.
func Load() (*Config, error) {
	cfg := defaultConfig()
	path, err := ConfigPath()
	if err != nil {
		return &cfg, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &cfg, nil
		}
		return &cfg, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return &cfg, fmt.Errorf("decode config: %w", err)
	}
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = SchemaVersion
	}
	return &cfg, nil
}

// Save writes the configuration to the persistent file atomically.
// Creates the config directory with 0700 permissions if it does not exist.
// The config file itself uses 0600 permissions.
// Implements atomic write-and-rename for crash safety.
func Save(cfg *Config) error {
	cfg.SchemaVersion = SchemaVersion
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	// Atomic write-and-rename
	tmp, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ResetToDefault replaces the current file config with defaults and saves.
func ResetToDefault() error {
	cfg := defaultConfig()
	return Save(&cfg)
}

// SetField updates a single configuration field specified by dot-path key
// (e.g., "context.watch_enter"). Returns an error if the key is unknown
// or the value cannot be parsed to the correct type.
func SetField(cfg *Config, key, value string) error {
	parseFloat := func() (float64, error) {
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid float %q for %s: %w", value, key, err)
		}
		return f, nil
	}
	parseInt := func() (int, error) {
		i, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("invalid int %q for %s: %w", value, key, err)
		}
		return i, nil
	}
	parseBool := func() (bool, error) {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes":
			return true, nil
		case "0", "false", "no":
			return false, nil
		default:
			return false, fmt.Errorf("invalid bool %q for %s (use 0/1, true/false, yes/no)", value, key)
		}
	}

	switch key {
	case "context.watch_enter":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Context.WatchEnter = v
	case "context.warn_enter":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Context.WarnEnter = v
	case "context.critical_enter":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Context.CriticalEnter = v
	case "context.watch_leave":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Context.WatchLeave = v
	case "context.warn_leave":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Context.WarnLeave = v
	case "context.critical_leave":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Context.CriticalLeave = v
	case "context.output_reserve":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.Context.OutputReserve = v
	case "cache.warn_threshold":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Cache.WarnThreshold = v
	case "cache.cooldown_mins":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.Cache.CooldownMins = v
	case "refresh.interval_mins":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.Refresh.IntervalMins = v
	case "privacy.diagnostic_probes":
		v, err := parseBool()
		if err != nil {
			return err
		}
		cfg.Privacy.DiagnosticProbes = v
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// ============================================================
// Resolve (env + file + defaults)
// ============================================================

// Manager resolves configuration with environment → file → default precedence.
type Manager struct {
	mu   sync.RWMutex
	cfg  *Config
	path string
}

// Resolve reads configuration from file, applies environment overrides, and
// returns the effective view with provenance for every field.
func (m *Manager) Resolve() (*EffectiveConfig, error) {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()

	if cfg == nil {
		var err error
		cfg, err = Load()
		if err != nil {
			cfg = &Config{}
			_ = cfg // ignore load error; continue with defaults
		}
		m.mu.Lock()
		m.cfg = cfg
		m.mu.Unlock()
	}

	eff := &EffectiveConfig{}

	// Context: WatchEnter
	eff.Context.WatchEnter = resolveFloat(
		"FI_WATCH_ENTER",
		cfg.Context.WatchEnter,
		envFloat,
		func(v float64) error {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
				return fmt.Errorf("must be in [0,100], got %v", v)
			}
			return nil
		},
	)

	// Context: WarnEnter
	eff.Context.WarnEnter = resolveFloat(
		"FI_WARN_ENTER",
		cfg.Context.WarnEnter,
		envFloat,
		func(v float64) error {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
				return fmt.Errorf("must be in [0,100], got %v", v)
			}
			return nil
		},
	)

	// Context: CriticalEnter
	eff.Context.CriticalEnter = resolveFloat(
		"FI_CRITICAL_ENTER",
		cfg.Context.CriticalEnter,
		envFloat,
		func(v float64) error {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
				return fmt.Errorf("must be in [0,100], got %v", v)
			}
			return nil
		},
	)

	// Context: WatchLeave
	eff.Context.WatchLeave = resolveFloat(
		"FI_WATCH_LEAVE",
		cfg.Context.WatchLeave,
		envFloat,
		func(v float64) error {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
				return fmt.Errorf("must be in [0,100], got %v", v)
			}
			return nil
		},
	)

	// Context: WarnLeave
	eff.Context.WarnLeave = resolveFloat(
		"FI_WARN_LEAVE",
		cfg.Context.WarnLeave,
		envFloat,
		func(v float64) error {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
				return fmt.Errorf("must be in [0,100], got %v", v)
			}
			return nil
		},
	)

	// Context: CriticalLeave
	eff.Context.CriticalLeave = resolveFloat(
		"FI_CRITICAL_LEAVE",
		cfg.Context.CriticalLeave,
		envFloat,
		func(v float64) error {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
				return fmt.Errorf("must be in [0,100], got %v", v)
			}
			return nil
		},
	)

	// Context: OutputReserve
	eff.Context.OutputReserve = resolveInt(
		"FI_OUTPUT_RESERVE",
		cfg.Context.OutputReserve,
		envInt,
		func(v int) error {
			if v <= 0 || v > 1_000_000 {
				return fmt.Errorf("must be in (0, 1000000], got %d", v)
			}
			return nil
		},
	)

	// Cache: WarnThreshold
	eff.Cache.WarnThreshold = resolveFloat(
		"FI_CACHE_WARN_THRESHOLD",
		cfg.Cache.WarnThreshold,
		envFloat,
		func(v float64) error {
			if v < 0 || v > 1 {
				return fmt.Errorf("must be in [0,1], got %v", v)
			}
			return nil
		},
	)

	// Cache: CooldownMins
	eff.Cache.CooldownMins = resolveInt(
		"FI_CACHE_COOLDOWN_MINS",
		cfg.Cache.CooldownMins,
		envInt,
		func(v int) error {
			if v < 0 {
				return fmt.Errorf("must be >= 0, got %d", v)
			}
			return nil
		},
	)

	// Refresh: IntervalMins
	eff.Refresh.IntervalMins = resolveInt(
		"FI_REFRESH_INTERVAL_MINS",
		cfg.Refresh.IntervalMins,
		envInt,
		func(v int) error {
			if v < 1 {
				return fmt.Errorf("must be >= 1, got %d", v)
			}
			return nil
		},
	)

	// Privacy: DiagnosticProbes
	eff.Privacy.DiagnosticProbes = resolveBool(
		"FI_DIAGNOSTIC_PROBES",
		cfg.Privacy.DiagnosticProbes,
		envBool,
		nil,
	)

	return eff, nil
}

// resolveFloat resolves a float64 config value with env override and validation.
func resolveFloat(
	envKey string,
	fileVal float64,
	readEnv func(string, float64) (float64, bool, error),
	validate func(float64) error,
) EffectiveValue[float64] {
	envVal, envSet, err := readEnv(envKey, fileVal)
	if err != nil {
		return EffectiveValue[float64]{
			Value:    fileVal,
			Source:   SourceConfig,
			RawValue: os.Getenv(envKey),
			Valid:    false,
			Error:    fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), err),
		}
	}
	if envSet {
		if validate != nil {
			if verr := validate(envVal); verr != nil {
				return EffectiveValue[float64]{
					Value:    fileVal,
					Source:   SourceConfig,
					RawValue: os.Getenv(envKey),
					Valid:    false,
					Error:    fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), verr),
				}
			}
		}
		return EffectiveValue[float64]{
			Value:    envVal,
			Source:   SourceEnv,
			RawValue: os.Getenv(envKey),
			Valid:    true,
		}
	}
	// From file or default
	valid := true
	if validate != nil {
		if verr := validate(fileVal); verr != nil {
			valid = false
		}
	}
	return EffectiveValue[float64]{
		Value: fileVal,
		Source: func() ValueSource {
			if fileVal != 0 {
				return SourceConfig
			}
			return SourceDefault
		}(),
		Valid: valid,
	}
}

// resolveInt resolves an int config value with env override and validation.
func resolveInt(
	envKey string,
	fileVal int,
	readEnv func(string, int) (int, bool, error),
	validate func(int) error,
) EffectiveValue[int] {
	envVal, envSet, err := readEnv(envKey, fileVal)
	if err != nil {
		return EffectiveValue[int]{
			Value:    fileVal,
			Source:   SourceConfig,
			RawValue: os.Getenv(envKey),
			Valid:    false,
			Error:    fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), err),
		}
	}
	if envSet {
		if validate != nil {
			if verr := validate(envVal); verr != nil {
				return EffectiveValue[int]{
					Value:    fileVal,
					Source:   SourceConfig,
					RawValue: os.Getenv(envKey),
					Valid:    false,
					Error:    fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), verr),
				}
			}
		}
		return EffectiveValue[int]{
			Value:    envVal,
			Source:   SourceEnv,
			RawValue: os.Getenv(envKey),
			Valid:    true,
		}
	}
	valid := true
	if validate != nil {
		if verr := validate(fileVal); verr != nil {
			valid = false
		}
	}
	return EffectiveValue[int]{
		Value: fileVal,
		Source: func() ValueSource {
			if fileVal != 0 {
				return SourceConfig
			}
			return SourceDefault
		}(),
		Valid: valid,
	}
}

// resolveBool resolves a bool config value with env override.
func resolveBool(
	envKey string,
	fileVal bool,
	readEnv func(string, bool) (bool, bool, error),
	validate func(bool) error,
) EffectiveValue[bool] {
	envVal, envSet, err := readEnv(envKey, fileVal)
	if err != nil {
		return EffectiveValue[bool]{
			Value:    fileVal,
			Source:   SourceConfig,
			RawValue: os.Getenv(envKey),
			Valid:    false,
			Error:    fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), err),
		}
	}
	if envSet {
		if validate != nil {
			if verr := validate(envVal); verr != nil {
				return EffectiveValue[bool]{
					Value:    fileVal,
					Source:   SourceConfig,
					RawValue: os.Getenv(envKey),
					Valid:    false,
					Error:    fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), verr),
				}
			}
		}
		return EffectiveValue[bool]{
			Value:    envVal,
			Source:   SourceEnv,
			RawValue: os.Getenv(envKey),
			Valid:    true,
		}
	}
	return EffectiveValue[bool]{
		Value:  fileVal,
		Source: SourceDefault,
		Valid:  true,
	}
}

// NewManager creates a config manager and loads persisted config.
func NewManager() (*Manager, error) {
	cfg, err := Load()
	if err != nil {
		// Continue with defaults on load error
		d := defaultConfig()
		cfg = &d
	}
	m := &Manager{cfg: cfg}
	m.path, _ = ConfigPath()
	return m, nil
}

// ConfigPath returns the path to the configuration file used by the manager.
func (m *Manager) ConfigPath() string {
	m.mu.RLock()
	p := m.path
	m.mu.RUnlock()
	return p
}
