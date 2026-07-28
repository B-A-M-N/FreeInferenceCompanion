package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/render"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// newAPIClient builds an API client from the environment.
func newAPIClient() *api.Client {
	baseURL := os.Getenv("FREEINFERENCE_BASE_URL")
	apiKey := os.Getenv("FREEINFERENCE_API_KEY")
	if baseURL == "" {
		baseURL = api.DefaultBaseURL
	}
	client := api.NewClient(baseURL, apiKey, 30*time.Second)
	client.Version = Version
	return client
}

// includeIdentifiers reports whether the caller passed --include-identifiers.
// When false (the default), all display paths mask session IDs and other
// identifying fields. When true, full identifiers are shown — intended for
// local debugging by a user who understands the value is on their own disk.
func includeIdentifiers(args []string) bool {
	for _, a := range args {
		if a == "--include-identifiers" {
			return true
		}
	}
	return false
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

// parseClientSessionFlags extracts --client, --session, and --format flags.
func parseClientSessionFlags(args []string) (clientType, sessionID, format string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client":
			if i+1 < len(args) {
				i++
				clientType = args[i]
			}
		case "--session":
			if i+1 < len(args) {
				i++
				sessionID = args[i]
			}
		case "--format":
			if i+1 < len(args) {
				i++
				format = args[i]
			}
		}
	}
	return clientType, sessionID, format
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

// loadGlobal is a fail-open global state load.
func loadGlobal(paths state.Paths) *schema.GlobalState {
	gs, err := state.LoadGlobal(paths)
	if err != nil || gs == nil {
		return &schema.GlobalState{}
	}
	return gs
}

// buildView assembles the normalized view model for a snapshot.
func buildView(snap *schema.Snapshot, gs *schema.GlobalState) *render.ViewModel {
	return render.BuildViewModel(Version, snap, gs, time.Now())
}

// renderConfig returns a RenderConfig with auto-detected color mode.
func renderConfig() render.RenderConfig {
	cfg := render.DefaultRenderConfig()
	cfg.ColorMode = render.DetectColorMode()
	return cfg
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

// accessSymbol renders a catalog access state.
func accessSymbol(state string) string {
	switch state {
	case schema.AccessAvailable:
		return "✓"
	case schema.AccessRestricted:
		return "⊘"
	default:
		return "?"
	}
}

// repeat is strings.Repeat exposed for command files.
func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}
