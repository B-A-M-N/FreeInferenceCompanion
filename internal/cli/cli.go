// Package cli implements the fi command-line interface. Command functions
// return exit codes; only main() calls os.Exit. Hook commands always exit 0.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

// Version and Commit are stamped by main from ldflags.
// Canonical version source for the CLI binary and plugin manifests.
var (
	// Version is the semver string injected at build time via -ldflags.
	// The fallback matches the plugin manifest versions.
	Version = version.Version
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

	// Derive an activation identity so global state is namespaced under
	// providers/<id>/ and different endpoints/keys don't share data.
	activation := runtime.Evaluate()

	// Commands that read/write provider-level state (models, health, circuit
	// breakers, account usage) require an active FreeInference runtime.
	// Session-only commands (sessions, snapshot, context, render) may use
	// unnamespaced paths because session state is independent of the provider.
	providerStateCommands := map[string]bool{
		"status":      true,
		"models":      true,
		"doctor":      true,
		"report":      true,
		"dashboard":   true,
		"refresh":     true,
		"cache":       true,
		"status-line": true, // renders provider state
	}
	needsProviderState := providerStateCommands[cmd]

	if !activation.Active {
		if needsProviderState {
			fmt.Fprintf(stderr, "error: FreeInference not active — cannot read/write provider state\n")
			return 1
		}
		// Session-only commands may use unnamespaced paths.
	} else {
		id, err := activation.Identity(runtime.DefaultSaltLoader())
		if err != nil {
			fmt.Fprintf(stderr, "error: failed to initialize activation identity: %v\n", err)
			return 1
		}
		dirName := id.DirName()
		if dirName == "" {
			fmt.Fprintf(stderr, "error: activation identity is empty\n")
			return 1
		}
		paths = paths.NewNamespacedPaths(dirName)
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
		return cmdDashboard(rest, stdout, stderr)
	case "context":
		return cmdContext(paths, rest, stdin, stdout, stderr)
	case "refresh":
		return cmdRefresh(paths, rest, stdout, stderr)
	case "cache":
		return cmdCache(paths, rest, stdout, stderr)
	case "status-line":
		return cmdStatusLine(rest, stdout, stderr)
	case "version", "--version", "-v":
		return cmdVersion(rest, stdout, stderr)
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", cmd)
		printUsage(stderr)
		return 1
	}
}

// cmdVersion implements `fi version`, `fi --version`, and `fi -v`. Supports
// `--json` for machine-readable output.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
			continue
		}
		if strings.HasPrefix(a, "--") {
			fmt.Fprintf(stderr, "usage error: unknown flag %q\n", a)
			return 2
		}
		fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", a)
		return 2
	}
	if jsonOut {
		fmt.Fprintf(stdout, `{"version":%q,"commit":%q,"schema_version":%d,"clients":[%q,%q]}`+"\n",
			Version, Commit, schema.StateVersion, schema.ClientClaudeCode, schema.ClientCodex)
		return 0
	}
	fmt.Fprintf(stdout, "fi %s (%s)\n", Version, Commit)
	fmt.Fprintf(stdout, "state schema v%d\n", schema.StateVersion)
	fmt.Fprintf(stdout, "clients: %s, %s\n", schema.ClientClaudeCode, schema.ClientCodex)
	return 0
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
  fi cache [--client <type>] [--session <id>]
  fi refresh [--force|--if-stale] [--detach] [--worker models|health]
  fi status-line install|uninstall
  fi version [--json]
  fi hook <client> <event>

Environment:
  FREEINFERENCE_API_KEY    FreeInference API key
  FREEINFERENCE_BASE_URL   API base URL (default: https://freeinference.org/v1)
  FI_HEALTH_URL            Health monitoring URL (optional)
  FI_CACHE_DIR             Cache directory (default: ~/.cache/freeinference-companion)
  FI_SESSION_ID            Explicit session override for status/context/report
  FI_PROVIDER              Set to "freeinference" to force provider detection
  FI_NO_BACKGROUND         Disable background refresh
  FI_DISABLED              Disable all companion features
  FI_ALLOW_INSECURE_LOCALHOST  Allow http:// loopback (development only)
`)
}
