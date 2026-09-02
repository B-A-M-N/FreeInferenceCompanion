package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// cmdSessions implements `freeinference sessions`.
func cmdSessions(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	reveal := false
	jsonOut := false
	for _, a := range args {
		if a == "--include-identifiers" {
			reveal = true
		} else if a == "--json" {
			jsonOut = true
		} else if strings.HasPrefix(a, "--") {
			fmt.Fprintf(stderr, "usage error: unknown flag %q\n", a)
			return 2
		} else {
			fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", a)
			return 2
		}
	}
	idx, err := state.LoadSessionIndex(paths)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if len(idx.Sessions) == 0 {
		if jsonOut {
			fmt.Fprintln(stdout, "[]")
			return 0
		}
		fmt.Fprintln(stdout, "No sessions recorded.")
		return 0
	}

	if jsonOut {
		type sessionEntry struct {
			Client      string `json:"client"`
			SessionID   string `json:"session_id"`
			ModelID     string `json:"model_id"`
			Status      string `json:"status"`
			LastEventAt string `json:"last_event_at"`
		}
		entries := make([]sessionEntry, 0, len(idx.Sessions))
		for _, e := range idx.Sessions {
			entries = append(entries, sessionEntry{
				Client:      e.Client,
				SessionID:   displaySessionID(e.SessionID, reveal),
				ModelID:     secure.SanitizeField(e.ModelID),
				Status:      e.Status,
				LastEventAt: e.LastEventAt.Format(time.RFC3339),
			})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(entries)
		return 0
	}

	fmt.Fprintf(stdout, "%-12s %-40s %-20s %-10s %s\n", "CLIENT", "SESSION", "MODEL", "STATUS", "LAST EVENT")
	for _, e := range idx.Sessions {
		sessionID := displaySessionID(e.SessionID, reveal)
		if len(sessionID) > 40 {
			sessionID = sessionID[:37] + "..."
		}
		model := secure.SanitizeField(e.ModelID)
		if len(model) > 20 {
			model = model[:17] + "..."
		}
		fmt.Fprintf(stdout, "%-12s %-40s %-20s %-10s %s\n",
			e.Client, sessionID, model, e.Status, e.LastEventAt.Format(time.RFC3339))
	}
	return 0
}

// cmdSnapshot implements `freeinference snapshot --json` — the machine-readable
// normalized view model consumed by panels and scripts.
func cmdSnapshot(paths state.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	jsonOut := false
	var flagArgs []string
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		} else {
			flagArgs = append(flagArgs, a)
		}
	}
	clientType, sessionID, _, reveal, _, err := parseClientSessionFlags(flagArgs)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	historical := explicitSessionRequested(flagArgs)

	// Derive activation ID to check for identity errors.
	// Identity failures are reported before any state access.
	activation := activationForCLICommand("snapshot", flagArgs)
	if stdinHasData(stdin) {
		activation = runtime.EvaluateForClient(runtime.ClientClaudeCode)
	}
	aid, aidErr := activationID(activation)

	// Status-line mode (stdin input): zero output on identity failure.
	if aidErr != nil && stdinHasData(stdin) {
		return 0
	}

	// Accept a Claude status payload on stdin (updates state first).
	// This path is taken when no --session is specified and stdin has a status payload.
	if sessionID == "" && stdinHasData(stdin) {
		var statusInput schema.ClaudeStatusLineInput
		if json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&statusInput) == nil && statusInput.SessionID != "" {
			// Status-line mode with valid payload: update state and render.
			// Identity failure in status-line mode → zero output (fail-closed).
			if aidErr != nil {
				return 0
			}
			// Default to Claude Code client type for status-line input.
			if clientType == "" {
				clientType = schema.ClientClaudeCode
			}
			activation := runtime.EvaluateForClient(runtime.ClientClaudeCode)
			if !activation.Active {
				return 0
			}
			aid, aidErr = activationID(activation)
			if aidErr != nil {
				return 0
			}
			_ = adapters.NewClaudeAdapter(paths).HandleStatusLineUpdateWith(&statusInput, statusInput.SessionID, activation)
			snap, _ := state.LoadSnapshot(paths, schema.ClientClaudeCode, statusInput.SessionID)
			if snap != nil {
				return printSnapshot(stdout, stderr, snap, loadGlobal(paths), jsonOut, aid, aidErr, reveal, false)
			}
		}
	}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resolved == nil {
		if jsonOut {
			vm := buildView(nil, loadGlobal(paths), "", false, "", "")
			data, _ := vm.JSON()
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		fmt.Fprintln(stdout, "FI: no session")
		return 0
	}
	return printSnapshot(stdout, stderr, resolved.Snap, loadGlobal(paths), jsonOut, aid, aidErr, reveal, historical)
}

