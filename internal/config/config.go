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

type ValueSource string

const (
	SourceDefault ValueSource = "default"
	SourceConfig  ValueSource = "config_file"
	SourceEnv     ValueSource = "environment"
	SourceFlag    ValueSource = "command_line"
)

type EffectiveValue[T any] struct {
	Value    T           `json:"value"`
	Source   ValueSource `json:"source"`
	RawValue string      `json:"raw_value,omitempty"`
	Valid    bool        `json:"valid"`
	Error    string      `json:"error,omitempty"`
}

const SchemaVersion = 1

type Config struct {
	SchemaVersion int             `json:"schema_version"`
	Context       ContextConfig   `json:"context"`
	Cache         CacheConfig     `json:"cache"`
	Refresh       RefreshConfig   `json:"refresh"`
	Reporting     ReportingConfig `json:"reporting"`
	Provider      ProviderConfig  `json:"provider"`
	Privacy       PrivacyConfig   `json:"privacy"`
	Tracing       TracingConfig   `json:"tracing"`
}

type ContextConfig struct {
	WatchEnter    float64 `json:"watch_enter"`
	WarnEnter     float64 `json:"warn_enter"`
	CriticalEnter float64 `json:"critical_enter"`
	WatchLeave    float64 `json:"watch_leave"`
	WarnLeave     float64 `json:"warn_leave"`
	CriticalLeave float64 `json:"critical_leave"`
	OutputReserve int     `json:"output_reserve"`
}

type CacheConfig struct {
	WarnThreshold      float64 `json:"warn_threshold"`
	RecoveredThreshold float64 `json:"recovered_threshold"`
	CooldownMins       int     `json:"cooldown_mins"`
}

type RefreshConfig struct {
	IntervalMins int `json:"interval_mins"`
	StaleMins    int `json:"stale_mins"`
}

// ReportingConfig controls the default detail shown by interactive status
// commands. Explicit command-line flags always take precedence.
type ReportingConfig struct {
	Level string `json:"level"`
}

type ProviderConfig struct {
	AllowInsecureLocalhost bool `json:"allow_insecure_localhost"`
}

type PrivacyConfig struct {
	DiagnosticProbes bool `json:"diagnostic_probes"`
}

// TracingConfig controls Companion's launch-time support correlation. It is
// intentionally separate from diagnostic probes: tracing adds only a random
// per-launch X-Session-ID and never sends X-Probe or X-Request-ID.
type TracingConfig struct {
	Enabled bool `json:"enabled"`
}

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
			WarnThreshold:      0.20,
			RecoveredThreshold: 0.40,
			CooldownMins:       30,
		},
		Refresh: RefreshConfig{
			IntervalMins: 5,
			StaleMins:    15,
		},
		// Preserve the pre-reporting-level status output by default.
		Reporting: ReportingConfig{Level: "detailed"},
		Provider: ProviderConfig{
			AllowInsecureLocalhost: false,
		},
		Privacy: PrivacyConfig{
			DiagnosticProbes: true,
		},
		// Trace correlation is enabled for explicit Companion-launched clients.
		// Non-Companion launches are unaffected because the launcher is the only
		// component that injects the header.
		Tracing: TracingConfig{Enabled: true},
	}
}

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
		WarnThreshold      EffectiveValue[float64]
		RecoveredThreshold EffectiveValue[float64]
		CooldownMins       EffectiveValue[int]
	}
	Refresh struct {
		IntervalMins EffectiveValue[int]
		StaleMins    EffectiveValue[int]
	}
	Reporting struct {
		Level EffectiveValue[string]
	}
	Provider struct {
		AllowInsecureLocalhost EffectiveValue[bool]
	}
	Privacy struct {
		DiagnosticProbes EffectiveValue[bool]
	}
	Tracing struct {
		Enabled EffectiveValue[bool]
	}
	Invalid   []string `json:"invalid,omitempty"`
	LoadError string   `json:"load_error,omitempty"`
}

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

func envString(key string, def string) (string, bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def, false, nil
	}
	return strings.ToLower(strings.TrimSpace(v)), true, nil
}

// ValidReportingLevel reports whether level is a supported human-readable
// reporting detail level.
func ValidReportingLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "summary", "standard", "detailed":
		return true
	default:
		return false
	}
}

