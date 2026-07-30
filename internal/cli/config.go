package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
)

// cmdConfig implements `freeinference config` — manage persistent configuration.
// Subcommands: show, set, reset, path
func cmdConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: freeinference config show|set|reset|path [args]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Subcommands:")
		fmt.Fprintln(stderr, "  show [--json]         Show effective configuration with provenance")
		fmt.Fprintln(stderr, "  set <key> <value>     Set a config value (e.g., context.watch_enter 65)")
		fmt.Fprintln(stderr, "  reset [<key>]         Reset all settings or a specific key to default")
		fmt.Fprintln(stderr, "  path                  Show config file location")
		return 2
	}

	switch args[0] {
	case "show":
		return cmdConfigShow(args[1:], stdout, stderr)
	case "set":
		return cmdConfigSet(args[1:], stdout, stderr)
	case "reset":
		return cmdConfigReset(args[1:], stdout, stderr)
	case "path":
		return cmdConfigPath(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown config subcommand: %s\n", args[0])
		return 2
	}
}

func cmdConfigShow(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
			continue
		}
		if strings.HasPrefix(a, "--") {
			fmt.Fprintf(stderr, "unknown flag: %s\n", a)
			return 2
		}
	}

	mgr, err := config.NewManager()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	eff, err := mgr.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(eff); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	printField := func(name, value, source, valid, errMsg string) {
		fmt.Fprintf(stdout, "  %-30s %-10s %s  [%s]\n", name, value, valid, source)
		if errMsg != "" {
			fmt.Fprintf(stdout, "  %-30s %s\n", "", errMsg)
		}
	}

	path, _ := config.ConfigPath()
	printField("Config file", path, "", "✓", "")

	fmt.Fprintln(stdout, "\nContext thresholds:")
	printField("  watch_enter", fmt.Sprintf("%.1f", eff.Context.WatchEnter.Value), string(eff.Context.WatchEnter.Source), boolStr(eff.Context.WatchEnter.Valid), eff.Context.WatchEnter.Error)
	printField("  warn_enter", fmt.Sprintf("%.1f", eff.Context.WarnEnter.Value), string(eff.Context.WarnEnter.Source), boolStr(eff.Context.WarnEnter.Valid), eff.Context.WarnEnter.Error)
	printField("  critical_enter", fmt.Sprintf("%.1f", eff.Context.CriticalEnter.Value), string(eff.Context.CriticalEnter.Source), boolStr(eff.Context.CriticalEnter.Valid), eff.Context.CriticalEnter.Error)
	printField("  watch_leave", fmt.Sprintf("%.1f", eff.Context.WatchLeave.Value), string(eff.Context.WatchLeave.Source), boolStr(eff.Context.WatchLeave.Valid), eff.Context.WatchLeave.Error)
	printField("  warn_leave", fmt.Sprintf("%.1f", eff.Context.WarnLeave.Value), string(eff.Context.WarnLeave.Source), boolStr(eff.Context.WarnLeave.Valid), eff.Context.WarnLeave.Error)
	printField("  critical_leave", fmt.Sprintf("%.1f", eff.Context.CriticalLeave.Value), string(eff.Context.CriticalLeave.Source), boolStr(eff.Context.CriticalLeave.Valid), eff.Context.CriticalLeave.Error)
	printField("  output_reserve", fmt.Sprintf("%d", eff.Context.OutputReserve.Value), string(eff.Context.OutputReserve.Source), boolStr(eff.Context.OutputReserve.Valid), eff.Context.OutputReserve.Error)

	fmt.Fprintln(stdout, "\nCache:")
	printField("  warn_threshold", fmt.Sprintf("%.2f", eff.Cache.WarnThreshold.Value), string(eff.Cache.WarnThreshold.Source), boolStr(eff.Cache.WarnThreshold.Valid), eff.Cache.WarnThreshold.Error)
	printField("  recovered_threshold", fmt.Sprintf("%.2f", eff.Cache.RecoveredThreshold.Value), string(eff.Cache.RecoveredThreshold.Source), boolStr(eff.Cache.RecoveredThreshold.Valid), eff.Cache.RecoveredThreshold.Error)
	printField("  cooldown_mins", fmt.Sprintf("%d", eff.Cache.CooldownMins.Value), string(eff.Cache.CooldownMins.Source), boolStr(eff.Cache.CooldownMins.Valid), eff.Cache.CooldownMins.Error)

	fmt.Fprintln(stdout, "\nRefresh:")
	printField("  interval_mins", fmt.Sprintf("%d", eff.Refresh.IntervalMins.Value), string(eff.Refresh.IntervalMins.Source), boolStr(eff.Refresh.IntervalMins.Valid), eff.Refresh.IntervalMins.Error)

	fmt.Fprintln(stdout, "\nPrivacy:")
	printField("  diagnostic_probes", fmt.Sprintf("%t", eff.Privacy.DiagnosticProbes.Value), string(eff.Privacy.DiagnosticProbes.Source), boolStr(eff.Privacy.DiagnosticProbes.Valid), eff.Privacy.DiagnosticProbes.Error)

	return 0
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func cmdConfigSet(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	var realArgs []string
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		} else {
			realArgs = append(realArgs, a)
		}
	}

	if len(realArgs) != 2 {
		fmt.Fprintln(stderr, "usage: freeinference config set <key> <value>")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Keys: context.watch_enter, context.warn_enter, context.critical_enter,")
		fmt.Fprintln(stderr, "      context.watch_leave, context.warn_leave, context.critical_leave,")
		fmt.Fprintln(stderr, "      context.output_reserve, cache.warn_threshold, cache.recovered_threshold, cache.cooldown_mins,")
		fmt.Fprintln(stderr, "      refresh.interval_mins, privacy.diagnostic_probes")
		return 2
	}

	key, value := realArgs[0], realArgs[1]

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if err := config.SetField(cfg, key, value); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if jsonOut {
		fmt.Fprintf(stdout, `{"key":%q,"value":%q}`+"\n", key, value)
		return 0
	}
	fmt.Fprintf(stdout, "Set %s to %s\n", key, value)
	return 0
}

func cmdConfigReset(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	var realArgs []string
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		} else {
			realArgs = append(realArgs, a)
		}
	}

	if len(realArgs) == 0 {
		if err := config.ResetToDefault(); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if jsonOut {
			fmt.Fprintln(stdout, `{"reset":true,"key":null}`)
			return 0
		}
		fmt.Fprintln(stdout, "All settings reset to defaults")
		return 0
	}
	fmt.Fprintf(stderr, "usage: freeinference config reset [<key>]\n")
	fmt.Fprintln(stderr, "Without arguments, resets all settings to defaults.")
	return 2
}

func cmdConfigPath(args []string, stdout, stderr io.Writer) int {
	path, err := config.ConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}
