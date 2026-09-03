package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/internal/install"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

type doctorCheck struct {
	name   string
	result api.CheckResult
}

// cmdDoctor implements `freeinference doctor`. All independent checks run; the command
// exits 1 if any check failed, 2 on usage error, 0 otherwise.
func cmdDoctor(paths state.Paths, args []string, stdout, _ io.Writer) int {
	probe := false
	probeModel := ""
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--probe":
			probe = true
		case "--model":
			if i+1 >= len(args) {
				fmt.Fprintln(stdout, "usage error: --model requires a value")
				return 2
			}
			i++
			probeModel = args[i]
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(args[i], "--") {
				fmt.Fprintf(stdout, "usage error: unknown flag %q\n", args[i])
				return 2
			}
			fmt.Fprintf(stdout, "usage error: unexpected argument %q\n", args[i])
			return 2
		}
	}

	var checks []doctorCheck
	add := func(name string, r api.CheckResult) {
		checks = append(checks, doctorCheck{name, r})
	}
	activation := activationForCLICommand("doctor", args)

	// 1. Cache directory exists and is writable.
	add("Cache directory", checkCacheDir(paths))

	// 2. State files readable.
	add("State schema", checkStateReadable(paths))
	add("Configuration", checkConfigValid())

	// 3. Binary resolvable.
	add("freeinference binary", checkBinaryResolvable())

	// 4. Claude hook configuration present.
	add("Claude hook config", checkClaudeHookConfig())
	// 5. Codex skill installation is intentionally separate from lifecycle
	// hooks: the Companion Codex package is skill-only and uses Codex's native
	// marketplace manager.
	add("Codex skill installed", checkCodexPluginInstalled())
	add("Codex plugin registration", checkCodexPluginRegistration())
	add("Codex native footer", checkCodexNativeFooter())

	// 6. Status-line wrapper valid.
	add("Status-line wrapper", checkStatusLineWrapper())

	// 7. Provider detection. Generic environment detection remains useful for
	// provider-level setups, while the client-specific checks below prevent a
	// coincidental shell key from being treated as client evidence.
	det := adapters.DetectProvider()
	if activation.Active {
		add("Provider detection", api.CheckResult{State: api.CheckPass, Detail: "freeinference via " + activation.EndpointSource})
	} else if det.Confirmed {
		add("Provider detection", api.CheckResult{State: api.CheckPass, Detail: det.Name + " via " + det.Source})
	} else {
		add("Provider detection", api.CheckResult{State: api.CheckUnknown, Detail: "provider unknown — FreeInference features stay quiet"})
	}
	claudeActivation := runtime.EvaluateForClient(runtime.ClientClaudeCode)
	if claudeActivation.Active {
		add("Claude runtime", api.CheckResult{State: api.CheckPass, Detail: "FreeInference Anthropic route confirmed"})
	} else {
		add("Claude runtime", api.CheckResult{State: api.CheckUnknown, Detail: "not confirmed for Claude Code"})
	}
	codexActivation := runtime.EvaluateForClient(runtime.ClientCodex)
	if codexActivation.Active {
		add("Codex provider", api.CheckResult{State: api.CheckPass, Detail: "selected FreeInference provider confirmed"})
	} else if codexActivation.InactiveReason == runtime.ReasonCodexProviderUnverified {
		add("Codex provider", api.CheckResult{State: api.CheckUnknown, Detail: "current Codex provider unverified"})
	} else {
		add("Codex provider", api.CheckResult{State: api.CheckUnknown, Detail: "FreeInference is not the selected Codex provider"})
	}

	// 7. Trace correlation is a launch-time, client-specific feature. These
	// checks report actual executable, route, and configuration capability
	// without printing header values.
	traceEnabled, traceConfigValid, traceConfigErr := effectiveTracing()
	if traceConfigErr != nil {
		add("Tracing feature", api.CheckResult{State: api.CheckUnknown, Detail: "configuration unavailable"})
	} else if !traceConfigValid {
		add("Tracing feature", api.CheckResult{State: api.CheckFail, Detail: "FI_TRACING is invalid"})
	} else if traceEnabled {
		add("Tracing feature", api.CheckResult{State: api.CheckPass, Detail: "enabled for Companion-launched sessions"})
	} else {
		add("Tracing feature", api.CheckResult{State: api.CheckWarn, Detail: "disabled by configuration"})
	}
	add("Trace header support", api.CheckResult{State: api.CheckPass, Detail: "X-Session-ID supported by FreeInference"})
	claudeExecutable, claudeExecutableOK := checkClientExecutable("claude")
	add("Claude executable", claudeExecutable)
	codexExecutable, codexExecutableOK := checkClientExecutable("codex")
	add("Codex executable", codexExecutable)

	claudeHeadersOK := false
	if claudeActivation.Active {
		if err := tracing.ValidateClaudeCustomHeaders(os.Getenv("ANTHROPIC_CUSTOM_HEADERS")); err != nil {
			add("Claude trace headers", api.CheckResult{State: api.CheckWarn, Detail: "ANTHROPIC_CUSTOM_HEADERS cannot be safely merged; launch will fail open"})
		} else {
			claudeHeadersOK = true
			add("Claude trace headers", api.CheckResult{State: api.CheckPass, Detail: "custom-header merge available"})
		}
	} else {
		add("Claude trace headers", api.CheckResult{State: api.CheckUnknown, Detail: "Claude Code route unverified"})
	}
	claudeLauncherOK := claudeExecutableOK && claudeActivation.Active && claudeHeadersOK
	add("Claude launcher", traceCapabilityResult(claudeLauncherOK, claudeExecutableOK, claudeActivation.Active, claudeHeadersOK, "claude"))

	codexHeadersOK := false
	if codexActivation.Active {
		path, pathErr := runtime.CodexConfigPath()
		if pathErr != nil {
			add("Codex trace headers", api.CheckResult{State: api.CheckUnknown, Detail: "Codex config path unavailable"})
		} else if mapping, inspectErr := runtime.InspectCodexTraceHeaders(path, codexActivation.Evidence.ProviderID); inspectErr != nil {
			add("Codex trace headers", api.CheckResult{State: api.CheckWarn, Detail: "env_http_headers mapping unavailable; run will fail open"})
		} else if len(mapping.Conflicts) > 0 {
			add("Codex trace headers", api.CheckResult{State: api.CheckWarn, Detail: "Companion header mapping conflict; existing values will not be replaced"})
		} else if mapping.Ready {
			codexHeadersOK = true
			add("Codex trace headers", api.CheckResult{State: api.CheckPass, Detail: "Companion env_http_headers mappings confirmed"})
		} else {
			if codexConfigInstallable(path) {
				add("Codex trace headers", api.CheckResult{State: api.CheckWarn, Detail: "Companion env_http_headers mappings incomplete; run `freeinference trace setup --client codex` first"})
				add("Codex trace setup", api.CheckResult{State: api.CheckPass, Detail: "selected config is writable; setup is available"})
			} else {
				add("Codex trace headers", api.CheckResult{State: api.CheckWarn, Detail: "Companion env_http_headers mappings incomplete and config is not writable"})
			}
		}
	} else {
		add("Codex trace headers", api.CheckResult{State: api.CheckUnknown, Detail: "selected Codex provider unverified"})
	}
	codexLauncherOK := codexExecutableOK && codexActivation.Active && codexHeadersOK
	add("Codex launcher", traceCapabilityResult(codexLauncherOK, codexExecutableOK, codexActivation.Active, codexHeadersOK, "codex"))
	if claudeLauncherOK && codexLauncherOK {
		add("Trace client support", api.CheckResult{State: api.CheckPass, Detail: "Claude and Codex launchers verified"})
	} else {
		add("Trace client support", api.CheckResult{State: api.CheckWarn, Detail: "one or more client launchers are unavailable or unverified"})
	}
	if inherited, ok := tracing.EnvironmentTrace(); ok && (claudeActivation.Active || codexActivation.Active) {
		add("Trace correlation", api.CheckResult{State: api.CheckWarn, Detail: "inherited trace present but unverified; durable provenance requires a launch receipt"})
		if tracing.ValidateTraceID(inherited.SessionID) {
			add("Trace ID format", api.CheckResult{State: api.CheckPass, Detail: "opaque bounded ID format valid"})
		} else {
			add("Trace ID format", api.CheckResult{State: api.CheckFail, Detail: "inherited ID format invalid"})
		}
	} else {
		add("Trace correlation", api.CheckResult{State: api.CheckUnknown, Detail: "no active Companion launch trace"})
		add("Trace ID format", api.CheckResult{State: api.CheckUnknown, Detail: "no active trace ID"})
	}

	// 8. Health source configured.
	if healthURL := os.Getenv("FI_HEALTH_URL"); healthURL != "" {
		sanitized, err := api.NormalizeHealthURL(healthURL)
		if err != nil {
			add("Health source", api.CheckResult{State: api.CheckFail, Detail: "configured but invalid: " + err.Error()})
		} else if adapters.IsFreeInferenceURL(sanitized.Origin) {
			add("Health source", api.CheckResult{State: api.CheckPass, Detail: "configured (" + sanitized.Origin + ")"})
		} else {
			add("Health source", api.CheckResult{State: api.CheckFail, Detail: "configured but not a FreeInference host: " + sanitized.Origin})
		}
	} else {
		add("Health source", api.CheckResult{State: api.CheckUnknown, Detail: "not configured (optional)"})
	}

	// 9. Model catalog reachable.
	// In disabled mode, skip all network-dependent checks.
	disabled := os.Getenv("FI_DISABLED") == "1" || activation.Disabled
	if disabled {
		// Installation-convenience checks (binary on PATH, hook config,
		// status-line wrapper) are downgraded to warnings while disabled:
		// they describe setup completeness, not correctness, and must not
		// make `doctor` exit 1 — diagnostics stay usable when disabled.
		for i := range checks {
			switch checks[i].name {
			case "freeinference binary", "Claude hook config", "Codex skill installed", "Codex plugin registration", "Codex native footer", "Status-line wrapper":
				if checks[i].result.State == api.CheckFail {
					checks[i].result.State = api.CheckWarn
				}
			}
		}
		add("Model catalog", api.CheckResult{State: api.CheckUnknown, Detail: "skipped - disabled"})
		add("API key format", api.CheckResult{State: api.CheckUnknown, Detail: "skipped - disabled"})
		add("Authentication", api.CheckResult{State: api.CheckUnknown, Detail: "skipped - disabled"})
		add("Model access", api.CheckResult{State: api.CheckUnknown, Detail: "skipped - disabled"})
	} else {
		client, clientErr := newAPIClient()
		if clientErr != nil {
			add("API endpoint", api.CheckResult{State: api.CheckFail, Detail: endpointFailDetail(clientErr)})
			add("Model catalog", api.CheckResult{State: api.CheckUnknown, Detail: "skipped due to invalid endpoint"})
			add("API key format", api.CheckResult{State: api.CheckUnknown, Detail: "skipped due to invalid endpoint"})
			add("Authentication", api.CheckResult{State: api.CheckUnknown, Detail: "skipped due to invalid endpoint"})
			add("Model access", api.CheckResult{State: api.CheckUnknown, Detail: "skipped due to invalid endpoint"})
		} else {
			probeResult := client.Probe()
			add("API endpoint", probeResult.Endpoint)
			add("Model catalog", probeResult.Catalog)

			if os.Getenv("FREEINFERENCE_API_KEY") != "" {
				if api.VerifyAPIKey(os.Getenv("FREEINFERENCE_API_KEY")) {
					add("API key format", api.CheckResult{State: api.CheckPass, Detail: "present, format valid (not verified)"})
				} else {
					add("API key format", api.CheckResult{State: api.CheckUnknown, Detail: "unusual format"})
				}
			} else {
				add("API key format", api.CheckResult{State: api.CheckUnknown, Detail: "not set"})
			}
			add("Authentication", probeResult.Authentication)
			add("Model access", probeResult.ModelAccess)
		}

		// 9. Optional synthetic inference probe (explicit consent + model required).
		if probe {
			model := probeModel
			if model == "" {
				add("Inference probe", api.CheckResult{State: api.CheckUnknown, Detail: "no model given -- pass --model to specify a model for the synthetic probe"})
			} else if client == nil {
				add("Inference probe", api.CheckResult{State: api.CheckUnknown, Detail: "skipped due to invalid endpoint"})
			} else {
				pr := client.ProbeInference(model)
				add("Probe endpoint", pr.Endpoint)
				add("Probe authentication", pr.Authentication)
				add("Probe model access", pr.ModelAccess)
			}
		}
	}

	// 10. Circuit breaker status.
	gs := loadGlobal(paths)
	if len(gs.CircuitBreakers) > 0 {
		for _, cb := range gs.CircuitBreakers {
			state := cb.State
			detail := fmt.Sprintf("%s (failures: %d", state, cb.FailureCount)
			if cb.LastFailureAt != nil {
				detail += fmt.Sprintf(", last: %s", cb.LastFailureAt.Format("15:04:05"))
			}
			if cb.NextRetryAt != nil {
				detail += fmt.Sprintf(", retry: %s", cb.NextRetryAt.Format("15:04:05"))
			}
			detail += ")"
			if cb.State == schema.CircuitOpen {
				add(fmt.Sprintf("Circuit: %s", cb.Endpoint), api.CheckResult{State: api.CheckFail, Detail: detail})
			} else {
				add(fmt.Sprintf("Circuit: %s", cb.Endpoint), api.CheckResult{State: api.CheckPass, Detail: detail})
			}
		}
	} else {
		add("Circuit breakers", api.CheckResult{State: api.CheckUnknown, Detail: "no state recorded"})
	}

	// Print results.
	failures := 0
	warnings := 0
	for _, c := range checks {
		symbol := "?"
		switch c.result.State {
		case api.CheckPass:
			symbol = "✓"
		case api.CheckWarn:
			symbol = "⚠"
			warnings++
		case api.CheckFail:
			symbol = "✗"
			failures++
		}
		if !jsonOut {
			line := fmt.Sprintf("%-22s %s", c.name+":", symbol)
			if c.result.Detail != "" {
				line += " " + c.result.Detail
			}
			fmt.Fprintln(stdout, line)
		}
	}

	if jsonOut {
		type doctorResult struct {
			Name   string          `json:"name"`
			Result api.CheckResult `json:"result"`
		}
		results := make([]doctorResult, len(checks))
		for i, c := range checks {
			results[i] = doctorResult{Name: c.name, Result: c.result}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{
			"results":  results,
			"failures": failures,
			"warnings": warnings,
		})
		if failures > 0 {
			return 1
		}
		return 0
	}

	fmt.Fprintln(stdout)
	if failures > 0 {
		fmt.Fprintf(stdout, "Doctor complete: %d check(s) failed.\n", failures)
		return 1
	}
	if warnings > 0 {
		fmt.Fprintf(stdout, "Doctor complete: %d warning(s).\n", warnings)
		return 0
	}
	fmt.Fprintln(stdout, "Doctor complete.")
	return 0
}

