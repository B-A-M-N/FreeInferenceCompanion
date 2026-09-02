package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

type traceStatus struct {
	Enabled             bool   `json:"enabled"`
	Active              bool   `json:"active"`
	Verified            bool   `json:"verified"`
	Client              string `json:"client,omitempty"`
	TraceID             string `json:"trace_id,omitempty"`
	Header              string `json:"header"`
	Provider            string `json:"provider,omitempty"`
	Source              string `json:"source"`
	StartedAt           string `json:"started_at,omitempty"`
	EndpointOrigin      string `json:"endpoint_origin,omitempty"`
	CodexMapping        string `json:"codex_mapping,omitempty"`
	CodexSetupAvailable bool   `json:"codex_setup_available,omitempty"`
	Note                string `json:"note"`
}

func cmdTrace(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	operation := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		operation = args[0]
		args = args[1:]
	}
	if operation == "setup" || operation == "uninstall" {
		return cmdTraceCodexLifecycle(operation, args, stdout, stderr)
	}
	if operation != "status" {
		fmt.Fprintf(stderr, "usage error: unknown trace operation %q\n", operation)
		return 2
	}
	jsonOut := false
	clientType := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--client":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --client requires a value")
				return 2
			}
			i++
			clientType = args[i]
			if clientType != schema.ClientClaudeCode && clientType != schema.ClientCodex {
				fmt.Fprintf(stderr, "usage error: unknown client %q\n", clientType)
				return 2
			}
		case "--session":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --session requires a value")
				return 2
			}
			i++
		case "--help", "-h", "help":
			printTraceUsage(stdout)
			return 0
		default:
			if (operation == "setup" || operation == "uninstall") && clientType == "" && args[i] == "codex" {
				clientType = schema.ClientCodex
				continue
			}
			fmt.Fprintf(stderr, "usage error: unknown flag or argument %q\n", args[i])
			return 2
		}
	}

	mgr, err := config.NewManager()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	eff, err := mgr.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	status := traceStatus{
		Enabled: eff.Tracing.Enabled.Valid && eff.Tracing.Enabled.Value,
		Header:  tracing.SessionHeader,
		Source:  string(tracing.SourceNone),
		Note:    "Trace correlation is per Companion launch; request content and credentials are not recorded.",
	}
	if clientType == "" {
		if inherited, ok := tracing.EnvironmentTrace(); ok {
			clientType = inherited.Client
		}
	}
	activation := traceActivation(clientType, args)
	resolveOutput := stdout
	if jsonOut {
		resolveOutput = io.Discard
	}
	resolved, resolveErr := resolveSession(paths, clientType, sessionIDFromArgs(args), resolveOutput)
	if resolveErr != nil {
		fmt.Fprintf(stderr, "error: %v\n", resolveErr)
		return 1
	}
	if resolved != nil {
		if clientType == "" {
			activation = traceActivation(resolved.Client, args)
			clientType = resolved.Client
		}
		status.Client = resolved.Client
		if trace := currentTraceInfo(resolved.Snap, resolved.Client, activation); trace != nil && status.Enabled {
			applyTraceStatus(&status, trace)
		}
	} else if status.Enabled {
		if trace := environmentTraceInfo(clientType, activation); trace != nil {
			applyTraceStatus(&status, trace)
		}
	}
	if clientType == schema.ClientCodex {
		applyCodexMappingStatus(&status)
	}
	if !status.Active {
		status.Source = string(tracing.SourceNone)
		status.TraceID = ""
		status.StartedAt = ""
		status.Provider = ""
		status.EndpointOrigin = ""
		if status.Client == "" {
			status.Client = clientType
		}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(status)
		return 0
	}
	fmt.Fprintf(stdout, "Tracing:       %s\n", boolStatus(status.Enabled))
	if status.CodexMapping != "" {
		fmt.Fprintf(stdout, "Codex mapping: %s\n", status.CodexMapping)
	}
	if !status.Active {
		fmt.Fprintln(stdout, "Correlation:   unavailable (no active Companion launch trace)")
		return 0
	}
	fmt.Fprintf(stdout, "Client:        %s\n", status.Client)
	fmt.Fprintf(stdout, "Trace ID:      %s\n", status.TraceID)
	fmt.Fprintf(stdout, "Provider:      %s\n", status.Provider)
	fmt.Fprintf(stdout, "Header:        %s\n", status.Header)
	fmt.Fprintf(stdout, "Source:        %s\n", status.Source)
	if status.StartedAt != "" {
		fmt.Fprintf(stdout, "Started:       %s\n", status.StartedAt)
	}
	if !status.Verified {
		fmt.Fprintln(stdout, "Provenance:    inherited/unverified (not persisted)")
	}
	fmt.Fprintln(stdout, "Support:       provide this trace ID when contacting FreeInference support")
	return 0
}

