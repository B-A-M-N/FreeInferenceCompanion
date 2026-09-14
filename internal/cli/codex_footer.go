package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/install"
	"github.com/b-a-m-n/freeinference-companion/internal/render"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

// cmdCodexFooter manages Codex's native tui.status_line array and the
// Companion's rollout-backed rich line. Codex owns its native footer; the
// render subcommand is designed for tmux status bars and other host surfaces.
func cmdCodexFooter(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: freeinference codex-footer install|uninstall|status|render [--json]")
		return 2
	}
	subcommand := args[0]
	jsonOut := false
	colorMode := render.ColorAuto
	for _, arg := range args[1:] {
		switch arg {
		case "--json":
			jsonOut = true
		case "--help", "-h":
			fmt.Fprint(stdout, helpCodexFooter)
			return 0
		default:
			if strings.HasPrefix(arg, "--color=") {
				colorMode = render.ParseColorMode(strings.TrimPrefix(arg, "--color="))
				continue
			}
			fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", arg)
			return 2
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	configPath, err := runtime.CodexConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	switch subcommand {
	case "render":
		usage, usageErr := latestCodexUsage()
		if usageErr != nil {
			if jsonOut {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"client":       "codex",
					"availability": "unavailable",
					"reason":       codexUsageError(usageErr),
				})
			}
			return 0
		}
		activation := runtime.EvaluateForClient(runtime.ClientCodex)
		if !activation.Active {
			return 0
		}
		snap := codexSnapshotFromUsage(usage, activation)
		aid := codexActivationID(activation)
		vm := render.BuildViewModel(Version, snap, nil, aid, usage.ObservedAt, true, "codex", snap.Session.ID)
		if jsonOut {
			data, err := vm.JSON()
			if err != nil {
				return 1
			}
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		rc := render.DefaultRenderConfig()
		rc.ColorMode = render.ApplyEnv(colorMode)
		if line := vm.Line(rc); line != "" {
			fmt.Fprintln(stdout, line)
		}
		return 0
	case "install":
		if err := install.InstallCodexTUI(home, configPath, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "uninstall":
		if err := install.UninstallCodexTUI(home, configPath, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "status":
		status, err := install.InspectCodexTUI(home, configPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(status)
			return 0
		}
		fmt.Fprintf(stdout, "Config:     %s\n", status.ConfigPath)
		fmt.Fprintf(stdout, "Configured: %t\n", status.Configured)
		fmt.Fprintf(stdout, "Installed:  %t\n", status.Installed)
		fmt.Fprintf(stdout, "Referenced: %t\n", status.Referenced)
		fmt.Fprintf(stdout, "Status:     %s\n", status.Status)
		if len(status.StatusLine) > 0 {
			fmt.Fprintf(stdout, "Items:      %v\n", status.StatusLine)
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %s\n", subcommand)
		return 2
	}
}

// cmdCodexSurface provides a host-neutral Codex telemetry line. Unlike the
// native footer, this contract is owned by Companion and can run in tmux, a
// terminal title host, or any launcher-backed status process.
func cmdCodexSurface(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "render" {
		fmt.Fprintln(stderr, "Usage: freeinference codex-surface render [--json] [--color auto|always|never]")
		return 2
	}
	renderArgs := append([]string{"render"}, args[1:]...)
	return cmdCodexFooter(renderArgs, stdout, stderr)
}
