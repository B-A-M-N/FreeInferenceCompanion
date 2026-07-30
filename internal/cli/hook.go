package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/background"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// runHook is the fail-open hook entry point. Every failure mode — missing
// arguments, unknown client or event, invalid JSON, missing session ID,
// unavailable state, lock contention, INACTIVATION — returns without output
// and exit 0.
//
// P0-2: the activation gate runs BEFORE any state/IO work. When the runtime
// is not active (no validated FreeInference endpoint+key, or FI_DISABLED=1),
// the hook is a true zero-output, zero-side-effect no-op: no cache directory
// creation, no lock files, no session index entry, no event file, no detached
// process, no network request. This is the contract that prevents the
// companion from polluting ordinary Claude / Codex sessions.
func runHook(args []string, stdin io.Reader, stdout io.Writer, _ io.Writer) {
	if len(args) < 2 {
		return
	}
	clientType := args[0]
	eventName := args[1]

	// Activation gate: evaluated exactly once per process. Must be the FIRST
	// real work this function does — every step below touches the filesystem
	// or the network and must be skipped when inactive.
	activation := runtime.Evaluate()
	if !activation.Active {
		return
	}

	paths, err := state.NewPaths()
	if err != nil {
		return
	}
	// Derive an activation identity so global state (health, models,
	// circuit-breakers, session index) is namespaced under providers/<id>/
	// and never mixes with another endpoint or key. Session state stays on
	// the unnamespaced path because sessions are independent of which
	// provider runtime is active.
	loader := runtime.DefaultSaltLoader()
	id, err := activation.Identity(loader)
	if err != nil {
		// Identity derivation failed. For an active runtime, this is a hard failure:
		// no provider-state read, no provider-state write, no FreeInference output.
		// Hook commands still exit 0 but silently perform no mutation.
		return
	}
	dirName := id.DirName()
	if dirName == "" {
		return
	}
	paths = paths.NewNamespacedPaths(dirName)
	if err := paths.EnsureDirs(); err != nil {
		return
	}

	switch clientType {
	case schema.ClientClaudeCode:
		handleClaudeHook(paths, eventName, stdin, stdout, activation)
	case schema.ClientCodex:
		handleCodexHook(paths, eventName, stdin, stdout, activation)
	default:
		// Unknown client — fail open silently.
		return
	}
}

func handleClaudeHook(paths state.Paths, eventName string, stdin io.Reader, stdout io.Writer, activation runtime.Activation) {
	adapter := adapters.NewClaudeAdapter(paths)
	input, err := adapter.ParseHookInput(stdin)
	if err != nil || input == nil {
		return
	}
	sessionID := input.SessionID
	if sessionID == "" {
		return
	}

	switch eventName {
	case "SessionStart":
		_ = adapter.HandleSessionStartWith(input, activation)
		maybeRequestDetachedRefresh(paths, activation)
	case "SessionEnd":
		_ = adapter.HandleSessionEnd(sessionID)
		maybeRequestDetachedRefresh(paths, activation)
	case "UserPromptSubmit":
		output, err := adapter.HandleUserPromptSubmitWith(input, sessionID, activation)
		if err == nil && output != nil {
			if data, merr := json.Marshal(output); merr == nil {
				fmt.Fprintln(stdout, string(data))
			}
		}
	case "PreCompact":
		_ = adapter.HandlePreCompact(input, sessionID)
	case "PostCompact":
		_ = adapter.HandlePostCompact(input, sessionID)
	case "Stop":
		_ = adapter.HandleStop(sessionID)
	case "StopFailure":
		_ = adapter.HandleStopFailure(input, sessionID)
	default:
		// Unknown event — fail open.
		return
	}
}

func handleCodexHook(paths state.Paths, eventName string, stdin io.Reader, stdout io.Writer, activation runtime.Activation) {
	adapter := adapters.NewCodexAdapter(paths)
	input, err := adapter.ParseHookInput(stdin)
	if err != nil || input == nil {
		return
	}
	sessionID := input.SessionID
	if sessionID == "" {
		return
	}

	switch eventName {
	case "SessionStart":
		_ = adapter.HandleSessionStartWith(input, activation)
		maybeRequestDetachedRefresh(paths, activation)
	case "SessionEnd":
		_ = adapter.HandleSessionEnd(sessionID)
		maybeRequestDetachedRefresh(paths, activation)
	case "UserPromptSubmit":
		output, err := adapter.HandleUserPromptSubmit(input, sessionID)
		if err == nil && output != nil {
			if data, merr := json.Marshal(output); merr == nil {
				fmt.Fprintln(stdout, string(data))
			}
		}
	case "PreCompact":
		_ = adapter.HandlePreCompact(input, sessionID)
	case "PostCompact":
		_ = adapter.HandlePostCompact(input, sessionID)
	case "Stop":
		_ = adapter.HandleStop(sessionID)
	case "StopFailure":
		_ = adapter.HandleStopFailure(input, sessionID)
	default:
		return
	}
}

// maybeRequestDetachedRefresh spawns detached refresh workers when caches are
// stale. Called only after the activation gate has already confirmed an active
// FreeInference runtime (P0-2), so the permissive DetectProvider check is gone.
// FI_NO_BACKGROUND=1 still suppresses spawning.
//
// Detached children re-validate activation independently — the parent gate
// alone is insufficient because environment or configuration may change
// between spawn and child exec.
func maybeRequestDetachedRefresh(paths state.Paths, activation runtime.Activation) {
	if os.Getenv("FI_NO_BACKGROUND") == "1" {
		return
	}
	if !activation.Active {
		return
	}
	stale := background.StaleWorkers(paths, os.Getenv("FI_HEALTH_URL"))
	if len(stale) == 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = background.SpawnDetachedWorkers(exe, stale)
}
