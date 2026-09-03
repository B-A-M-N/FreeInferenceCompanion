package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/install"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

// cmdCodexFooter manages Codex's native tui.status_line array. It is kept
// separate from Claude's script-backed status-line wrapper because Codex owns
// rendering and does not expose an arbitrary plugin footer command.
func cmdCodexFooter(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: freeinference codex-footer install|uninstall|status [--json]")
		return 2
	}
	subcommand := args[0]
	jsonOut := false
	for _, arg := range args[1:] {
		switch arg {
		case "--json":
			jsonOut = true
		case "--help", "-h":
			fmt.Fprint(stdout, helpCodexFooter)
			return 0
		default:
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
