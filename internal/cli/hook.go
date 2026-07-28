package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/background"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// runHook is the fail-open hook entry point. Every failure mode — missing
// arguments, unknown client or event, invalid JSON, missing session ID,
// unavailable state, lock contention — returns without output and exit 0.
func runHook(args []string, stdin io.Reader, stdout io.Writer, _ io.Writer) {
	if len(args) < 2 {
		return
	}
	clientType := args[0]
	eventName := args[1]

	paths, err := state.NewPaths()
	if err != nil {
		return
	}
	if err := paths.EnsureDirs(); err != nil {
		return
	}

	switch clientType {
	case schema.ClientClaudeCode:
		handleClaudeHook(paths, eventName, stdin, stdout)
	case schema.ClientCodex:
		handleCodexHook(paths, eventName, stdin, stdout)
	default:
		// Unknown client — fail open silently.
		return
	}
}

func handleClaudeHook(paths state.Paths, eventName string, stdin io.Reader, stdout io.Writer) {
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
		_ = adapter.HandleSessionStart(input)
		maybeRequestDetachedRefresh(paths)
	case "SessionEnd":
		_ = adapter.HandleSessionEnd(sessionID)
		maybeRequestDetachedRefresh(paths)
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
		// Unknown event — fail open.
		return
	}
}

func handleCodexHook(paths state.Paths, eventName string, stdin io.Reader, stdout io.Writer) {
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
		_ = adapter.HandleSessionStart(input)
		maybeRequestDetachedRefresh(paths)
	case "SessionEnd":
		_ = adapter.HandleSessionEnd(sessionID)
		maybeRequestDetachedRefresh(paths)
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
// stale. It never runs for unconfirmed providers (don't phone home from
// non-FreeInference sessions) and can be disabled with FI_NO_BACKGROUND=1.
func maybeRequestDetachedRefresh(paths state.Paths) {
	if os.Getenv("FI_NO_BACKGROUND") == "1" {
		return
	}
	if !adapters.DetectProvider().Confirmed {
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
