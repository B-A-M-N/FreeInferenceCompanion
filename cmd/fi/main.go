package main

import (
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/cli"
)

var (
	version = "0.1.0"
	commit  = "dev"
)

func main() {
	// Allow complete disable via environment variable. The hook entry
	// (fi hook) and operational commands (status, sessions, render, etc.)
	// become no-ops when disabled. However, diagnostic commands (version,
	// doctor, help, report) must still work so an operator can diagnose
	// why the companion is disabled.
	if os.Getenv("FI_DISABLED") == "1" && len(os.Args) > 1 {
		cmd := os.Args[1]
		switch cmd {
		case "version", "--version", "-v", "doctor", "help", "--help", "-h":
			// Diagnostic commands pass through
		default:
			os.Exit(0)
		}
	}
	cli.Version = version
	cli.Commit = commit
	adapters.PluginVersion = version
	os.Exit(cli.Run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
