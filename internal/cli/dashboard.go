package cli

import (
	"fmt"
	"io"
	"os/exec"
)

const dashboardURL = "https://status.freeinference.org/"

// cmdDashboard implements `fi dashboard`.
func cmdDashboard(stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "Opening: %s\n", dashboardURL)
	if err := openBrowser(dashboardURL); err != nil {
		fmt.Fprintf(stderr, "could not open browser: %v\n", err)
		fmt.Fprintf(stdout, "Visit: %s\n", dashboardURL)
	}
	return 0
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