func effectiveTracing() (enabled, valid bool, err error) {
	mgr, err := config.NewManager()
	if err != nil {
		return false, false, err
	}
	eff, err := mgr.Resolve()
	if err != nil {
		return false, false, err
	}
	return eff.Tracing.Enabled.Value, eff.Tracing.Enabled.Valid, nil
}

func checkClientExecutable(name string) (api.CheckResult, bool) {
	if _, err := exec.LookPath(name); err != nil {
		return api.CheckResult{State: api.CheckWarn, Detail: "not found on PATH"}, false
	}
	return api.CheckResult{State: api.CheckPass, Detail: "found on PATH"}, true
}

func codexConfigInstallable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0200 != 0
}

func traceCapabilityResult(ready, executable, route, headers bool, name string) api.CheckResult {
	if ready {
		return api.CheckResult{State: api.CheckPass, Detail: name + " launcher verified"}
	}
	missing := make([]string, 0, 3)
	if !executable {
		missing = append(missing, "executable missing")
	}
	if !route {
		missing = append(missing, "FreeInference route unverified")
	}
	if !headers {
		missing = append(missing, "trace header setup unavailable")
	}
	return api.CheckResult{State: api.CheckWarn, Detail: strings.Join(missing, "; ")}
}

func checkCacheDir(paths state.Paths) api.CheckResult {
	info, err := os.Stat(paths.CacheDir)
	if err != nil || !info.IsDir() {
		return api.CheckResult{State: api.CheckFail, Detail: "cache directory missing"}
	}
	probe := filepath.Join(paths.CacheDir, ".doctor-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0600); err != nil {
		return api.CheckResult{State: api.CheckFail, Detail: "cache directory not writable"}
	}
	os.Remove(probe)
	return api.CheckResult{State: api.CheckPass}
}