func configDir() (string, error) {
	if d := os.Getenv("FI_CONFIG_DIR"); d != "" {
		return d, nil
	}
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

func ConfigPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

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

func ResetToDefault() error {
	cfg := defaultConfig()
	return Save(&cfg)
}

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
	case "cache.recovered_threshold":
		v, err := parseFloat()
		if err != nil {
			return err
		}
		cfg.Cache.RecoveredThreshold = v
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
	case "reporting.level":
		level := strings.ToLower(strings.TrimSpace(value))
		if !ValidReportingLevel(level) {
			return fmt.Errorf("invalid reporting level %q (use summary, standard, or detailed)", value)
		}
		cfg.Reporting.Level = level
	case "privacy.diagnostic_probes":
		v, err := parseBool()
		if err != nil {
			return err
		}
		cfg.Privacy.DiagnosticProbes = v
	case "tracing.enabled":
		v, err := parseBool()
		if err != nil {
			return err
		}
		cfg.Tracing.Enabled = v
	case "refresh.stale_mins":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.Refresh.StaleMins = v
	case "provider.allow_insecure_localhost":
		v, err := parseBool()
		if err != nil {
			return err
		}
		cfg.Provider.AllowInsecureLocalhost = v
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// Validate checks cross-field invariants before a configuration is persisted
// or consumed by the warning state machines.
func Validate(cfg *Config) error {
	c := cfg.Context
	values := map[string]float64{"watch_enter": c.WatchEnter, "warn_enter": c.WarnEnter, "critical_enter": c.CriticalEnter, "watch_leave": c.WatchLeave, "warn_leave": c.WarnLeave, "critical_leave": c.CriticalLeave}
	for name, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return fmt.Errorf("context.%s must be finite and in [0,100]", name)
		}
	}
	if !(c.WatchEnter < c.WarnEnter && c.WarnEnter < c.CriticalEnter) || !(c.WatchLeave < c.WarnLeave && c.WarnLeave < c.CriticalLeave) {
		return fmt.Errorf("context thresholds must be ordered watch < warn < critical")
	}
	if c.WatchEnter-c.WatchLeave < 3 || c.WarnEnter-c.WarnLeave < 3 || c.CriticalEnter-c.CriticalLeave < 3 {
		return fmt.Errorf("context enter thresholds must exceed matching leave thresholds by at least 3")
	}
	if c.OutputReserve <= 0 || c.OutputReserve > 1_000_000 {
		return fmt.Errorf("context.output_reserve must be in [1,1000000]")
	}
	cache := cfg.Cache
	if math.IsNaN(cache.WarnThreshold) || math.IsInf(cache.WarnThreshold, 0) || math.IsNaN(cache.RecoveredThreshold) || math.IsInf(cache.RecoveredThreshold, 0) || cache.WarnThreshold < 0 || cache.RecoveredThreshold > 1 || cache.WarnThreshold >= cache.RecoveredThreshold {
		return fmt.Errorf("cache thresholds must satisfy 0 <= warn_threshold < recovered_threshold <= 1")
	}
	if cache.CooldownMins <= 0 || cfg.Refresh.IntervalMins <= 0 || cfg.Refresh.StaleMins <= 0 {
		return fmt.Errorf("cache cooldown and refresh intervals must be positive")
	}
	if !ValidReportingLevel(cfg.Reporting.Level) {
		return fmt.Errorf("reporting.level must be summary, standard, or detailed")
	}
	return nil
}

func (m *Manager) ConfigPath() string {
	m.mu.RLock()
	p := m.path
	m.mu.RUnlock()
	return p
}

type Manager struct {
	mu        sync.RWMutex
	cfg       *Config
	cfgLoaded bool
	path      string
	loadErr   error
}

func NewManager() (*Manager, error) {
	cfg, err := Load()
	_, statErr := os.Stat(func() string { p, _ := ConfigPath(); return p }())
	cfgLoaded := statErr == nil
	if err != nil {
		d := defaultConfig()
		cfg = &d
	}
	m := &Manager{cfg: cfg, cfgLoaded: cfgLoaded, loadErr: err}
	m.path, _ = ConfigPath()
	return m, nil
}

