package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/state"
)

const companionDisabledMarker = ".companion-disabled"

// companionConfigDir returns the companion configuration directory.
func companionConfigDir() (string, error) {
	if d := os.Getenv("FI_CONFIG_DIR"); d != "" {
		return d, nil
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg != "" {
		return filepath.Join(xdg, "freeinference-companion"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "freeinference-companion"), nil
}

// cmdCompanion implements `freeinference companion status|enable|disable`.
func cmdCompanion(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: freeinference companion status|enable|disable [--json]")
		return 2
	}

	subcommand := args[0]
	jsonOut := false
	for _, a := range args[1:] {
		if a == "--json" {
			jsonOut = true
		} else if strings.HasPrefix(a, "--") {
			fmt.Fprintf(stderr, "unknown flag: %s\n", a)
			return 2
		}
	}

	switch subcommand {
	case "status":
		disabled := isCompanionDisabled()
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			enc.Encode(map[string]bool{"disabled": disabled, "active": !disabled && os.Getenv("FI_DISABLED") != "1"})
			return 0
		}
		if disabled {
			fmt.Fprintln(stdout, "Companion: disabled")
		} else {
			fmt.Fprintln(stdout, "Companion: active")
		}
		return 0

	case "enable":
		removeCompanionMarker()
		os.Unsetenv("FI_DISABLED")
		if jsonOut {
			fmt.Fprintln(stdout, `{"enabled":true}`)
			return 0
		}
		fmt.Fprintln(stdout, "Companion enabled")
		return 0

	case "disable":
		if err := createCompanionMarker(); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if jsonOut {
			fmt.Fprintln(stdout, `{"disabled":true}`)
			return 0
		}
		fmt.Fprintln(stdout, "Companion disabled (marker file created; set FI_DISABLED=1 in your shell for immediate effect)")
		return 0

	default:
		fmt.Fprintf(stderr, "unknown companion subcommand: %s\n", subcommand)
		return 2
	}
}

// markerPath returns the path to the companion disabled marker file.
func markerPath() (string, error) {
	dir, err := companionConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, companionDisabledMarker), nil
}

// isCompanionDisabled checks both the marker file and the env var.
func isCompanionDisabled() bool {
	if p, err := markerPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return os.Getenv("FI_DISABLED") == "1"
}

func createCompanionMarker() error {
	dir, err := companionConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	p := filepath.Join(dir, companionDisabledMarker)
	return os.WriteFile(p, []byte("disabled"), 0644)
}

func removeCompanionMarker() {
	if p, err := markerPath(); err == nil {
		os.Remove(p)
	}
}