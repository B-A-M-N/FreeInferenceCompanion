package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/install"
)

// cmdStatusLine implements `fi status-line install|uninstall`.
func cmdStatusLine(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: fi status-line install|uninstall")
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	switch args[0] {
	case "install":
		binaryPath := resolveSelfPath()
		if err := install.InstallClaudeStatusLine(home, binaryPath, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "uninstall":
		if err := install.UninstallClaudeStatusLine(home, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %s\n", args[0])
		return 1
	}
}

// resolveSelfPath returns the absolute path of the running binary when it is
// a real on-disk file (not a test harness), else "" so the wrapper falls
// back to PATH lookup.
func resolveSelfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if info, err := os.Stat(exe); err != nil || info.IsDir() {
		return ""
	}
	return exe
}