func (m *Manager) Resolve() (*EffectiveConfig, error) {
	m.mu.RLock()
	cfg := m.cfg
	cfgLoaded := m.cfgLoaded
	loadErr := m.loadErr
	m.mu.RUnlock()

	if cfg == nil {
		var err error
		cfg, err = Load()
		if err != nil {
			cfg = &Config{}
		}
		m.mu.Lock()
		m.cfg = cfg
		m.mu.Unlock()
	}

	resolveSrc := func() ValueSource {
		if cfgLoaded {
			return SourceConfig
		}
		return SourceDefault
	}

	eff := &EffectiveConfig{}

	eff.Context.WatchEnter = resolveFloat("FI_WATCH_ENTER", cfg.Context.WatchEnter, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return fmt.Errorf("must be in [0,100], got %v", v)
		}
		return nil
	})
	eff.Context.WarnEnter = resolveFloat("FI_WARN_ENTER", cfg.Context.WarnEnter, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return fmt.Errorf("must be in [0,100], got %v", v)
		}
		return nil
	})
	eff.Context.CriticalEnter = resolveFloat("FI_CRITICAL_ENTER", cfg.Context.CriticalEnter, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return fmt.Errorf("must be in [0,100], got %v", v)
		}
		return nil
	})
	eff.Context.WatchLeave = resolveFloat("FI_WATCH_LEAVE", cfg.Context.WatchLeave, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return fmt.Errorf("must be in [0,100], got %v", v)
		}
		return nil
	})
	eff.Context.WarnLeave = resolveFloat("FI_WARN_LEAVE", cfg.Context.WarnLeave, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return fmt.Errorf("must be in [0,100], got %v", v)
		}
		return nil
	})
	eff.Context.CriticalLeave = resolveFloat("FI_CRITICAL_LEAVE", cfg.Context.CriticalLeave, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return fmt.Errorf("must be in [0,100], got %v", v)
		}
		return nil
	})
	eff.Context.OutputReserve = resolveInt("FI_OUTPUT_RESERVE", cfg.Context.OutputReserve, cfgLoaded, resolveSrc, envInt, func(v int) error {
		if v <= 0 || v > 1_000_000 {
			return fmt.Errorf("must be in (0, 1000000], got %d", v)
		}
		return nil
	})
	eff.Cache.WarnThreshold = resolveFloat("FI_CACHE_WARN_THRESHOLD", cfg.Cache.WarnThreshold, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return fmt.Errorf("must be in [0,1], got %v", v)
		}
		return nil
	})
	eff.Cache.RecoveredThreshold = resolveFloat("FI_CACHE_RECOVERED_THRESHOLD", cfg.Cache.RecoveredThreshold, cfgLoaded, resolveSrc, envFloat, func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return fmt.Errorf("must be in [0,1], got %v", v)
		}
		return nil
	})
	eff.Cache.CooldownMins = resolveInt("FI_CACHE_COOLDOWN_MINS", cfg.Cache.CooldownMins, cfgLoaded, resolveSrc, envInt, func(v int) error {
		if v < 1 {
			return fmt.Errorf("must be >= 1, got %d", v)
		}
		return nil
	})
	eff.Refresh.IntervalMins = resolveInt("FI_REFRESH_INTERVAL_MINS", cfg.Refresh.IntervalMins, cfgLoaded, resolveSrc, envInt, func(v int) error {
		if v < 1 {
			return fmt.Errorf("must be >= 1, got %d", v)
		}
		return nil
	})
	eff.Refresh.StaleMins = resolveInt("FI_REFRESH_STALE_MINS", cfg.Refresh.StaleMins, cfgLoaded, resolveSrc, envInt, func(v int) error {
		if v < 1 {
			return fmt.Errorf("must be >= 1, got %d", v)
		}
		return nil
	})
	eff.Reporting.Level = resolveString("FI_REPORTING_LEVEL", cfg.Reporting.Level, resolveSrc, envString, func(v string) error {
		if !ValidReportingLevel(v) {
			return fmt.Errorf("must be summary, standard, or detailed, got %q", v)
		}
		return nil
	})
	eff.Privacy.DiagnosticProbes = resolveBool("FI_DIAGNOSTIC_PROBES", cfg.Privacy.DiagnosticProbes, cfgLoaded, resolveSrc, envBool, nil)
	eff.Tracing.Enabled = resolveBool("FI_TRACING", cfg.Tracing.Enabled, cfgLoaded, resolveSrc, envBool, nil)
	eff.Provider.AllowInsecureLocalhost = resolveBool("FI_ALLOW_INSECURE_LOCALHOST", cfg.Provider.AllowInsecureLocalhost, cfgLoaded, resolveSrc, envBool, nil)

	validateEffective(eff)
	if loadErr != nil {
		eff.LoadError = loadErr.Error()
		return eff, loadErr
	}
	return eff, nil
}

