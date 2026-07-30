package main

import (
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/adapters"
	"github.com/b-a-m-n/freeinference-companion/internal/cli"
	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

var commit = "dev"

func main() {
	// Allow complete disable via environment variable.
	// The disabled state is injected into the CLI so that human-facing
	// commands (status, doctor, etc.) can display a prominent disabled
	// warning while hook commands remain silent no-ops.
	if os.Getenv("FI_DISABLED") == "1" {
		os.Setenv("FI_RUNTIME_INACTIVE", "1")
	}

	cli.Version = version.Version
	cli.Commit = commit
	adapters.PluginVersion = version.Version
	os.Exit(cli.Run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
