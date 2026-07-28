package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// cmdSessions implements `fi sessions`.
func cmdSessions(paths state.Paths, _ []string, stdout, stderr io.Writer) int {
	idx, err := state.LoadSessionIndex(paths)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if len(idx.Sessions) == 0 {
		fmt.Fprintln(stdout, "No sessions recorded.")
		return 0
	}
	fmt.Fprintf(stdout, "%-12s %-40s %-20s %-10s %s\n", "CLIENT", "SESSION", "MODEL", "STATUS", "LAST EVENT")
	for _, e := range idx.Sessions {
		sessionID := e.SessionID
		if len(sessionID) > 40 {
			sessionID = sessionID[:37] + "..."
		}
		model := e.ModelID
		if len(model) > 20 {
			model = model[:17] + "..."
		}
		fmt.Fprintf(stdout, "%-12s %-40s %-20s %-10s %s\n",
			e.Client, sessionID, model, e.Status, e.LastEventAt.Format(time.RFC3339))
	}
	return 0
}

// cmdSnapshot implements `fi snapshot --json` — the machine-readable
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
	clientType, sessionID, _, _, err := parseClientSessionFlags(flagArgs)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}

	// Accept a Claude status payload on stdin (updates state first).
	if sessionID == "" {
		if snap := updateFromStdinStatus(paths, stdin); snap != nil {
			return printSnapshot(stdout, snap, loadGlobal(paths), jsonOut)
		}
	}

	resolved, err := resolveSession(paths, clientType, sessionID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resolved == nil {
		if jsonOut {
			vm := buildView(nil, loadGlobal(paths))
			data, _ := vm.JSON()
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		fmt.Fprintln(stdout, "FI: no session")
		return 0
	}
	return printSnapshot(stdout, resolved.Snap, loadGlobal(paths), jsonOut)
}

func printSnapshot(stdout io.Writer, snap *schema.Snapshot, gs *schema.GlobalState, jsonOut bool) int {
	vm := buildView(snap, gs)
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

// cmdRender implements `fi render --mode line|expanded`.
func cmdRender(paths state.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	mode := ""
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--mode" && i+1 < len(args) {
			i++
			mode = args[i]
		} else {
			flagArgs = append(flagArgs, args[i])
		}
	}
	clientType, sessionID, _, _, err := parseClientSessionFlags(flagArgs)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %v\n", err)
		return 2
	}
	if mode == "" {
		mode = "line"
	}
	if mode != "line" && mode != "expanded" {
		fmt.Fprintf(stderr, "unknown render mode: %s (want line|expanded)\n", mode)
		return 2
	}

	// Status payload on stdin takes priority (same path as status line).
	if sessionID == "" {
		if snap := updateFromStdinStatus(paths, stdin); snap != nil {
			return printRendered(stdout, snap, loadGlobal(paths), mode)
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
	return printRendered(stdout, resolved.Snap, loadGlobal(paths), mode)
}

func printRendered(stdout io.Writer, snap *schema.Snapshot, gs *schema.GlobalState, mode string) int {
	vm := buildView(snap, gs)
	rc := renderConfig()
	if mode == "expanded" {
		fmt.Fprintln(stdout, vm.Expanded(rc))
	} else {
		fmt.Fprintln(stdout, vm.Line(rc))
	}
	return 0
}

// updateFromStdinStatus reads a Claude status payload from stdin (if any),
// updates state, and returns the updated snapshot. Returns nil when stdin
// has no usable payload.
func updateFromStdinStatus(paths state.Paths, stdin io.Reader) *schema.Snapshot {
	if !stdinHasData(stdin) {
		return nil
	}
	var statusInput schema.ClaudeStatusLineInput
	if err := json.NewDecoder(stdin).Decode(&statusInput); err != nil || statusInput.SessionID == "" {
		return nil
	}
	adapter := adapters.NewClaudeAdapter(paths)
	_ = adapter.HandleStatusLineUpdate(&statusInput, statusInput.SessionID)
	snap, _ := state.LoadSnapshot(paths, schema.ClientClaudeCode, statusInput.SessionID)
	return snap
}
