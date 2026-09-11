package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	case "diagnose":
		return cmdIntegrationsDiagnose(args[1:], stdout, stderr)
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
			if err := encodeJSON(stdout, integrations); err != nil {
				fmt.Fprintf(stderr, "error: encode client integrations: %v\n", err)
				return 1
			}
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
		// Keep this output stable and explicitly snake_case for automation.
		type discoveryJSON struct {
			Client     string `json:"client"`
			ConfigRoot string `json:"config_root"`
			Source     string `json:"source"`
		}
		output := make([]discoveryJSON, 0, len(environments))
		for _, environment := range environments {
			output = append(output, discoveryJSON{Client: string(environment.Client), ConfigRoot: environment.ConfigRoot, Source: string(environment.Source)})
		}
		if err := encodeJSON(stdout, output); err != nil {
			fmt.Fprintf(stderr, "error: encode discovery: %v\n", err)
			return 1
		}
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

func normalizeIntegrationRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("configuration root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func cmdIntegrationsAdd(args []string, stdout, stderr io.Writer) int {
	var (
		client        clientenv.Client
		root          string
		proxyUpstream string
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
		case "--proxy-upstream":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "error: --proxy-upstream requires a URL")
				return 2
			}
			proxyUpstream = args[i]
		default:
			fmt.Fprintf(stderr, "unknown flag: %s\n", args[i])
			return 2
		}
	}
	if client == "" || root == "" {
		fmt.Fprintln(stderr, "error: integrations add requires --client and --root")
		return 2
	}
	if client != clientenv.ClientClaudeCode && client != clientenv.ClientCodex {
		fmt.Fprintf(stderr, "error: unsupported integration client %q\n", client)
		return 2
	}
	root, rootErr := normalizeIntegrationRoot(root)
	if rootErr != nil {
		fmt.Fprintf(stderr, "error: invalid root: %v\n", rootErr)
		return 2
	}
	homeHint, homeErr := os.UserHomeDir()
	if homeErr != nil {
		fmt.Fprintf(stderr, "error: no home directory: %v\n", homeErr)
		return 1
	}
	if client == clientenv.ClientCodex && proxyUpstream != "" {
		if err := clientenv.SetCodexProxyAttestation(homeHint, root, proxyUpstream); err != nil {
			fmt.Fprintf(stderr, "error: proxy attestation: %v\n", err)
			return 2
		}
	}
	integration, addErr := installer.AddClientIntegration(homeHint, client, root, stdout)
	if addErr != nil {
		if client == clientenv.ClientCodex && proxyUpstream != "" {
			if removeErr := os.Remove(clientenv.CodexProxyAttestationPath(homeHint, root)); removeErr != nil && !os.IsNotExist(removeErr) {
				fmt.Fprintf(stderr, "warning: could not remove proxy attestation after failed registration: %v\n", removeErr)
			}
		}
		fmt.Fprintf(stderr, "error: add client integration: %v\n", addErr)
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
	root, rootErr := normalizeIntegrationRoot(root)
	if rootErr != nil {
		fmt.Fprintf(stderr, "error: invalid root: %v\n", rootErr)
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

func cmdIntegrationsDiagnose(args []string, stdout, stderr io.Writer) int {
	var (
		client  clientenv.Client
		root    string
		model   string
		jsonOut bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
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
		case "--model":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				fmt.Fprintln(stderr, "error: --model requires a model ID")
				return 2
			}
			model = args[i]
		default:
			fmt.Fprintf(stderr, "unknown flag: %s\n", args[i])
			return 2
		}
	}
	if client == "" || root == "" {
		fmt.Fprintln(stderr, "error: integrations diagnose requires --client and --root")
		return 2
	}
	root, rootErr := normalizeIntegrationRoot(root)
	if rootErr != nil {
		fmt.Fprintf(stderr, "error: invalid root: %v\n", rootErr)
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: no home directory: %v\n", err)
		return 1
	}
	result := map[string]any{"client": string(client), "config_root": root}
	switch client {
	case clientenv.ClientClaudeCode:
		state, routeErr := clientenv.DiagnoseClaudeRoute(root)
		if state != "" {
			result["route_state"] = state
		}
		if routeErr != nil {
			result["route_error"] = routeErr.Error()
		}
		result["overall"] = map[string]bool{"ready": routeErr == nil && state == "verified"}
	case clientenv.ClientCodex:
		state, baseURL, routeErr := clientenv.VerifyCodexConfigRoute(home, root)
		result["route_state"] = string(state)
		result["provider_base_url"] = baseURL
		if routeErr != nil {
			result["route_error"] = routeErr.Error()
		}
		caps, capsErr := clientenv.InspectCodexModelCatalogForModel(root, model)
		result["model_capabilities"] = caps
		if capsErr != nil {
			result["model_capabilities_error"] = capsErr.Error()
		}
		installation, installationErr := installer.InspectCodexInstallation(home, root)
		result["installation"] = installation
		if installationErr != nil {
			result["installation_error"] = installationErr.Error()
		}
		result["plugin_ready"] = installationErr == nil && installation.PayloadInstalled && installation.MarketplaceInstalled && installation.NativeStatusKnown && installation.MarketplaceRegistered && installation.PluginRegistered
		result["overall"] = map[string]bool{
			"ready": (state == clientenv.CodexRouteVerifiedDirect || state == clientenv.CodexRouteVerifiedProxy) && capsErr == nil && caps.SkillsAllowed && caps.PluginsAllowed,
		}
	default:
		fmt.Fprintf(stderr, "error: unsupported diagnostics client %q\n", client)
		return 2
	}
	if jsonOut {
		if err := encodeJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "error: encode diagnostics: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "Client ................. %s\n", client)
	fmt.Fprintf(stdout, "Config root ............ %s\n", root)
	for _, key := range []string{"route_state", "provider_base_url", "route_error", "installation_error", "plugin_ready", "overall"} {
		if value, ok := result[key]; ok {
			fmt.Fprintf(stdout, "%-22s %v\n", key, value)
		}
	}
	if caps, ok := result["model_capabilities"].(clientenv.CodexModelCapabilities); ok {
		fmt.Fprintf(stdout, "Skills allowed ......... %t\n", caps.SkillsAllowed)
		fmt.Fprintf(stdout, "Plugins allowed ........ %t\n", caps.PluginsAllowed)
	}
	if installation, ok := result["installation"].(installer.CodexInstallationSummary); ok {
		fmt.Fprintf(stdout, "Payload installed ...... %t\n", installation.PayloadInstalled)
		fmt.Fprintf(stdout, "Marketplace installed .. %t\n", installation.MarketplaceInstalled)
		fmt.Fprintf(stdout, "Marketplace registered . %t\n", installation.MarketplaceRegistered)
		fmt.Fprintf(stdout, "Plugin registered ...... %t\n", installation.PluginRegistered)
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
       freeinference integrations add --client claude-code|codex --root <directory> [--proxy-upstream <url>]
       freeinference integrations remove --client claude-code|codex --root <directory>
       freeinference integrations diagnose --client claude-code|codex --root <directory> [--model <id>] [--json]

Inspect and register FreeInference client environments.

Commands:
  list      Show additional environments currently owned by the installer
  discover  Show canonical, exported, and bounded-discovery environments
  diagnose  Inspect one explicit root read-only
  add       Register one explicit arbitrary client configuration root
  remove    Remove one recorded alternate client environment

All alternate roots must independently route to the approved FreeInference
endpoint for their client. Discovery and explicit registration never inspect launcher names or
recursively scan projects.
`)
}
