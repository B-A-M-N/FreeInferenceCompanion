package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bamn/freeinference-companion/internal/adapters"
	"github.com/bamn/freeinference-companion/internal/api"
	"github.com/bamn/freeinference-companion/internal/background"
	"github.com/bamn/freeinference-companion/internal/state"
	"github.com/bamn/freeinference-companion/pkg/schema"
)

var (
	version = "0.1.0"
	commit  = "dev"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// Resolve state paths
	paths, err := state.NewPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	paths.EnsureDirs()

	switch cmd {
	case "status":
		cmdStatus(paths, args)
	case "models":
		cmdModels(paths, args)
	case "doctor":
		cmdDoctor(args)
	case "report":
		cmdReport(paths, args)
	case "dashboard":
		cmdDashboard()
	case "context":
		cmdContext(paths, args)
	case "refresh":
		cmdRefresh(paths, args)
	case "hook":
		cmdHook(paths, args)
	case "status-line":
		cmdStatusLine(args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `FreeInference Companion v`+version+`

Usage:
  fi status [--client <type>] [--compact] [--session <id>]
  fi models [--model <name>] [--refresh]
  fi doctor [--probe]
  fi report [--session <id>]
  fi dashboard
  fi context [--session <id>]
  fi refresh [--force]
  fi hook <client> <event>
  fi status-line install|uninstall

Environment:
  FREEINFERENCE_API_KEY    FreeInference API key
  FREEINFERENCE_BASE_URL   API base URL (default: https://freeinference.org/v1)
  FI_HEALTH_URL            Health monitoring URL (optional)
  FI_CACHE_DIR             Cache directory (default: ~/.cache/freeinference-companion)
`)
}

// ============================================================
// fi status
// ============================================================

func cmdStatus(paths state.Paths, args []string) {
	compact := false
	clientType := schema.ClientClaudeCode
	sessionID := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--compact":
			compact = true
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
		}
	}

	// If session not specified and stdin has data, try to read from stdin
	if sessionID == "" && isStdinAvailable() {
		// Read Claude status line from stdin
		var statusInput schema.ClaudeStatusLineInput
		if err := json.NewDecoder(os.Stdin).Decode(&statusInput); err == nil {
			if statusInput.SessionID != "" {
				sessionID = statusInput.SessionID
				// Update session state from status line
				adapter := adapters.NewClaudeAdapter(paths)
				adapter.HandleStatusLineUpdate(&statusInput, sessionID)
			}
		}
	}

	if sessionID == "" {
		fmt.Println("FI: no session")
		return
	}

	snap, err := state.LoadSnapshot(paths, clientType, sessionID)
	if err != nil || snap == nil {
		fmt.Println("FI: no data")
		return
	}

	// Load health if available
	gs, _ := state.LoadGlobal(paths)

	if compact {
		fmt.Println(adapters.FormatStatusLineCompact(snap, gs.Health))
		return
	}

	// Full output
	fmt.Printf("FreeInference Companion %s\n", version)
	fmt.Printf("Session: %s (%s)\n", snap.Session.ID, snap.Session.Status)
	fmt.Printf("Model:   %s (%d context)\n", snap.Model.ID, snap.Model.ContextLength)
	fmt.Printf("\n")

	// Live context
	if snap.LiveContext != nil {
		fmt.Printf("Live Context (from %s at %s):\n", snap.LiveContext.Source, snap.LiveContext.ObservedAt.Format(time.RFC3339))
		fmt.Printf("  Window:     ")
		if snap.LiveContext.ContextWindowSize != nil {
			fmt.Printf("%s", formatTokenCount(*snap.LiveContext.ContextWindowSize))
		} else {
			fmt.Printf("?")
		}
		if snap.LiveContext.UsedPercentage != nil {
			fmt.Printf(" (%.1f%% used)", *snap.LiveContext.UsedPercentage)
		}
		fmt.Printf("\n")
		fmt.Printf("  Fresh:      %s\n", formatTokenPtr(snap.LiveContext.FreshInputTokens))
		fmt.Printf("  Cache Read: %s\n", formatTokenPtr(snap.LiveContext.CacheReadInputTokens))
		fmt.Printf("  Cache New:  %s\n", formatTokenPtr(snap.LiveContext.CacheCreationInputTokens))
		fmt.Printf("  Output:     %s\n", formatTokenPtr(snap.LiveContext.OutputTokens))
		fmt.Printf("\n")
	}

	// Pressure
	fmt.Printf("Pressure:   %s", snap.Pressure.State)
	if snap.Pressure.ProjectedPercentage != nil {
		fmt.Printf(" (projected %.0f%%, confidence: %s)", *snap.Pressure.ProjectedPercentage, snap.Pressure.ProjectionConfidence)
	}
	fmt.Printf("\n")
	if snap.Pressure.Reason != nil {
		fmt.Printf("  Reason:  %s\n", *snap.Pressure.Reason)
	}
	fmt.Printf("\n")

	// Cache
	if snap.CacheAnalysis != nil {
		fmt.Printf("Cache Analysis (%d samples):\n", snap.CacheAnalysis.RequestSamples)
		fmt.Printf("  Read Share:  %s\n", formatPctPtr(snap.CacheAnalysis.CacheReadShare))
		fmt.Printf("  New Share:   %s\n", formatPctPtr(snap.CacheAnalysis.CacheCreationShare))
		fmt.Printf("  Fresh Share: %s\n", formatPctPtr(snap.CacheAnalysis.FreshInputShare))
		fmt.Printf("  Trend:       %s\n", snap.CacheAnalysis.Trend)
		fmt.Printf("\n")
	}

	// Health
	if gs != nil && gs.Health != nil {
		fmt.Printf("Provider Health (%s):\n", gs.Health.Source)
		fmt.Printf("  Status:   %s\n", gs.Health.Status)
		if gs.Health.HealthyCount != nil {
			fmt.Printf("  Healthy:  %d/%d\n", *gs.Health.HealthyCount, *gs.Health.HealthyCount+*gs.Health.UnhealthyCount)
		}
		fmt.Printf("  Checked:  %s\n", gs.Health.FetchedAt.Format(time.RFC3339))
		fmt.Printf("\n")
	}

	// Last failure
	if snap.LastFailure != nil {
		fmt.Printf("Last Failure: %s (at %s)\n", snap.LastFailure.Category, snap.LastFailure.ObservedAt.Format(time.RFC3339))
	}
}