func checkStateReadable(paths state.Paths) api.CheckResult {
	globalDir := paths.GlobalDir()
	entries, err := os.ReadDir(globalDir)
	if err != nil || len(entries) == 0 {
		return api.CheckResult{State: api.CheckPass, Detail: "no state yet (first run)"}
	}

	// Count JSON resources vs. non-state files (locks, temp files, etc.).
	jsonCount := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonCount++
		}
	}
	if jsonCount == 0 {
		return api.CheckResult{State: api.CheckPass, Detail: "no state yet (first run)"}
	}

	// Verify each global resource loads. LoadGlobal quarantines corrupt
	// files, so a load error here means something more serious.
	gs, loadErr := state.LoadGlobal(paths)
	if loadErr != nil {
		// Check if the load produced partial state (some resources OK).
		allNil := gs.Health == nil && gs.Models == nil &&
			gs.AccountUsage == nil && gs.PublicStatus == nil && len(gs.CircuitBreakers) == 0
		if allNil {
			return api.CheckResult{State: api.CheckFail, Detail: "global state files present but unreadable"}
		}
		return api.CheckResult{State: api.CheckWarn, Detail: "some global state resources failed to load (corrupt files quarantined)"}
	}
	_ = gs
	return api.CheckResult{State: api.CheckPass}
}

