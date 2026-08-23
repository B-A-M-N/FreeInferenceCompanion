package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/install"
)

// cmdStatusLine implements `freeinference status-line install|uninstall|status`.
func cmdStatusLine(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: freeinference status-line install|uninstall|status [--scope user|project|local] [--project <dir>] [--json]")
		return 2
	}
	subcommand := args[0]
	rest := args[1:]

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	scope := install.ScopeProject // default: project scope
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = home
	}

	// Parse remaining flags (--json is handled per-subcommand)
	for len(rest) > 0 {
		switch rest[0] {
		case "--json":
			rest = rest[1:]
		case "--scope":
			if len(rest) < 2 {
				fmt.Fprintln(stderr, "error: --scope requires a value (user, project, or local)")
				return 2
			}
			switch rest[1] {
			case "user":
				scope = install.ScopeUser
			case "project":
				scope = install.ScopeProject
			case "local":
				scope = install.ScopeLocal
			default:
				fmt.Fprintf(stderr, "error: unknown scope %q (user, project, local)\n", rest[1])
				return 2
			}
			rest = rest[2:]
		case "--project":
			if len(rest) < 2 {
				fmt.Fprintln(stderr, "error: --project requires a value")
				return 2
			}
			projectRoot, err = filepath.Abs(rest[1])
			if err != nil {
				fmt.Fprintf(stderr, "error: invalid project directory: %v\n", err)
				return 2
			}
			rest = rest[2:]
		default:
			fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", rest[0])
			return 2
		}
	}

	switch subcommand {
	case "install":
		binaryPath := resolveSelfPath()
		if err := install.InstallClaudeStatusLine(home, binaryPath, scope, projectRoot, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "uninstall":
		if err := install.UninstallClaudeStatusLine(home, scope, projectRoot, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "status":
		// Parse --json flag from original args (outer loop may have consumed it from rest).
		jsonOut := false
		for _, a := range args[1:] {
			if a == "--json" {
				jsonOut = true
			}
		}
		s, err := install.InspectClaudeStatusLine(home, scope, projectRoot)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			enc.Encode(s)
			return 0
		}
		fmt.Fprintf(stdout, "Scope:      %s\n", s.Scope)
		fmt.Fprintf(stdout, "Config:     %s\n", s.SettingsPath)
		fmt.Fprintf(stdout, "Wrapper:    %s\n", s.Wrapper)
		fmt.Fprintf(stdout, "Installed:  %t\n", s.Installed)
		fmt.Fprintf(stdout, "Executable: %t\n", s.Executable)
		fmt.Fprintf(stdout, "Referenced: %t\n", s.Referenced)
		fmt.Fprintf(stdout, "Status: %s\n", s.Status)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %s\n", subcommand)
		return 2
	}
}

// resolveSelfPath returns the absolute path of the running binary when it is
// a real on-disk file (not a test harness), else "" so the wrapper falls
// back to PATH lookup.
//
// Ephemeral paths are rejected: a binary resolved under the Go build cache
// (`go run` artifacts), /tmp, or similar transient locations can be
// garbage-collected at any time, leaving the wrapper silently degraded.
func resolveSelfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if info, err := os.Stat(exe); err != nil || info.IsDir() {
		return ""
	}
	if isEphemeralPath(exe) {
		return ""
	}
	return exe
}

// ephemeralPathMarkers are path prefixes/segments that indicate a binary
// living in transient storage. Wrappers must not pin these paths because the
// files disappear without notice (Go build cache GC, tmp cleaners).
var ephemeralPathMarkers = []string{
	"/go-build/", // `go run` / `go build -o` into the build cache
	"/tmp/",      // temp dirs and mktemp results
	"/var/tmp/",
	"/.cache/go-build/",
}

func isEphemeralPath(path string) bool {
	for _, m := range ephemeralPathMarkers {
		if strings.Contains(path, m) {
			return true
		}
	}
	return false
}