func cmdTraceCodexLifecycle(operation string, args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	clientType := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--client":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --client requires a value")
				return 2
			}
			i++
			clientType = args[i]
		case "--help", "-h", "help":
			printTraceUsage(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "usage error: unknown flag or argument %q\n", args[i])
			return 2
		}
	}
	if clientType != schema.ClientCodex {
		fmt.Fprintln(stderr, "usage error: trace setup/uninstall requires --client codex")
		return 2
	}
	path, err := runtime.CodexConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	evidence, err := runtime.ResolveCodexProviderConfiguration()
	if err != nil || !evidence.ProviderSelectionVerified {
		if err == nil {
			err = fmt.Errorf("selected Codex provider is not fully configured")
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	endpoint, err := normalizeCodexTraceEndpoint(evidence.EndpointURL)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if !endpoint.IsFI {
		fmt.Fprintln(stderr, "error: selected Codex provider is not a FreeInference endpoint")
		return 1
	}

	if operation == "setup" {
		mapping, err := runtime.SetupCodexTraceConfig(path, evidence.ProviderID)
		if err != nil {
			fmt.Fprintf(stderr, "error: Codex trace setup: %v\n", err)
			return 1
		}
		if jsonOut {
			fmt.Fprintf(stdout, "{\"operation\":\"setup\",\"client\":%q,\"ready\":%t,\"modified\":%t}\n", clientType, mapping.Ready, mapping.Modified)
		} else if mapping.Modified {
			fmt.Fprintln(stdout, "Codex trace mappings installed. Ownership recorded for uninstall.")
		} else {
			fmt.Fprintln(stdout, "Codex trace mappings already installed.")
		}
		return 0
	}

	if err := runtime.RestoreCodexTraceConfig(path, evidence.ProviderID); err != nil {
		fmt.Fprintf(stderr, "error: Codex trace uninstall: %v\n", err)
		return 1
	}
	if jsonOut {
		fmt.Fprintln(stdout, `{"operation":"uninstall","client":"codex","ready":true}`)
	} else {
		fmt.Fprintln(stdout, "Codex trace mapping uninstalled and original config restored.")
	}
	return 0
}

func normalizeCodexTraceEndpoint(raw string) (*api.EndpointIdentity, error) {
	return api.NormalizeEndpoint(raw)
}

func traceActivation(client string, args []string) runtime.Activation {
	switch client {
	case schema.ClientClaudeCode:
		return runtime.EvaluateForClient(runtime.ClientClaudeCode)
	case schema.ClientCodex:
		return runtime.EvaluateForClient(runtime.ClientCodex)
	default:
		return activationForCLICommand("trace", args)
	}
}

func applyTraceStatus(status *traceStatus, trace *schema.TraceInfo) {
	if trace == nil || !trace.Enabled || trace.Provider != schema.ProviderFreeInference || trace.Header != tracing.SessionHeader ||
		trace.Source == schema.TraceSourceNone || !tracing.ValidateTraceID(trace.SessionID) {
		return
	}
	status.Active = true
	status.Verified = trace.Verified
	status.Client = trace.Client
	status.TraceID = trace.SessionID
	status.Provider = trace.Provider
	status.Source = trace.Source
	status.Header = trace.Header
	status.EndpointOrigin = trace.EndpointOrigin
	if !trace.Verified {
		status.Note = "Inherited trace is unverified and will not be persisted; use a Companion launch receipt for durable provenance."
	}
	if !trace.StartedAt.IsZero() {
		status.StartedAt = trace.StartedAt.UTC().Format(time.RFC3339)
	}
}

func applyCodexMappingStatus(status *traceStatus) {
	if status == nil {
		return
	}
	path, err := runtime.CodexConfigPath()
	if err != nil {
		status.CodexMapping = "unavailable"
		status.Note = "Codex trace mapping status is unavailable"
		return
	}
	evidence, err := runtime.ResolveCodexProviderConfiguration()
	if err != nil || !evidence.ProviderSelectionVerified {
		status.CodexMapping = "unavailable (provider unverified)"
		status.Note = "Codex trace mapping requires a verified selected provider"
		return
	}
	endpoint, err := api.NormalizeEndpoint(evidence.EndpointURL)
	if err != nil || !endpoint.IsFI {
		status.CodexMapping = "not applicable (provider is not FreeInference)"
		return
	}
	mapping, err := runtime.InspectCodexTraceHeaders(path, evidence.ProviderID)
	if err != nil {
		status.CodexMapping = "unavailable"
		status.Note = "Codex trace mapping could not be inspected"
		return
	}
	if len(mapping.Conflicts) > 0 {
		status.CodexMapping = "conflict"
		status.Note = "Codex Companion header mapping is user-owned; Companion will not replace it"
		return
	}
	if mapping.Ready {
		status.CodexMapping = "configured"
		return
	}
	status.CodexMapping = "missing"
	status.CodexSetupAvailable = codexConfigInstallable(path)
	if status.CodexSetupAvailable {
		status.Note = "Run `freeinference trace setup --client codex` to install the reversible mappings"
	} else {
		status.Note = "Codex Companion mappings are incomplete and its config is not writable"
	}
}

func boolStatus(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func sessionIDFromArgs(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--session" {
			return args[i+1]
		}
	}
	return strings.TrimSpace(os.Getenv("FI_SESSION_ID"))
}

func printTraceUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: freeinference trace [status|setup|uninstall] [codex] [--json] [--client claude-code|codex] [--session <id>]")
	fmt.Fprintln(w, "Show the current per-launch trace correlation metadata and Codex mapping state.")
	fmt.Fprintln(w, "Codex lifecycle: `trace setup codex` (or `--client codex`) installs a reversible mapping; `trace uninstall codex` removes the Companion-owned mapping.")
}