func checkBinaryResolvable() api.CheckResult {
	onPath := false
	if _, err := lookPathFI(); err == nil {
		onPath = true
	}
	runningBinary := false
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(exe); err == nil {
			runningBinary = true
		}
	}
	switch {
	case onPath && runningBinary:
		return api.CheckResult{State: api.CheckPass, Detail: "on PATH"}
	case runningBinary:
		// Binary exists but `freeinference` is not on PATH — hooks configured to call
		// `freeinference` by name will fail to resolve it.
		return api.CheckResult{State: api.CheckFail, Detail: "running binary found, but `freeinference` is not on PATH — plugin hooks may not resolve it"}
	default:
		return api.CheckResult{State: api.CheckFail, Detail: "freeinference not resolvable"}
	}
}

func checkConfigValid() api.CheckResult {
	mgr, err := config.NewManager()
	if err != nil {
		return api.CheckResult{State: api.CheckWarn, Detail: "config manager: " + err.Error()}
	}
	eff, err := mgr.Resolve()
	if err != nil {
		if eff != nil && eff.LoadError != "" {
			return api.CheckResult{State: api.CheckWarn, Detail: "config load: " + eff.LoadError}
		}
		return api.CheckResult{State: api.CheckWarn, Detail: "config resolve: " + err.Error()}
	}
	// Count invalid settings
	var invalid []string
	for _, v := range []struct {
		name  string
		valid bool
		err   string
	}{
		{"watch_enter", eff.Context.WatchEnter.Valid, eff.Context.WatchEnter.Error},
		{"warn_enter", eff.Context.WarnEnter.Valid, eff.Context.WarnEnter.Error},
		{"critical_enter", eff.Context.CriticalEnter.Valid, eff.Context.CriticalEnter.Error},
		{"watch_leave", eff.Context.WatchLeave.Valid, eff.Context.WatchLeave.Error},
		{"warn_leave", eff.Context.WarnLeave.Valid, eff.Context.WarnLeave.Error},
		{"critical_leave", eff.Context.CriticalLeave.Valid, eff.Context.CriticalLeave.Error},
		{"output_reserve", eff.Context.OutputReserve.Valid, eff.Context.OutputReserve.Error},
		{"cache.warn_threshold", eff.Cache.WarnThreshold.Valid, eff.Cache.WarnThreshold.Error},
		{"cache.recovered_threshold", eff.Cache.RecoveredThreshold.Valid, eff.Cache.RecoveredThreshold.Error},
		{"cache.cooldown_mins", eff.Cache.CooldownMins.Valid, eff.Cache.CooldownMins.Error},
		{"refresh.interval_mins", eff.Refresh.IntervalMins.Valid, eff.Refresh.IntervalMins.Error},
		{"refresh.stale_mins", eff.Refresh.StaleMins.Valid, eff.Refresh.StaleMins.Error},
		{"reporting.level", eff.Reporting.Level.Valid, eff.Reporting.Level.Error},
		{"tracing.enabled", eff.Tracing.Enabled.Valid, eff.Tracing.Enabled.Error},
	} {
		if !v.valid {
			invalid = append(invalid, v.name+": "+v.err)
		}
	}
	invalid = append(invalid, eff.Invalid...)
	if len(invalid) > 0 {
		detail := fmt.Sprintf("%d invalid settings: ", len(invalid))
		for i, s := range invalid {
			if i > 0 {
				detail += "; "
			}
			detail += s
		}
		return api.CheckResult{State: api.CheckWarn, Detail: detail}
	}
	detail := fmt.Sprintf("all settings valid (%s)", eff.Context.WatchEnter.Source)
	return api.CheckResult{State: api.CheckPass, Detail: detail}
}

