package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
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

	// 1. Cache directory exists and is writable.
	add("Cache directory", checkCacheDir(paths))

	// 2. State files readable.
	add("State schema", checkStateReadable(paths))
	add("Configuration", checkConfigValid())

	// 3. Binary resolvable.
	add("freeinference binary", checkBinaryResolvable())

	// 4. Claude hook configuration present.
	add("Claude hook config", checkClaudeHookConfig())

	// 5. Status-line wrapper valid.
	add("Status-line wrapper", checkStatusLineWrapper())

	// 6. Provider detection.
	det := adapters.DetectProvider()
	if det.Confirmed {
		add("Provider detection", api.CheckResult{State: api.CheckPass, Detail: det.Name + " via " + det.Source})
	} else {
		add("Provider detection", api.CheckResult{State: api.CheckUnknown, Detail: "provider unknown — FreeInference features stay quiet"})
	}

	// 7. Health source configured.
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

	// 8. Model catalog reachable.
	// In disabled mode, skip all network-dependent checks.
	activation := runtime.Evaluate()
	disabled := os.Getenv("FI_DISABLED") == "1" || activation.Disabled
	if disabled {
		// Installation-convenience checks (binary on PATH, hook config,
		// status-line wrapper) are downgraded to warnings while disabled:
		// they describe setup completeness, not correctness, and must not
		// make `doctor` exit 1 — diagnostics stay usable when disabled.
		for i := range checks {
			switch checks[i].name {
			case "freeinference binary", "Claude hook config", "Status-line wrapper":
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
			gs.AccountUsage == nil && len(gs.CircuitBreakers) == 0
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
	} {
		if !v.valid {
			invalid = append(invalid, v.name+": "+v.err)
		}
	}
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
