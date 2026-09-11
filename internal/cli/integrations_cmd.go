package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
	"github.com/b-a-m-n/freeinference-companion/internal/installer"
)

// cmdIntegrations implements read-only client environment diagnostics. The
// generic fallback for arbitrary roots is intentionally `add` in the future;
// automatic discovery never parse launchers or recursively inspect projects.
func cmdIntegrations(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printIntegrationsHelp(stdout)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printIntegrationsHelp(stdout)
		return 0
	}
	switch args[0] {
	case "list", "discover":
		return cmdIntegrationsRead(args, stdout, stderr)
	case "add":
		return cmdIntegrationsAdd(args[1:], stdout, stderr)
	case "remove":
		return cmdIntegrationsRemove(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown integrations command: %s\n", args[0])
		printIntegrationsHelp(stderr)
		return 2
	}
}

func cmdIntegrationsRead(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	for _, arg := range args[1:] {
		if arg == "--json" {
			jsonOut = true
			continue
		}
		fmt.Fprintf(stderr, "unknown flag: %s\n", arg)
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: no home directory: %v\n", err)
		return 1
	}
	if args[0] == "list" {
		integrations, err := installer.ListClientIntegrations(home)
		if err != nil {
			fmt.Fprintf(stderr, "error: read client integrations: %v\n", err)
			return 1
		}
		if jsonOut {
			encodeJSON(stdout, integrations)
			return 0
		}
		if len(integrations) == 0 {
			fmt.Fprintln(stdout, "No additional FreeInference client environments are recorded.")
			return 0
		}
		for _, integration := range integrations {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", integration.Client, integration.ConfigRoot, integration.Version, integration.DiscoverySource)
		}
		return 0
	}

	environments, warnings := clientenv.Discover(home)
	for _, warning := range warnings {
		fmt.Fprintf(stderr, "warning: %v\n", warning)
	}
	if jsonOut {
		encodeJSON(stdout, environments)
		return 0
	}
	if len(environments) == 0 {
		fmt.Fprintln(stdout, "No client environments found.")
		return 0
	}
	for _, environment := range environments {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", environment.Client, environment.ConfigRoot, environment.Source)
	}
	return 0
}

func cmdIntegrationsAdd(args []string, stdout, stderr io.Writer) int {
	var (
		client clientenv.Client
		root   string
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "error: --client requires claude-code or codex")
				return 2
			}
			client = clientenv.Client(args[i])
		case "--root":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "error: --root requires a directory argument")
				return 2
			}
			root = args[i]
		default:
			fmt.Fprintf(stderr, "unknown flag: %s\n", args[i])
			return 2
		}
	}
	if client == "" || root == "" {
		fmt.Fprintln(stderr, "error: integrations add requires --client and --root")
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: no home directory: %v\n", err)
		return 1
	}
	integration, err := installer.AddClientIntegration(home, client, root, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: add client integration: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Integrated %s environment %s (%s).\n", integration.Client, integration.ConfigRoot, integration.Version)
	return 0
}

func cmdIntegrationsRemove(args []string, stdout, stderr io.Writer) int {
	var (
		client clientenv.Client
		root   string
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "error: --client requires claude-code or codex")
				return 2
			}
			client = clientenv.Client(args[i])
		case "--root":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "error: --root requires a directory argument")
				return 2
			}
			root = args[i]
		default:
			fmt.Fprintf(stderr, "unknown flag: %s\n", args[i])
			return 2
		}
	}
	if client == "" || root == "" {
		fmt.Fprintln(stderr, "error: integrations remove requires --client and --root")
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: no home directory: %v\n", err)
		return 1
	}
	removed, err := installer.RemoveClientIntegration(home, client, root, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: remove client integration: %v\n", err)
		if len(removed) == 0 {
			return 1
		}
	}
	for _, path := range removed {
		fmt.Fprintf(stdout, "Removed %s\n", path)
	}
	return 0
}

func encodeJSON(stdout io.Writer, value any) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func printIntegrationsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage: freeinference integrations list [--json]
       freeinference integrations discover [--json]
       freeinference integrations add|remove --client claude-code|codex --root <directory>

Inspect and register FreeInference client environments.

Commands:
  list      Show additional environments currently owned by the installer
  discover  Show canonical, exported, and bounded-discovery environments
  add       Register one explicit arbitrary client configuration root
  remove    Remove one recorded alternate client environment

All alternate roots must independently route to an approved FreeInference /v1
endpoint. Discovery and explicit registration never inspect launcher names or
recursively scan projects.
`)
}