func checkClaudeHookConfig() api.CheckResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "no home directory"}
	}
	// Plugin installation places hooks under ~/.claude/plugins.
	// NOTE: the status-line wrapper is a SEPARATE check (checkStatusLineWrapper).
	// Do not conflate the two — a pass here must mean hooks are installed,
	// not just the status-line wrapper.
	primaryHook := filepath.Join(home, ".claude", "plugins", "freeinference-companion", "hooks", "hooks.json")
	if _, err := os.Stat(primaryHook); err == nil {
		return api.CheckResult{State: api.CheckPass, Detail: primaryHook}
	}
	// Scan plugin cache for our hooks file. Match on the exact hook runner
	// path that our installer writes, not a loose substring that could
	// false-positive on unrelated plugins.
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "plugins", "*", "*", "hooks", "hooks.json"))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err == nil && strings.Contains(string(data), "run-hook.sh") && strings.Contains(string(data), "freeinference-companion") {
			return api.CheckResult{State: api.CheckPass, Detail: m}
		}
	}
	return api.CheckResult{State: api.CheckUnknown, Detail: "not installed as a Claude plugin"}
}

func codexPluginRoots(home string) []string {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	roots := []string{filepath.Join(codexHome, "plugins", "freeinference-companion")}
	matches, _ := filepath.Glob(filepath.Join(codexHome, "plugins", "cache", "*", "*", "*"))
	for _, match := range matches {
		duplicate := false
		for _, root := range roots {
			if filepath.Clean(root) == filepath.Clean(match) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			roots = append(roots, match)
		}
	}
	return roots
}

