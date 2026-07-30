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

	// Stateless commands: dispatch before any state initialization so they
	// succeed even when HOME or cache initialization fails, and never create
	// state directories as a side effect of simply checking the version.
	if cmd == "version" || cmd == "--version" || cmd == "-v" {
		return cmdVersion(rest, stdout, stderr)
	}
	if cmd == "help" || cmd == "--help" || cmd == "-h" {
		printUsage(stdout)
		return 0
	}
	if cmd == "dashboard" {
		return cmdDashboard(rest, stdout, stderr)
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
		if activation.Disabled {
			fmt.Fprintf(stderr, "WARNING: FreeInference companion is DISABLED (FI_DISABLED=1)\n")
			fmt.Fprintf(stderr, "         All hooks and automatic features are suppressed.\n")
			fmt.Fprintf(stderr, "         Remove FI_DISABLED or set it to \"0\" to re-enable.\n")
		}
		if needsProviderState {
			if !activation.Disabled {
				fmt.Fprintf(stderr, "error: FreeInference not active — cannot read/write provider state\n")
			}
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
		if printCmdHelp(stdout, stderr, "status", rest) {
			return 0
		}
		return cmdStatus(paths, rest, stdin, stdout, stderr)
	case "sessions":
		if printCmdHelp(stdout, stderr, "sessions", rest) {
			return 0
		}
		return cmdSessions(paths, rest, stdout, stderr)
	case "snapshot":
		if printCmdHelp(stdout, stderr, "snapshot", rest) {
			return 0
		}
		return cmdSnapshot(paths, rest, stdin, stdout, stderr)
	case "render":
		if printCmdHelp(stdout, stderr, "render", rest) {
			return 0
		}
		return cmdRender(paths, rest, stdin, stdout, stderr)
	case "models":
		if printCmdHelp(stdout, stderr, "models", rest) {
			return 0
		}
		return cmdModels(paths, rest, stdout, stderr)
	case "doctor":
		if printCmdHelp(stdout, stderr, "doctor", rest) {
			return 0
		}
		return cmdDoctor(paths, rest, stdout, stderr)
	case "report":
		if printCmdHelp(stdout, stderr, "report", rest) {
			return 0
		}
		return cmdReport(paths, rest, stdout, stderr)
	case "dashboard":
		if printCmdHelp(stdout, stderr, "dashboard", rest) {
			return 0
		}
		return cmdDashboard(rest, stdout, stderr)
	case "context":
		if printCmdHelp(stdout, stderr, "context", rest) {
			return 0
		}
		return cmdContext(paths, rest, stdin, stdout, stderr)
	case "refresh":
		if printCmdHelp(stdout, stderr, "refresh", rest) {
			return 0
		}
		return cmdRefresh(paths, rest, stdout, stderr)
	case "cache":
		if printCmdHelp(stdout, stderr, "cache", rest) {
			return 0
		}
		return cmdCache(paths, rest, stdout, stderr)
	case "status-line":
		if printCmdHelp(stdout, stderr, "status-line", rest) {
			return 0
		}
		return cmdStatusLine(rest, stdout, stderr)
	case "config":
		return cmdConfig(rest, stdout, stderr)
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
  fi refresh [--force|--if-stale] [--detach] [--worker models|health|account-usage]
  fi status-line install|uninstall
  fi config show|set|reset|path
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

// Command help text constants
const (
	helpStatus = `Usage: fi status [--client <type>] [--compact] [--session <id>] [--help]

Show the current session status with context usage, pressure, and cache analysis.

Flags:
  --client <type>    Client type: claude-code (default) or codex
  --compact          Output a single line suitable for status-line wrappers
  --session <id>     Explicit session ID (also via FI_SESSION_ID env var)
  --help             Show this help message
`

	helpSessions = `Usage: fi sessions [--include-identifiers] [--help]

List all recorded sessions across clients.

Flags:
  --include-identifiers  Show full session IDs (default: masked)
  --help                 Show this help message
`

	helpSnapshot = `Usage: fi snapshot --json [--client <type>] [--session <id>] [--include-identifiers] [--help]

Output the full session snapshot in JSON format for machine consumption.

Flags:
  --client <type>        Client type: claude-code (default) or codex
  --session <id>         Explicit session ID (also via FI_SESSION_ID env var)
  --include-identifiers  Show full session IDs (default: masked)
  --help                 Show this help message
`

	helpRender = `Usage: fi render --mode line|expanded [--client <type>] [--session <id>] [--include-identifiers] [--help]

Render session status as human-readable output.

Flags:
  --mode line|expanded   Render mode: line (default) or expanded
  --client <type>        Client type: claude-code (default) or codex
  --session <id>         Explicit session ID (also via FI_SESSION_ID env var)
  --include-identifiers  Show full session IDs (default: masked)
  --help                 Show this help message
`

	helpModels = `Usage: fi models [--model <name>] [--refresh] [--help]

List available models from the catalog, optionally showing a specific model.

Flags:
  --model <name>     Show detail for a specific model by ID or name
  --refresh          Force a refresh of the model catalog before displaying
  --help             Show this help message
`

	helpDoctor = `Usage: fi doctor [--probe --model <name>] [--help]

Run diagnostic checks on the companion installation.

Checks performed:
  - Cache directory exists and is writable
  - State files are readable
  - fi binary is resolvable
  - Claude hook configuration present
  - Status-line wrapper valid
  - Provider detection
  - Health source configured
  - Model catalog reachable
  - API key format and authentication
  - Model access
  - Circuit breaker status

Flags:
  --probe --model <name>  Run a synthetic inference probe against the given model
  --help                  Show this help message
`

	helpReport = `Usage: fi report [--client <type>] [--session <id>] [--format markdown|json] [--include-identifiers] [--help]

Generate a sanitized report suitable for sharing with support.

Flags:
  --client <type>         Client type: claude-code (default) or codex
  --session <id>          Explicit session ID (also via FI_SESSION_ID env var)
  --format markdown|json  Output format (default: markdown)
  --include-identifiers   Show full session IDs (default: masked)
  --help                  Show this help message
`

	helpDashboard = `Usage: fi dashboard [--status] [--account] [--print-url] [--help]

Open the FreeInference dashboard in your browser.

Flags:
  --status    Open the public service health page instead of the account dashboard
  --account   Open the account dashboard (default)
  --print-url Print the URL without opening a browser
  --help      Show this help message
`

	helpContext = `Usage: fi context [--client <type>] [--session <id>] [--help]

Show current context usage for the active session.

Flags:
  --client <type>  Client type: claude-code (default) or codex
  --session <id>   Explicit session ID (also via FI_SESSION_ID env var)
  --help           Show this help message
`

	helpRefresh = `Usage: fi refresh [--force|--if-stale] [--detach] [--worker models|health] [--help]

Refresh cached data (models, health, account usage).

Modes (mutually exclusive):
  --force             Force refresh regardless of staleness
  --if-stale          Refresh only if caches are stale (default)
  --detach            Spawn detached background workers for stale caches
  --worker <name>     Single worker: models or health

Flags:
  --help  Show this help message
`

	helpCache = `Usage: fi cache [--client <type>] [--session <id>] [--include-identifiers] [--help]

Analyze cache efficiency and provide recommendations to improve hit rates.

Flags:
  --client <type>         Client type: claude-code (default) or codex
  --session <id>          Explicit session ID (also via FI_SESSION_ID env var)
  --include-identifiers   Show full session IDs (default: masked)
  --help                  Show this help message
`

	helpStatusLine = `Usage: fi status-line install|uninstall [--scope user|project|local] [--project <dir>] [--help]

Install or uninstall the status-line wrapper for Claude Code.

Subcommands:
  install   Install the status-line wrapper (default scope: project)
  uninstall Remove the status-line wrapper

Flags:
  --scope <type>   Scope: user, project (default), or local
  --project <dir>  Project directory for project/local scope
  --help           Show this help message
`

	helpVersion = `Usage: fi version [--json] [--help]

Show the fi companion version and schema information.

Flags:
  --json    Output machine-readable JSON
  --help    Show this help message
`

	helpHook = `Usage: fi hook <client> <event>

Internal hook entry point for Claude Code and Codex. Never called directly.

Arguments:
  client    Client type: claude-code or codex
  event     Event name: SessionStart, SessionEnd, UserPromptSubmit,
            PreCompact, PostCompact, Stop, StopFailure
`
)

// printCmdHelp prints per-command help if --help is in the args.
// Returns true if help was printed (caller should return 0).
func printCmdHelp(stdout, stderr io.Writer, cmd string, args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			switch cmd {
			case "status":
				fmt.Fprint(stdout, helpStatus)
			case "sessions":
				fmt.Fprint(stdout, helpSessions)
			case "snapshot":
				fmt.Fprint(stdout, helpSnapshot)
			case "render":
				fmt.Fprint(stdout, helpRender)
			case "models":
				fmt.Fprint(stdout, helpModels)
			case "doctor":
				fmt.Fprint(stdout, helpDoctor)
			case "report":
				fmt.Fprint(stdout, helpReport)
			case "dashboard":
				fmt.Fprint(stdout, helpDashboard)
			case "context":
				fmt.Fprint(stdout, helpContext)
			case "refresh":
				fmt.Fprint(stdout, helpRefresh)
			case "cache":
				fmt.Fprint(stdout, helpCache)
			case "status-line":
				fmt.Fprint(stdout, helpStatusLine)
			case "version":
				fmt.Fprint(stdout, helpVersion)
			case "hook":
				fmt.Fprint(stdout, helpHook)
			default:
				printUsage(stdout)
			}
			return true
		}
	}
	return false
}
