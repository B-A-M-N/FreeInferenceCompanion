package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DashboardTarget identifies which dashboard surface to open.
type DashboardTarget string

const (
	DashboardAccount DashboardTarget = "account"
	DashboardStatus  DashboardTarget = "status"
)

const (
	dashboardStatusURL  = "https://status.freeinference.org/"
	dashboardAccountURL = "https://freeinference.org/dashboard"
)

// cmdDashboard implements `fi dashboard`.
//
//	fi dashboard              → user/account dashboard (default)
//	fi dashboard --status     → public service health page
//	fi dashboard --print-url  → print the URL without opening a browser
func cmdDashboard(args []string, stdout, stderr io.Writer) int {
	target := DashboardAccount
	printURL := false

	for _, a := range args {
		switch a {
		case "--status":
			target = DashboardStatus
		case "--print-url":
			printURL = true
		case "--account":
			target = DashboardAccount
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintf(stderr, "unknown flag: %s\n", a)
				return 2
			}
		}
	}

	url := dashboardURLFor(target)

	if printURL {
		fmt.Fprintln(stdout, url)
		return 0
	}

	fmt.Fprintf(stdout, "Opening: %s\n", url)
	if err := openBrowserWithTimeout(url, 5); err != nil {
		fmt.Fprintf(stderr, "could not open browser: %v\n", err)
		fmt.Fprintf(stdout, "Visit: %s\n", url)
		return 1
	}
	return 0
}

func dashboardURLFor(target DashboardTarget) string {
	switch target {
	case DashboardStatus:
		return dashboardStatusURL
	default:
		return dashboardAccountURL
	}
}

// openBrowserWithTimeout opens a URL in the default browser using OS-specific
// commands. It returns an error distinguishing between "no browser found" and
// "URL invalid" (which should never happen for well-formed URLs).
func openBrowserWithTimeout(url string, timeoutSec int) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(context.Background(), "open", url)
	case "windows":
		cmd = exec.CommandContext(context.Background(), "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		// Linux/Unix: try xdg-open first, then firefox, then chromium
		cmd = exec.CommandContext(context.Background(), "xdg-open", url)
	}

	// Kill the process after the timeout to prevent orphaned browsers.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		cmd.Process.Kill()
		return fmt.Errorf("browser opener timed out after %d seconds", timeoutSec)
	}
}