func codexPluginManifest(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, ".codex-plugin", "plugin.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(data, &manifest) == nil && manifest.Name == "freeinference-companion"
}

func checkCodexPluginInstalled() api.CheckResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "no home directory"}
	}
	for _, root := range codexPluginRoots(home) {
		if codexPluginManifest(root) {
			return api.CheckResult{State: api.CheckPass, Detail: "manifest found at " + root}
		}
	}
	return api.CheckResult{State: api.CheckUnknown, Detail: "not installed as a Codex plugin"}
}

func checkCodexPluginRegistration() api.CheckResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "no home directory"}
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	cacheRoot := filepath.Join(codexHome, "plugins", "cache")
	versions, _ := filepath.Glob(filepath.Join(cacheRoot, "*", "freeinference-companion", "*"))
	for _, version := range versions {
		if codexPluginManifest(version) {
			return api.CheckResult{State: api.CheckPass, Detail: "Codex marketplace cache found at " + version}
		}
	}
	if codexPluginManifest(filepath.Join(codexHome, "plugins", "freeinference-companion")) {
		return api.CheckResult{State: api.CheckWarn, Detail: "skill files are present, but Codex marketplace installation is not established"}
	}
	return api.CheckResult{State: api.CheckUnknown, Detail: "Codex marketplace installation not detected"}
}