// ============================================================
// fi models
// ============================================================

func cmdModels(paths state.Paths, args []string) {
	modelName := ""
	forceRefresh := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				i++
				modelName = args[i]
			}
		case "--refresh":
			forceRefresh = true
		}
	}

	// Try to load cached models
	gs, err := state.LoadGlobal(paths)
	if err != nil {
		gs = &schema.GlobalState{}
	}

	if forceRefresh || gs.Models == nil || time.Since(gs.Models.FetchedAt) > time.Hour {
		client := newAPIClient()
		refresher := background.NewRefresher(client, paths, os.Getenv("FI_HEALTH_URL"))
		result := refresher.ForceRefresh()
		if result.Error != "" {
			fmt.Fprintf(os.Stderr, "refresh error: %s\n", result.Error)
		}
		gs, _ = state.LoadGlobal(paths)
	}

	if gs.Models == nil || len(gs.Models.Models) == 0 {
		fmt.Println("No model data available. Use --refresh to fetch.")
		return
	}

	if modelName != "" {
		for _, m := range gs.Models.Models {
			if m.ID == modelName || m.Name == modelName {
				printModelDetail(m)
				return
			}
		}
		fmt.Printf("Model '%s' not found in catalog.\n", modelName)
		return
	}

	fmt.Printf("FreeInference Models (cached at %s):\n", gs.Models.FetchedAt.Format(time.RFC3339))
	fmt.Printf("%-24s %-12s %-12s %-6s %s\n", "MODEL", "CONTEXT", "MAX OUTPUT", "STATE", "FEATURES")
	fmt.Println(strings.Repeat("-", 90))
	for _, m := range gs.Models.Models {
		state := accessSymbol(m.AccessState)
		features := strings.Join(m.Features, ",")
		if len(features) > 30 {
			features = features[:30] + "..."
		}
		fmt.Printf("%-24s %-12s %-12s %-6s %s\n",
			m.ID,
			formatTokenCount(int64(m.ContextLength)),
			formatTokenCount(int64(m.MaxOutputLength)),
			state,
			features,
		)
	}
}