func validateEffective(eff *EffectiveConfig) {
	if eff == nil {
		return
	}
	if eff.Context.WatchEnter.Valid && eff.Context.WarnEnter.Valid && eff.Context.CriticalEnter.Valid &&
		!(eff.Context.WatchEnter.Value < eff.Context.WarnEnter.Value && eff.Context.WarnEnter.Value < eff.Context.CriticalEnter.Value) {
		markEffectiveInvalid(&eff.Context.WatchEnter, "context enter thresholds must be ordered watch < warn < critical", &eff.Invalid, "context thresholds")
		markEffectiveInvalid(&eff.Context.WarnEnter, "context enter thresholds must be ordered watch < warn < critical", &eff.Invalid, "context thresholds")
		markEffectiveInvalid(&eff.Context.CriticalEnter, "context enter thresholds must be ordered watch < warn < critical", &eff.Invalid, "context thresholds")
	}
	if eff.Context.WatchLeave.Valid && eff.Context.WarnLeave.Valid && eff.Context.CriticalLeave.Valid &&
		!(eff.Context.WatchLeave.Value < eff.Context.WarnLeave.Value && eff.Context.WarnLeave.Value < eff.Context.CriticalLeave.Value) {
		markEffectiveInvalid(&eff.Context.WatchLeave, "context leave thresholds must be ordered watch < warn < critical", &eff.Invalid, "context thresholds")
		markEffectiveInvalid(&eff.Context.WarnLeave, "context leave thresholds must be ordered watch < warn < critical", &eff.Invalid, "context thresholds")
		markEffectiveInvalid(&eff.Context.CriticalLeave, "context leave thresholds must be ordered watch < warn < critical", &eff.Invalid, "context thresholds")
	}
	if eff.Context.WatchEnter.Valid && eff.Context.WatchLeave.Valid && eff.Context.WatchEnter.Value-eff.Context.WatchLeave.Value < 3 {
		markEffectiveInvalid(&eff.Context.WatchEnter, "matching context thresholds must differ by at least 3", &eff.Invalid, "context hysteresis")
		markEffectiveInvalid(&eff.Context.WatchLeave, "matching context thresholds must differ by at least 3", &eff.Invalid, "context hysteresis")
	}
	if eff.Context.WarnEnter.Valid && eff.Context.WarnLeave.Valid && eff.Context.WarnEnter.Value-eff.Context.WarnLeave.Value < 3 {
		markEffectiveInvalid(&eff.Context.WarnEnter, "matching context thresholds must differ by at least 3", &eff.Invalid, "context hysteresis")
		markEffectiveInvalid(&eff.Context.WarnLeave, "matching context thresholds must differ by at least 3", &eff.Invalid, "context hysteresis")
	}
	if eff.Context.CriticalEnter.Valid && eff.Context.CriticalLeave.Valid && eff.Context.CriticalEnter.Value-eff.Context.CriticalLeave.Value < 3 {
		markEffectiveInvalid(&eff.Context.CriticalEnter, "matching context thresholds must differ by at least 3", &eff.Invalid, "context hysteresis")
		markEffectiveInvalid(&eff.Context.CriticalLeave, "matching context thresholds must differ by at least 3", &eff.Invalid, "context hysteresis")
	}
	if eff.Cache.WarnThreshold.Valid && eff.Cache.RecoveredThreshold.Valid && eff.Cache.WarnThreshold.Value >= eff.Cache.RecoveredThreshold.Value {
		markEffectiveInvalid(&eff.Cache.WarnThreshold, "cache warn_threshold must be below recovered_threshold", &eff.Invalid, "cache thresholds")
		markEffectiveInvalid(&eff.Cache.RecoveredThreshold, "cache warn_threshold must be below recovered_threshold", &eff.Invalid, "cache thresholds")
	}
}

