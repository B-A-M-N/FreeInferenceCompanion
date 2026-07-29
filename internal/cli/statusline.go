package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/b-a-m-n/freeinference-companion/internal/install"
)

// cmdStatusLine implements `fi status-line install|uninstall`.
func cmdStatusLine(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: fi status-line install|uninstall [--scope user|project|local] [--project <dir>]")
		return 2
	}
	subcommand := args[0]
	rest := args[1:]

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	scope := install.ScopeProject // default: project scope
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = home
	}

	// Parse remaining flags
	for len(rest) > 0 {
		switch rest[0] {
		case "--scope":
			if len(rest) < 2 {
				fmt.Fprintln(stderr, "error: --scope requires a value (user, project, or local)")
				return 2
			}
			switch rest[1] {
			case "user":
				scope = install.ScopeUser
			case "project":
				scope = install.ScopeProject
			case "local":
				scope = install.ScopeLocal
			default:
				fmt.Fprintf(stderr, "error: unknown scope %q (user, project, local)\n", rest[1])
				return 2
			}
			rest = rest[2:]
		case "--project":
			if len(rest) < 2 {
				fmt.Fprintln(stderr, "error: --project requires a value")
				return 2
			}
			projectRoot, err = filepath.Abs(rest[1])
			if err != nil {
				fmt.Fprintf(stderr, "error: invalid project directory: %v\n", err)
				return 2
			}
			rest = rest[2:]
		default:
			fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", rest[0])
			return 2
		}
	}

	switch subcommand {
	case "install":
		binaryPath := resolveSelfPath()
		if err := install.InstallClaudeStatusLine(home, binaryPath, scope, projectRoot, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "uninstall":
		if err := install.UninstallClaudeStatusLine(home, scope, projectRoot, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %s\n", subcommand)
		return 2
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