func printSnapshot(stdout, stderr io.Writer, snap *schema.Snapshot, gs *schema.GlobalState, jsonOut bool, aid string, aidErr error, reveal bool, historical bool) int {
	// Handle identity error: report sanitized error for interactive mode.
	// For status-line mode (jsonOut with no snapshot), fail-closed with zero output.
	if aidErr != nil {
		if historical && strings.Contains(aidErr.Error(), "runtime not active") {
			if jsonOut {
				writeHistoricalSnapshotJSON(stdout, snap, reveal, false, "runtime_not_active")
				return 0
			}
			fmt.Fprintln(stdout, "Historical session — FreeInference is not currently active.")
			printFullStatus(stdout, snap, gs, reveal)
			return 0
		}
		if jsonOut {
			// Status-line mode: zero output on identity failure
			return 0
		}
		// Interactive mode: report sanitized identity error
		fmt.Fprintf(stderr, "error: %s\n", secure.SanitizeField(aidErr.Error()))
		return 1
	}

	// Interactive diagnostic mode: show data.
	// The strict gate applies via buildView which receives the current activation ID.
	vm := buildView(snap, gs, aid, true, snap.Client.Type, snap.Session.ID)
	if historical && !vm.Eligible {
		if jsonOut {
			writeHistoricalSnapshotJSON(stdout, snap, reveal, true, "historical_snapshot")
			return 0
		}
		fmt.Fprintln(stdout, "Historical session — not a current live surface.")
		printFullStatus(stdout, snap, gs, reveal)
		return 0
	}
	rc := renderConfig()
	if jsonOut {
		data, err := vm.JSON()
		if err != nil {
			fmt.Fprintln(stdout, "{}")
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintln(stdout, vm.Expanded(rc))
	return 0
}

func writeHistoricalSnapshotJSON(stdout io.Writer, snap *schema.Snapshot, reveal, runtimeActive bool, reason string) {
	obj := map[string]any{
		"historical":     true,
		"runtime_active": runtimeActive,
		"reason":         reason,
		"session_id":     displaySessionID(snap.Session.ID, reveal),
		"client":         secure.SanitizeField(snap.Client.Type),
		"session_status": secure.SanitizeField(snap.Session.Status),
		"model_id":       secure.SanitizeField(snap.Model.ID),
		"provider":       secure.SanitizeField(snap.Provider.Name),
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(obj)
}

// cmdRender implements `freeinference render --mode line|standard|expanded`.
func cmdRender(paths state.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	mode := ""
	// Extract --color flag.
	_, remainingArgs, colorErr := parseColorFlag(args)
	if colorErr != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", colorErr)
		return 2
	}
	// Build a filtered args list for parseClientSessionFlags: skip --mode and its
	// value so the flag parser doesn't choke on the positional-looking mode token.
	var parseArgs []string
	for i := 0; i < len(remainingArgs); i++ {
		if remainingArgs[i] == "--mode" && i+1 < len(remainingArgs) {
			i++ // skip value
		} else {
			parseArgs = append(parseArgs, remainingArgs[i])
		}
	}
	for i := 0; i < len(remainingArgs); i++ {
		if remainingArgs[i] == "--mode" && i+1 < len(remainingArgs) {
			i++
			mode = remainingArgs[i]
		}
	}
	clientType, sessionID, _, reveal, _, err := parseClientSessionFlags(parseArgs)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	historical := explicitSessionRequested(parseArgs)
	if mode == "" {
		mode = "line"
	}
	if mode != "line" && mode != "standard" && mode != "expanded" {
		fmt.Fprintf(stderr, "unknown render mode: %s (want line|standard|expanded)\n", mode)
		return 2
	}

	// Derive activation ID to check for identity errors.
	activation := activationForCLICommand("render", parseArgs)
	if stdinHasData(stdin) {
		activation = runtime.EvaluateForClient(runtime.ClientClaudeCode)
	}
	aid, aidErr := activationID(activation)

	// Status-line mode (stdin input): zero output on identity failure.
	if aidErr != nil && stdinHasData(stdin) {
		return 0
	}

	// Status payload on stdin takes priority (same path as status line).
	if sessionID == "" {
		if snap, consumed := updateFromStdinStatus(paths, stdin); consumed {
			if snap == nil {
				return 0
			}
			return printRendered(stdout, stderr, snap, loadGlobal(paths), mode, aid, aidErr, reveal, false)
		}
	}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resolved == nil {
		fmt.Fprintln(stdout, "FI: no session")
		return 0
	}
	return printRendered(stdout, stderr, resolved.Snap, loadGlobal(paths), mode, aid, aidErr, reveal, historical)
}

func printRendered(stdout, stderr io.Writer, snap *schema.Snapshot, gs *schema.GlobalState, mode string, aid string, aidErr error, reveal bool, historical bool) int {
	// Handle identity error: report sanitized error for interactive mode.
	if aidErr != nil {
		if historical && strings.Contains(aidErr.Error(), "runtime not active") {
			fmt.Fprintln(stdout, "Historical session — FreeInference is not currently active.")
			if mode == "expanded" {
				printFullStatus(stdout, snap, gs, reveal)
			}
			return 0
		}
		fmt.Fprintf(stderr, "error: %s\n", secure.SanitizeField(aidErr.Error()))
		return 1
	}

	// Interactive diagnostic mode: show data regardless of activation state.
	// Note: For render, we use the activation ID (aid) to gate visibility via SurfaceEligibility.
	vm := buildView(snap, gs, aid, true, snap.Client.Type, snap.Session.ID)
	if historical && !vm.Eligible {
		fmt.Fprintln(stdout, "Historical session — not a current live surface.")
		if mode == "expanded" {
			printFullStatus(stdout, snap, gs, reveal)
		}
		return 0
	}
	rc := renderConfig()
	switch mode {
	case "expanded":
		fmt.Fprintln(stdout, vm.Expanded(rc))
	case "standard":
		fmt.Fprintln(stdout, vm.Standard(rc))
	default:
		line := vm.Line(rc)
		if line != "" {
			fmt.Fprintln(stdout, line)
		}
	}
	return 0
}

// updateFromStdinStatus reads a Claude status payload from stdin (if any),
// updates state, and returns the updated snapshot. Returns nil when stdin
// has no usable payload.
func updateFromStdinStatus(paths state.Paths, stdin io.Reader) (*schema.Snapshot, bool) {
	if !stdinHasData(stdin) {
		return nil, false
	}
	var statusInput schema.ClaudeStatusLineInput
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&statusInput); err != nil || statusInput.SessionID == "" {
		return nil, false
	}
	activation := runtime.EvaluateForClient(runtime.ClientClaudeCode)
	if !activation.Active {
		return nil, true
	}
	adapter := adapters.NewClaudeAdapter(paths)
	_ = adapter.HandleStatusLineUpdateWith(&statusInput, statusInput.SessionID, activation)
	snap, _ := state.LoadSnapshot(paths, schema.ClientClaudeCode, statusInput.SessionID)
	return snap, true
}