func printModelDetail(m schema.CatalogModel) {
	fmt.Printf("Model: %s\n", m.ID)
	if m.Name != "" {
		fmt.Printf("Name:  %s\n", m.Name)
	}
	fmt.Printf("Context Window: %s\n", formatTokenCount(int64(m.ContextLength)))
	fmt.Printf("Max Output:     %s\n", formatTokenCount(int64(m.MaxOutputLength)))
	fmt.Printf("Access:         %s\n", m.AccessState)
	if len(m.Features) > 0 {
		fmt.Printf("Features:       %s\n", strings.Join(m.Features, ", "))
	}
	if len(m.Pricing) > 0 {
		fmt.Println("Pricing (per MTok):")
		for k, v := range m.Pricing {
			fmt.Printf("  %s: $%s\n", k, v)
		}
	}
}

// ============================================================
// fi doctor
// ============================================================

func cmdDoctor(args []string) {
	probe := false
	for _, a := range args {
		if a == "--probe" {
			probe = true
		}
	}

	client := newAPIClient()

	fmt.Println("FreeInference Doctor")
	fmt.Println(strings.Repeat("-", 60))

	// Check endpoint reachability
	fmt.Print("Endpoint reachable... ")
	result := client.Probe()
	if result.EndpointReachable {
		fmt.Println("✓")
	} else {
		fmt.Printf("✗ (%s)\n", result.Error)
		os.Exit(1)
	}

	// Check API key
	apiKey := os.Getenv("FREEINFERENCE_API_KEY")
	if apiKey != "" {
		if api.VerifyAPIKey(apiKey) {
			fmt.Println("API key present:     ✓ (format valid)")
		} else {
			fmt.Println("API key present:     ~ (unusual format, will attempt anyway)")
		}
	} else {
		fmt.Println("API key:             ~ (not set, limited functionality)")
	}

	// Check auth
	fmt.Print("Authentication...... ")
	if result.AuthAccepted {
		fmt.Println("✓")
	} else {
		fmt.Println("✗")
	}

	// Check models
	fmt.Print("Model catalog....... ")
	if result.ModelFound {
		fmt.Println("✓")
	} else {
		fmt.Println("✗")
	}

	// Health URL
	healthURL := os.Getenv("FI_HEALTH_URL")
	if healthURL != "" {
		fmt.Printf("Health source:       %s\n", healthURL)
	} else {
		fmt.Println("Health source:       ~ (not configured)")
	}

	// Optional probe
	if probe {
		fmt.Println()
		fmt.Println("Inference Probe (synthetic):")
		probeResult := client.ProbeInference("")
		if probeResult.EndpointReachable && probeResult.AuthAccepted {
			fmt.Println("  Inference: ✓ (synthetic request succeeded)")
		} else {
			fmt.Printf("  Inference: ✗ (%s)\n", probeResult.Error)
		}
	}

	fmt.Println()
	fmt.Println("Doctor complete.")
}

// ============================================================
// fi report
// ============================================================