func markEffectiveInvalid[T any](value *EffectiveValue[T], message string, invalid *[]string, name string) {
	if value == nil {
		return
	}
	value.Valid = false
	if value.Error == "" {
		value.Error = message
	}
	if invalid != nil {
		for _, existing := range *invalid {
			if existing == name {
				return
			}
		}
		*invalid = append(*invalid, name)
	}
}

func resolveFloat(envKey string, fileVal float64, cfgLoaded bool, resolveSrc func() ValueSource, readEnv func(string, float64) (float64, bool, error), validate func(float64) error) EffectiveValue[float64] {
	envVal, envSet, err := readEnv(envKey, fileVal)
	if err != nil {
		return EffectiveValue[float64]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), err)}
	}
	if envSet {
		if validate != nil {
			if verr := validate(envVal); verr != nil {
				return EffectiveValue[float64]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), verr)}
			}
		}
		return EffectiveValue[float64]{Value: envVal, Source: SourceEnv, RawValue: os.Getenv(envKey), Valid: true}
	}
	valid := true
	if validate != nil {
		if verr := validate(fileVal); verr != nil {
			valid = false
		}
	}
	return EffectiveValue[float64]{Value: fileVal, Source: resolveSrc(), Valid: valid}
}

func resolveInt(envKey string, fileVal int, cfgLoaded bool, resolveSrc func() ValueSource, readEnv func(string, int) (int, bool, error), validate func(int) error) EffectiveValue[int] {
	envVal, envSet, err := readEnv(envKey, fileVal)
	if err != nil {
		return EffectiveValue[int]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), err)}
	}
	if envSet {
		if validate != nil {
			if verr := validate(envVal); verr != nil {
				return EffectiveValue[int]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), verr)}
			}
		}
		return EffectiveValue[int]{Value: envVal, Source: SourceEnv, RawValue: os.Getenv(envKey), Valid: true}
	}
	valid := true
	if validate != nil {
		if verr := validate(fileVal); verr != nil {
			valid = false
		}
	}
	return EffectiveValue[int]{Value: fileVal, Source: resolveSrc(), Valid: valid}
}

func resolveBool(envKey string, fileVal bool, cfgLoaded bool, resolveSrc func() ValueSource, readEnv func(string, bool) (bool, bool, error), validate func(bool) error) EffectiveValue[bool] {
	envVal, envSet, err := readEnv(envKey, fileVal)
	if err != nil {
		return EffectiveValue[bool]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), err)}
	}
	if envSet {
		if validate != nil {
			if verr := validate(envVal); verr != nil {
				return EffectiveValue[bool]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), verr)}
			}
		}
		return EffectiveValue[bool]{Value: envVal, Source: SourceEnv, RawValue: os.Getenv(envKey), Valid: true}
	}
	return EffectiveValue[bool]{Value: fileVal, Source: resolveSrc(), Valid: true}
}

func resolveString(envKey, fileVal string, resolveSrc func() ValueSource, readEnv func(string, string) (string, bool, error), validate func(string) error) EffectiveValue[string] {
	envVal, envSet, err := readEnv(envKey, fileVal)
	if err != nil {
		return EffectiveValue[string]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), err)}
	}
	if envSet {
		if validate != nil {
			if verr := validate(envVal); verr != nil {
				return EffectiveValue[string]{Value: fileVal, Source: resolveSrc(), RawValue: os.Getenv(envKey), Valid: false, Error: fmt.Sprintf("%s=%q is invalid: %v", envKey, os.Getenv(envKey), verr)}
			}
		}
		return EffectiveValue[string]{Value: envVal, Source: SourceEnv, RawValue: os.Getenv(envKey), Valid: true}
	}
	valid := validate == nil || validate(fileVal) == nil
	return EffectiveValue[string]{Value: fileVal, Source: resolveSrc(), Valid: valid}
}