func checkCodexNativeFooter() api.CheckResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "no home directory"}
	}
	configPath, err := runtime.CodexConfigPath()
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "Codex config path unavailable"}
	}
	status, err := install.InspectCodexTUI(home, configPath)
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "native footer configuration unavailable"}
	}
	switch status.Status {
	case "installed":
		return api.CheckResult{State: api.CheckPass, Detail: "Companion-owned Codex native footer configured"}
	case "configured_unmanaged":
		return api.CheckResult{State: api.CheckPass, Detail: "Codex native footer configured outside Companion"}
	case "drifted":
		return api.CheckResult{State: api.CheckWarn, Detail: "Companion footer ownership drifted; reinstall/uninstall will require reconciliation"}
	default:
		return api.CheckResult{State: api.CheckUnknown, Detail: "Codex native footer not configured"}
	}
}

func checkStatusLineWrapper() api.CheckResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "no home directory"}
	}
	wrapper := filepath.Join(home, ".claude", "statusline-freeinference.sh")
	info, err := os.Stat(wrapper)
	if err != nil {
		return api.CheckResult{State: api.CheckUnknown, Detail: "not installed"}
	}
	if info.Mode()&0111 == 0 {
		return api.CheckResult{State: api.CheckFail, Detail: "wrapper not executable"}
	}
	// Verify that Claude settings actually point to this wrapper. An
	// executable wrapper that isn't referenced by settings is misleading.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		if !strings.Contains(string(data), "statusline-freeinference") {
			return api.CheckResult{State: api.CheckWarn, Detail: "wrapper exists but Claude settings don't reference it"}
		}
	}
	return api.CheckResult{State: api.CheckPass}
}

// endpointFailDetail returns a sanitized, user-facing description of an
// endpoint-validation error. It never echoes the raw URL (which may carry
// userinfo or credential-shaped substrings); it reports the failure category.
func endpointFailDetail(err error) string {
	if err == nil {
		return "FREEINFERENCE_BASE_URL is invalid"
	}
	return "FREEINFERENCE_BASE_URL is invalid: " + api.SanitizeEndpointError(err)
}
func lookPathFI() (string, error) {
	paths := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(paths) {
		candidate := filepath.Join(dir, "freeinference")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("freeinference not found on PATH")
}