func cmdReport(paths state.Paths, args []string) {
	sessionID := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			i++
			sessionID = args[i]
		}
	}

	clientType := schema.ClientClaudeCode
	gs, _ := state.LoadGlobal(paths)

	if sessionID == "" {
		// List available sessions
		fmt.Println("FreeInference Companion Report")
		fmt.Println(strings.Repeat("=", 60))
		fmt.Printf("Version: %s\n", version)
		fmt.Printf("Time:    %s\n", time.Now().UTC().Format(time.RFC3339))
		fmt.Println()

		// Global health
		if gs.Health != nil {
			fmt.Printf("Provider Health: %s\n", gs.Health.Status)
			if gs.Health.HealthyCount != nil {
				fmt.Printf("  Models: %d healthy, %d unhealthy\n", *gs.Health.HealthyCount, *gs.Health.UnhealthyCount)
			}
		}
		fmt.Println()
		fmt.Println("No session specified. Use --session <id> or check snapshot files.")
		return
	}

	snap, err := state.LoadSnapshot(paths, clientType, sessionID)
	if err != nil || snap == nil {
		fmt.Println("Session not found.")
		return
	}

	fmt.Println("FreeInference Companion Report")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Plugin Version: %s\n", version)
	fmt.Printf("Client:         %s\n", snap.Client.Type)
	fmt.Printf("Session:        %s\n", snap.Session.ID)
	fmt.Printf("Status:         %s\n", snap.Session.Status)
	fmt.Printf("Start:          %s\n", snap.Session.StartedAt.Format(time.RFC3339))
	fmt.Printf("Model:          %s\n", snap.Model.ID)
	fmt.Printf("Health Source:  %s\n", os.Getenv("FI_HEALTH_URL"))

	if snap.LiveContext != nil {
		fmt.Println()
		fmt.Println("--- Live Context ---")
		fmt.Printf("Used:           %.1f%%\n", safePct(snap.LiveContext.UsedPercentage))
		fmt.Printf("Fresh Input:    %s\n", formatTokenPtr(snap.LiveContext.FreshInputTokens))
		fmt.Printf("Cache Read:     %s\n", formatTokenPtr(snap.LiveContext.CacheReadInputTokens))
		fmt.Printf("Cache New:      %s\n", formatTokenPtr(snap.LiveContext.CacheCreationInputTokens))
		fmt.Printf("Output:         %s\n", formatTokenPtr(snap.LiveContext.OutputTokens))
	}

	fmt.Println()
	fmt.Println("--- Pressure ---")
	fmt.Printf("State:          %s\n", snap.Pressure.State)
	if snap.Pressure.ProjectedPercentage != nil {
		fmt.Printf("Projected:      %.0f%%\n", *snap.Pressure.ProjectedPercentage)
	}

	if snap.LastFailure != nil {
		fmt.Println()
		fmt.Println("--- Last Failure ---")
		fmt.Printf("Category:       %s\n", snap.LastFailure.Category)
		fmt.Printf("Time:           %s\n", snap.LastFailure.ObservedAt.Format(time.RFC3339))
	}

	if gs.Health != nil {
		fmt.Println()
		fmt.Println("--- Provider Health ---")
		fmt.Printf("Status:         %s\n", gs.Health.Status)
		fmt.Printf("Checked:        %s\n", gs.Health.FetchedAt.Format(time.RFC3339))
	}

	fmt.Println()
	fmt.Println("--- Sanitized ---")
	fmt.Println("No API keys, prompts, responses, or repository contents included.")
}

// ============================================================
// fi dashboard
// ============================================================

func cmdDashboard() {
	url := "https://status.staging.freeinference.org/"
	fmt.Printf("Opening: %s\n", url)
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "could not open browser: %v\n", err)
		fmt.Printf("Visit: %s\n", url)
	}
}

// ============================================================
// fi context
// ============================================================

func cmdContext(paths state.Paths, args []string) {
	sessionID := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			i++
			sessionID = args[i]
		}
	}

	if sessionID == "" {
		fmt.Println("Use: fi context --session <session-id>")
		return
	}

	snap, err := state.LoadSnapshot(paths, schema.ClientClaudeCode, sessionID)
	if err != nil || snap == nil {
		fmt.Println("No session data.")
		return
	}

	usedPct := 0.0
	if snap.LiveContext != nil && snap.LiveContext.UsedPercentage != nil {
		usedPct = *snap.LiveContext.UsedPercentage
	}

	fmt.Printf("Context:         %.1f%%\n", usedPct)
	fmt.Printf("State:           %s\n", snap.Pressure.State)
	if snap.Pressure.ProjectedPercentage != nil {
		fmt.Printf("Projected:       %.0f%%\n", *snap.Pressure.ProjectedPercentage)
	}
	fmt.Printf("Limit:           %s\n", formatTokenCount(int64(snap.Model.ContextLength)))

	// Recommendation
	switch snap.Pressure.State {
	case schema.PressureWatch:
		fmt.Println("Suggestion: Monitor context growth.")
	case schema.PressureWarn:
		fmt.Println("Suggestion: Consider compacting soon.")
	case schema.PressureCritical:
		fmt.Println("Suggestion: Compact or start a fresh session.")
	default:
		fmt.Println("Suggestion: No action needed.")
	}
}

