package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/engine"
	"github.com/b-a-m-n/freeinference-companion/internal/render"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// newAPIClient builds an API client from the environment.
// Validates the base URL: requires HTTPS (except loopback with opt-in),
// rejects userinfo/fragments. Returns an error if the URL is invalid or
// the endpoint is not an approved FreeInference host while an API key is set.
func newAPIClient() (*api.Client, error) {
	// Check for custom endpoint configuration first
	customCfg, err := api.LoadCustomEndpointConfig()
	if err != nil {
		return nil, err
	}

	var baseURL, apiKey string
	if customCfg != nil {
		// Use custom endpoint configuration
		baseURL = customCfg.EndpointIdentity.RequestURL
		apiKey = customCfg.APIKey
	} else {
		activation := activationForCLICommand("", nil)
		if activation.Active {
			baseURL = activation.ManagementBaseURL()
			switch activation.CredentialSource {
			case runtime.CredFreeInferenceAPIKey:
				apiKey = os.Getenv("FREEINFERENCE_API_KEY")
			case runtime.CredAnthropicAuthToken:
				apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
			case runtime.CredAnthropicAPIKey:
				apiKey = os.Getenv("ANTHROPIC_API_KEY")
			case runtime.CredOpenAIAPIKey:
				apiKey = os.Getenv("OPENAI_API_KEY")
			}
		}
		// Use standard FreeInference environment variables
		if baseURL == "" {
			baseURL = os.Getenv("FREEINFERENCE_BASE_URL")
		}
		if apiKey == "" {
			apiKey = os.Getenv("FREEINFERENCE_API_KEY")
		}
		// Codex stores its selected provider in ~/.codex/config.toml rather
		// than exporting the runtime endpoint. Use that resolver only when the
		// ordinary provider-level environment did not produce a client.
		if baseURL == "" || apiKey == "" {
			if evidence, resolveErr := runtime.ResolveCodexProviderConfiguration(); resolveErr == nil && evidence.CredentialValue != "" {
				if endpoint, normalizeErr := api.NormalizeEndpoint(evidence.EndpointURL); normalizeErr == nil && endpoint.IsFI {
					baseURL = endpoint.Origin + "/v1"
					apiKey = evidence.CredentialValue
				}
			}
		}
		if baseURL == "" {
			baseURL = api.DefaultBaseURL
		}
	}
	client, err := api.NewClient(api.ClientConfig{BaseURL: baseURL, APIKey: apiKey, Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	return client, nil
}

// displaySessionID returns either the masked or the raw session ID depending
// on whether --include-identifiers was passed. All human-facing output paths
// should call this rather than emitting the raw ID directly.
func displaySessionID(id string, reveal bool) string {
	if reveal {
		return id
	}
	return secure.MaskSessionID(id)
}

// parseClientSessionFlags extracts --client, --session, --format, and
// --include-identifiers flags. Rejects unknown flags (arguments starting
// with "--" that aren't recognized) and missing flag values.
// Returns an error describing the first invalid input.
func parseClientSessionFlags(args []string) (clientType, sessionID, format string, reveal, jsonOut bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--include-identifiers" {
			reveal = true
			continue
		}
		if a == "--json" {
			jsonOut = true
			continue
		}
		switch a {
		case "--client":
			if i+1 >= len(args) {
				return "", "", "", false, false, fmt.Errorf("--client requires a value")
			}
			i++
			clientType = args[i]
			if clientType != schema.ClientClaudeCode && clientType != schema.ClientCodex {
				return "", "", "", false, false, fmt.Errorf("unknown client %q (supported: %s, %s)",
					clientType, schema.ClientClaudeCode, schema.ClientCodex)
			}
		case "--session":
			if i+1 >= len(args) {
				return "", "", "", false, false, fmt.Errorf("--session requires a value")
			}
			i++
			sessionID = args[i]
		case "--format":
			if i+1 >= len(args) {
				return "", "", "", false, false, fmt.Errorf("--format requires a value")
			}
			i++
			format = args[i]
		default:
			if strings.HasPrefix(a, "--") {
				return "", "", "", false, false, fmt.Errorf("unknown flag %q", a)
			}
			return "", "", "", false, false, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return clientType, sessionID, format, reveal, jsonOut, nil
}

// explicitSessionRequested distinguishes an intentional historical lookup
// from an automatic/current-session command. FI_SESSION_ID is equivalent to
// --session for the diagnostic commands.
func explicitSessionRequested(args []string) bool {
	_, sessionID, _, _, _, err := parseClientSessionFlags(args)
	if err != nil {
		return false
	}
	return sessionID != "" || strings.TrimSpace(os.Getenv("FI_SESSION_ID")) != ""
}

// resolvedSession pairs a session identity with its loaded snapshot.
type resolvedSession struct {
	Client    string
	SessionID string
	Snap      *schema.Snapshot
}

// resolveSession implements the session resolution order:
//  1. explicit --session (or FI_SESSION_ID)
//  2. most recently updated active session for the requested client
//  3. most recently updated session overall
//  4. ambiguity → list available sessions instead of guessing
func resolveSession(paths state.Paths, clientType, sessionID string, stdout io.Writer) (*resolvedSession, error) {
	if sessionID == "" {
		sessionID = os.Getenv("FI_SESSION_ID")
	}

	// Explicit session ID: load the snapshot directly, honoring the client
	// filter when given.
	if sessionID != "" {
		clients := []string{schema.ClientClaudeCode, schema.ClientCodex}
		if clientType != "" {
			clients = []string{clientType}
		}
		for _, client := range clients {
			snap, err := state.LoadSnapshot(paths, client, sessionID)
			if err == nil && snap != nil {
				return &resolvedSession{Client: client, SessionID: sessionID, Snap: snap}, nil
			}
		}
		return nil, nil
	}

	entry, err := state.ResolveSession(paths, clientType, "")
	if err != nil {
		if errors.Is(err, state.ErrAmbiguousSession) {
			fmt.Fprintln(stdout, "FI: several active sessions — specify --client or --session:")
			printSessionList(paths, stdout)
			return nil, nil
		}
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	snap, err := state.LoadSnapshot(paths, entry.Client, entry.SessionID)
	if err != nil || snap == nil {
		return nil, nil
	}
	return &resolvedSession{Client: entry.Client, SessionID: entry.SessionID, Snap: snap}, nil
}

func printSessionList(paths state.Paths, stdout io.Writer) {
	printSessionListWith(paths, stdout, false)
}

func printSessionListWith(paths state.Paths, stdout io.Writer, reveal bool) {
	idx, err := state.LoadSessionIndex(paths)
	if err != nil || len(idx.Sessions) == 0 {
		fmt.Fprintln(stdout, "  (no sessions recorded)")
		return
	}
	limit := min(len(idx.Sessions), 10)
	for _, e := range idx.Sessions[:limit] {
		fmt.Fprintf(stdout, "  %-12s %-40s %-10s %s\n", e.Client, displaySessionID(e.SessionID, reveal), e.Status,
			e.LastEventAt.Format(time.RFC3339))
	}
}

// loadGlobal is a fail-open global state load. Each resource loads
// independently, so a corrupt models file will not discard valid
// circuit-breaker state. The returned state is always non-nil.
func loadGlobal(paths state.Paths) *schema.GlobalState {
	gs, _ := state.LoadGlobal(paths)
	if gs == nil {
		return &schema.GlobalState{}
	}
	return gs
}

// buildView assembles the normalized view model for a snapshot.
// currentActivationID is the activation identity from runtime.Evaluate() —
// when non-empty, health and circuit-breaker data are only included when
// the snapshot was recorded under the same identity.
// runtimeActive and clientType feed the SurfaceEligibility gate set.
func buildView(snap *schema.Snapshot, gs *schema.GlobalState, currentActivationID string, runtimeActive bool, clientType string, sessionID string) *render.ViewModel {
	return render.BuildViewModel(Version, snap, gs, currentActivationID, time.Now(), runtimeActive, clientType, sessionID)
}

// renderConfig returns a RenderConfig with color mode resolved from the
// explicit CLI flag or auto-detected from the environment.
//
// When the user passes --color auto (or nothing), DetectColorMode() is called
// which checks NO_COLOR first, then FORCE_COLOR, then whether stdout is a
// terminal.  --color always/never override detection.
func renderConfigWith(args []string) render.RenderConfig {
	cfg := render.DefaultRenderConfig()
	// parseColorFlag handles both `--color=value` and `--color value`, and
	// resolves NO_COLOR/FORCE_COLOR only when the user did not choose a mode.
	// Keeping that logic in one place prevents spaced color flags from being
	// silently ignored by status and render commands.
	mode, _, err := parseColorFlag(args)
	if err != nil {
		// Command handlers validate flags before rendering. Keep this helper
		// fail-safe for direct/internal callers by falling back to auto mode.
		mode = render.ApplyEnv(render.ColorAuto)
	}
	cfg.ColorMode = mode
	return cfg
}

// renderConfig returns a RenderConfig with auto-detected color mode.
func renderConfig() render.RenderConfig {
	return renderConfigWith(nil)
}

// parseColorFlag extracts a --color flag from args, returning the resolved
// ColorMode and the remaining args (with --color and its value removed).
// It rejects missing or unknown values rather than silently changing a
// user's requested output contract.
func parseColorFlag(args []string) (render.ColorMode, []string, error) {
	var remaining []string
	mode := render.ColorAuto
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--color" {
			if i+1 >= len(args) {
				return render.ColorAuto, nil, fmt.Errorf("--color requires a value (auto, always, or never)")
			}
			value := strings.ToLower(strings.TrimSpace(args[i+1]))
			if value != "auto" && value != "always" && value != "never" {
				return render.ColorAuto, nil, fmt.Errorf("unknown color mode %q (want auto, always, or never)", args[i+1])
			}
			mode = render.ParseColorFlag(value)
			i++ // skip value
			continue
		}
		if strings.HasPrefix(a, "--color=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(a, "--color=")))
			if value != "auto" && value != "always" && value != "never" {
				return render.ColorAuto, nil, fmt.Errorf("unknown color mode %q (want auto, always, or never)", strings.TrimPrefix(a, "--color="))
			}
			mode = render.ParseColorFlag(value)
			continue
		}
		remaining = append(remaining, a)
	}
	mode = render.ApplyEnv(mode)
	return mode, remaining, nil
}

// formatTokenCount renders a token count compactly.
func formatTokenCount(n int64) string {
	return render.FormatTokenCount(n)
}

// formatTokenPtr renders a nullable token count.
func formatTokenPtr(p *int64) string {
	if p == nil {
		return "unknown"
	}
	return render.FormatTokenCount(*p)
}

// formatPctPtr renders a nullable fraction as a percentage.
func formatPctPtr(p *float64) string {
	if p == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.0f%%", *p*100)
}

// formatQuotaPair renders a used/limit pair for reports. Either may be nil.
func formatQuotaPair(used, limit *int64) string {
	usedStr := "unknown"
	if used != nil {
		usedStr = formatTokenCount(*used)
	}
	limitStr := "unknown"
	if limit != nil {
		limitStr = formatTokenCount(*limit)
	}
	if used != nil && limit != nil && *limit > 0 {
		pct := float64(*used) / float64(*limit) * 100
		return fmt.Sprintf("%s / %s (%.1f%%)", usedStr, limitStr, pct)
	}
	return fmt.Sprintf("%s / %s", usedStr, limitStr)
}

// budgetIcon returns the icon for a budget status.
func budgetIcon(status engine.BudgetStatus) string {
	switch status {
	case engine.BudgetCritical:
		return "🔴"
	case engine.BudgetLow:
		return "🟠"
	case engine.BudgetWatch:
		return "🟡"
	case engine.BudgetHealthy:
		return "🟢"
	default:
		return "⚪"
	}
}

// repeat is strings.Repeat exposed for command files.
func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}
