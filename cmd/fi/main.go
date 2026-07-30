package main

import (
	"encoding/hex"
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/cli"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
)

var (
	version = "0.1.0"
	commit  = "dev"
)

func main() {
	// Test-only flag for salt race testing
	if len(os.Args) > 1 && os.Args[1] == "-salt-test" {
		if len(os.Args) < 3 {
			os.Stderr.WriteString("usage: fi -salt-test <cache-dir>\n")
			os.Exit(1)
		}
		os.Setenv("FI_CACHE_DIR", os.Args[2])
		loader := runtime.DefaultSaltLoader()
		salt, err := loader()
		if err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString(hex.EncodeToString(salt) + "\n")
		return
	}

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