// ============================================================
// fi refresh
// ============================================================

func cmdRefresh(paths state.Paths, args []string) {
	force := false
	for _, a := range args {
		if a == "--force" {
			force = true
		}
	}

	client := newAPIClient()
	refresher := background.NewRefresher(client, paths, os.Getenv("FI_HEALTH_URL"))

	var result *background.RefreshResult
	if force {
		result = refresher.ForceRefresh()
	} else {
		result = refresher.RefreshIfStale()
	}

	if result.ModelsRefreshed {
		fmt.Println("Models refreshed.")
	}
	if result.HealthRefreshed {
		fmt.Println("Health refreshed.")
	}
	if result.Error != "" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", result.Error)
	}
}

// ============================================================
// fi hook <client> <event>
// ============================================================

func cmdHook(paths state.Paths, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: fi hook <client> <event>")
		os.Exit(1)
	}

	clientType := args[0]
	eventName := args[1]

	switch clientType {
	case schema.ClientClaudeCode:
		handleClaudeHook(paths, eventName)
	case schema.ClientCodex:
		handleCodexHook(paths, eventName)
	default:
		fmt.Fprintf(os.Stderr, "unknown client: %s\n", clientType)
		os.Exit(1)
	}
}

func handleClaudeHook(paths state.Paths, eventName string) {
	adapter := adapters.NewClaudeAdapter(paths)
	event, err := adapter.ParseHookEvent(os.Stdin)
	if err != nil {
		// Fail open
		os.Exit(0)
	}

	sessionID := event.Payload.SessionID
	if sessionID == "" {
		os.Exit(0)
	}

	switch eventName {
	case "SessionStart":
		err = adapter.HandleSessionStart(event)
	case "SessionEnd":
		err = adapter.HandleSessionEnd(sessionID)
	case "UserPromptSubmit":
		var output *schema.ClaudeWarningOutput
		output, err = adapter.HandleUserPromptSubmit(event, sessionID)
		if err == nil && output != nil {
			data, _ := json.Marshal(output)
			fmt.Println(string(data))
		}
		return
	case "PreCompact":
		err = adapter.HandlePreCompact(sessionID)
	case "PostCompact":
		err = adapter.HandlePostCompact(sessionID)
	case "Stop":
		err = adapter.HandleStop(sessionID)
	case "StopFailure":
		err = adapter.HandleStopFailure(event, sessionID)
	default:
		// Unknown event — fail open
		return
	}

	if err != nil {
		// Fail open: log but don't block
		fmt.Fprintf(os.Stderr, "hook %s: %v\n", eventName, err)
	}
}

func handleCodexHook(paths state.Paths, eventName string) {
	adapter := adapters.NewCodexAdapter(paths)
	event, err := adapter.ParseHookEvent(os.Stdin)
	if err != nil {
		os.Exit(0)
	}

	sessionID := event.Payload.SessionID
	if sessionID == "" {
		os.Exit(0)
	}

	switch eventName {
	case "SessionStart":
		err = adapter.HandleSessionStart(event)
	case "SessionEnd":
		err = adapter.HandleSessionEnd(sessionID)
	case "UserPromptSubmit":
		var output *schema.CodexWarningOutput
		output, err = adapter.HandleUserPromptSubmit(event, sessionID)
		if err == nil && output != nil {
			data, _ := json.Marshal(output)
			fmt.Println(string(data))
		}
		return
	case "PreCompact":
		err = adapter.HandlePreCompact(sessionID)
	case "PostCompact":
		err = adapter.HandlePostCompact(sessionID)
	case "Stop":
		err = adapter.HandleStop(sessionID)
	default:
		return
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "hook %s: %v\n", eventName, err)
	}
}

// ============================================================
// fi status-line install|uninstall
// ============================================================

