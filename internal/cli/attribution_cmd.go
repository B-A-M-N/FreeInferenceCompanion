package cli

import (
	"fmt"
	"io"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
)

func cmdAttribution(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printAttributionHelp(stdout)
		return 2
	}
	switch args[0] {
	case "status":
		return attributionStatus(args[1:], stdout, stderr)
	case "set":
		return attributionSet(args[1:], stdout, stderr)
	case "--help", "-h", "help":
		printAttributionHelp(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown attribution command: %s\n", args[0])
		printAttributionHelp(stderr)
		return 2
	}
}

func attributionStatus(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOut = true
			continue
		}
		fmt.Fprintf(stderr, "unknown flag: %s\n", arg)
		return 2
	}
	mgr, err := config.NewManager()
	if err != nil {
		fmt.Fprintf(stderr, "error: load configuration: %v\n", err)
		return 1
	}
	eff, err := mgr.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "error: resolve configuration: %v\n", err)
		return 1
	}
	if jsonOut {
		if err := encodeJSON(stdout, map[string]any{"commit_mode": eff.Attribution.CommitMode}); err != nil {
			fmt.Fprintf(stderr, "error: encode attribution status: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "Commit attribution: %s\n", eff.Attribution.CommitMode.Value)
	return 0
}

func attributionSet(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	mode := ""
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case mode == "":
			mode = arg
		default:
			fmt.Fprintf(stderr, "unexpected argument: %s\n", arg)
			return 2
		}
	}
	if mode == "" {
		fmt.Fprintln(stderr, "error: attribution set requires off, append, or standalone")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "error: load configuration: %v\n", err)
		return 1
	}
	if err := config.SetField(cfg, "attribution.commit_mode", mode); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(stderr, "error: save configuration: %v\n", err)
		return 1
	}
	if jsonOut {
		if err := encodeJSON(stdout, map[string]string{"commit_mode": cfg.Attribution.CommitMode}); err != nil {
			fmt.Fprintf(stderr, "error: encode attribution mode: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "Commit attribution set to %s.\n", cfg.Attribution.CommitMode)
	return 0
}

func printAttributionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage: freeinference attribution status [--json]
       freeinference attribution set off|append|standalone [--json]

Manage provenance added to already-authorized agent git commits.

Modes:
  off         Never modify commits (default)
  append      Add inference provenance only when native agent attribution exists
  standalone  Add inference provenance whenever an eligible agent commit runs

Companion never stages, creates, amends, or originates commits.
`)
}
