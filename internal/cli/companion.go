package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
)

// cmdCompanion implements `freeinference companion status|enable|disable`.
func cmdCompanion(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: freeinference companion status|enable|disable [--json]")
		return 2
	}

	subcommand := args[0]
	jsonOut := false
	for _, a := range args[1:] {
		if a == "--json" {
			jsonOut = true
		} else if strings.HasPrefix(a, "--") {
			fmt.Fprintf(stderr, "unknown flag: %s\n", a)
			return 2
		}
	}

	switch subcommand {
	case "status":
		activation := runtime.Evaluate()
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			enc.Encode(map[string]any{
				"enabled":            !activation.Disabled,
				"configured":         activation.EndpointPresent && activation.KeyPresent,
				"runtime_active":     activation.Active,
				"disabled_by_env":    activation.DisabledByEnv,
				"disabled_by_marker": activation.DisabledByMarker,
			})
			return 0
		}
		switch {
		case activation.DisabledByEnv:
			fmt.Fprintln(stdout, "Companion: disabled by environment")
		case activation.DisabledByMarker:
			fmt.Fprintln(stdout, "Companion: disabled persistently")
		case activation.Active:
			fmt.Fprintln(stdout, "Companion: runtime active")
		case activation.EndpointPresent || activation.KeyPresent:
			fmt.Fprintln(stdout, "Companion: enabled, configuration incomplete")
		default:
			fmt.Fprintln(stdout, "Companion: enabled, not configured")
		}
		return 0

	case "enable":
		if err := runtime.EnablePersistently(); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if jsonOut {
			fmt.Fprintln(stdout, `{"enabled":true}`)
			return 0
		}
		fmt.Fprintln(stdout, "Companion enabled")
		return 0

	case "disable":
		if err := runtime.DisablePersistently(); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if jsonOut {
			fmt.Fprintln(stdout, `{"disabled":true}`)
			return 0
		}
		fmt.Fprintln(stdout, "Companion disabled persistently")
		return 0

	default:
		fmt.Fprintf(stderr, "unknown companion subcommand: %s\n", subcommand)
		return 2
	}
}