func cmdStatusLine(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: fi status-line install|uninstall")
		os.Exit(1)
	}

	switch args[0] {
	case "install":
		installStatusLine()
	case "uninstall":
		uninstallStatusLine()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func installStatusLine() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	claudeDir := home + "/.claude"
	settingsFile := claudeDir + "/settings.json"

	// Ensure .claude directory exists
	os.MkdirAll(claudeDir, 0755)

	// Generate wrapper script
	wrapperPath := claudeDir + "/statusline-freeinference.sh"
	wrapper := `#!/usr/bin/env bash
# FreeInference Companion status line wrapper
# Generated by: fi status-line install
set -u
input="$(cat)"
# Replay to any existing status line and our fi status
printf '%s' "$input" | fi status --compact --client claude
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error writing wrapper: %v\n", err)
		os.Exit(1)
	}

	// Read or create settings.json
	settings := map[string]interface{}{}
	existingData, err := os.ReadFile(settingsFile)
	if err == nil {
		json.Unmarshal(existingData, &settings)
	}

	// Set statusLine
	statusLine := map[string]interface{}{
		"type":            "command",
		"command":         wrapperPath,
		"refreshInterval": float64(5),
	}
	settings["statusLine"] = statusLine

	// Write settings.json with backup
	backupFile := settingsFile + ".bak"
	if _, err := os.Stat(settingsFile); err == nil {
		os.Rename(settingsFile, backupFile)
	}

	data, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(settingsFile, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing settings: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Status line installed.\n")
	fmt.Printf("  Wrapper: %s\n", wrapperPath)
	fmt.Printf("  Config:  %s\n", settingsFile)
	if _, err := os.Stat(backupFile); err == nil {
		fmt.Printf("  Backup:  %s\n", backupFile)
	}
	fmt.Println()
	fmt.Println("Restart Claude Code to see the FreeInference status line.")
}

func uninstallStatusLine() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	claudeDir := home + "/.claude"
	settingsFile := claudeDir + "/settings.json"
	backupFile := settingsFile + ".bak"
	wrapperPath := claudeDir + "/statusline-freeinference.sh"

	// Remove wrapper
	os.Remove(wrapperPath)

	// Restore backup
	if _, err := os.Stat(backupFile); err == nil {
		os.Rename(backupFile, settingsFile)
		fmt.Printf("Restored original settings from backup.\n")
	} else {
		// Remove statusLine from settings
		data, err := os.ReadFile(settingsFile)
		if err == nil {
			var settings map[string]interface{}
			if json.Unmarshal(data, &settings) == nil {
				delete(settings, "statusLine")
				newData, _ := json.MarshalIndent(settings, "", "  ")
				os.WriteFile(settingsFile, newData, 0644)
			}
		}
	}

	fmt.Println("Status line uninstalled.")
}

// ============================================================
// Helpers
// ============================================================

func newAPIClient() *api.Client {
	baseURL := os.Getenv("FREEINFERENCE_BASE_URL")
	apiKey := os.Getenv("FREEINFERENCE_API_KEY")
	if baseURL == "" {
		baseURL = api.DefaultBaseURL
	}
	return api.NewClient(baseURL, apiKey, api.DefaultConnectTimeout)
}

func isStdinAvailable() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) == 0
}

func formatTokenCount(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.0fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatTokenPtr(p *int64) string {
	if p == nil {
		return "N/A"
	}
	return formatTokenCount(*p)
}

func formatPctPtr(p *float64) string {
	if p == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.0f%%", *p*100)
}

func safePct(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

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

// openBrowser opens a URL in the default browser.
func openBrowser(url string) error {
	// Try xdg-open first (Linux), then open (macOS)
	cmds := []string{"xdg-open", "open"}
	for _, cmd := range cmds {
		if err := execCommand(cmd, url); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no browser opener found")
}

func execCommand(name, arg string) error {
	// Simple exec without fork/exec
	attr := os.ProcAttr{
		Files: []*os.File{nil, nil, os.Stderr},
	}
	proc, err := os.StartProcess(name, []string{name, arg}, &attr)
	if err != nil {
		return err
	}
	proc.Release()
	return nil
}