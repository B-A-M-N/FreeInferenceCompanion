package main

import (
	"os"

	"github.com/b-a-m-n/freeinference-companion/internal/cli"
)

var (
	version = "0.1.0"
	commit  = "dev"
)

func main() {
	// Allow complete disable via environment variable — exits immediately
	if os.Getenv("FI_DISABLED") == "1" {
		os.Exit(0)
	}
	cli.Version = version
	cli.Commit = commit
	os.Exit(cli.Run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
