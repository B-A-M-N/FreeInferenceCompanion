// Package cli implements the fi command-line interface. Command functions
// return exit codes; only main() calls os.Exit. Hook commands always exit 0.
package cli

import (
	"fmt"
	"io"

	"github.com/b-a-m-n/freeinference-companion/internal/state"
)

// Version and Commit are stamped by main from ldflags.
var (
	Version = "0.1.0"
	Commit  = "dev"
)

// Run dispatches a command and returns the process exit code.
// Hook commands are fully fail-open: they always return 0.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) (exitCode int) {
	if len(args) < 2 {
		printUsage(stderr)
		return 1
	}

	cmd := args[1]
	rest := args[2:]

	// Hook entry path: never block the client, even on panic or broken state.
	if cmd == "hook" {
		defer func() {
			if recover() != nil {
				exitCode = 0
			}
		}()
		runHook(rest, stdin, stdout, stderr)
		return 0
	}

	paths, err := state.NewPaths()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := paths.EnsureDirs(); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	switch cmd {
	case "status":
		return cmdStatus(paths, rest, stdin, stdout, stderr)
	case "sessions":
		return cmdSessions(paths, rest, stdout, stderr)
	case "snapshot":
		return cmdSnapshot(paths, rest, stdin, stdout, stderr)
	case "render":
		return cmdRender(paths, rest, stdin, stdout, stderr)
	case "models":
		return cmdModels(paths, rest, stdout, stderr)
	case "doctor":
		return cmdDoctor(paths, rest, stdout, stderr)
	case "report":
		return cmdReport(paths, rest, stdout, stderr)
	case "dashboard":
		return cmdDashboard(stdout, stderr)
	case "context":
		return cmdContext(paths, rest, stdin, stdout, stderr)
	case "refresh":
		return cmdRefresh(paths, rest, stdout, stderr)
	case "cache":
		return cmdCache(paths, rest, stdout, stderr)
	case "status-line":
		return cmdStatusLine(rest, stdout, stderr)
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", cmd)
		printUsage(stderr)
		return 1
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `FreeInference Companion v`+Version+`

Usage:
  fi status [--client <type>] [--compact] [--session <id>]
  fi sessions
  fi snapshot --json [--client <type>] [--session <id>]
  fi render --mode line|expanded [--client <type>] [--session <id>]
  fi models [--model <name>] [--refresh]
  fi doctor [--probe --model <name>]
  fi report [--client <type>] [--session <id>] [--format markdown|json]
  fi dashboard
  fi context [--client <type>] [--session <id>]
  fi refresh [--force] [--if-stale --detach] [--worker models|health]
  fi hook <client> <event>
  fi status-line install|uninstall

Environment:
  FREEINFERENCE_API_KEY    FreeInference API key
  FREEINFERENCE_BASE_URL   API base URL (default: https://freeinference.org/v1)
  FI_HEALTH_URL            Health monitoring URL (optional)
  FI_CACHE_DIR             Cache directory (default: ~/.cache/freeinference-companion)
  FI_SESSION_ID            Explicit session override for status/context/report
  FI_PROVIDER              Set to "freeinference" to force provider detection
`)
}
