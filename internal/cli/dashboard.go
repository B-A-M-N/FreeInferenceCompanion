package cli

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
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
	if err := openBrowser(url); err != nil {
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

// openBrowser opens a URL in the default browser.
func openBrowser(url string) error {
	for _, cmd := range []string{"xdg-open", "open"} {
		if err := exec.Command(cmd, url).Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no browser opener found")
}
