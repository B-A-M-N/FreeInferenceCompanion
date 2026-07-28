package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/background"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
)

// cmdRefresh implements:
//
//	fi refresh                        synchronous if-stale refresh
//	fi refresh --force                synchronous refresh ignoring staleness
//	fi refresh --if-stale --detach    spawn detached workers for stale caches
//	fi refresh --worker models|health single worker under a process lock
func cmdRefresh(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	force := false
	ifStale := false
	detach := false
	worker := ""

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--force":
			force = true
		case "--if-stale":
			ifStale = true
		case "--detach":
			detach = true
		case "--worker":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --worker requires a value")
				return 2
			}
			i++
			worker = args[i]
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintf(stderr, "usage error: unknown flag %q\n", a)
				return 2
			}
			fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", a)
			return 2
		}
	}

	client := newAPIClient()
	if client == nil {
		fmt.Fprintln(stderr, "error: FREEINFERENCE_BASE_URL is invalid (must be HTTPS, no userinfo)")
		return 1
	}
	refresher := background.NewRefresher(client, paths, os.Getenv("FI_HEALTH_URL"))

	// Worker mode: do the actual fetch under a cross-process lock.
	if worker != "" {
		result := refresher.WorkerRefresh(worker)
		if result.ModelsRefreshed {
			fmt.Fprintln(stdout, "Models refreshed.")
		}
		if result.HealthRefreshed {
			fmt.Fprintln(stdout, "Health refreshed.")
		}
		if result.Error != "" {
			fmt.Fprintf(stderr, "refresh error: %s\n", result.Error)
			return 1
		}
		return 0
	}

	// Detached mode: spawn workers and return immediately.
	if detach {
		stale := background.StaleWorkers(paths, os.Getenv("FI_HEALTH_URL"))
		if !ifStale {
			stale = []string{background.WorkerModels}
			if os.Getenv("FI_HEALTH_URL") != "" {
				stale = append(stale, background.WorkerHealth)
			}
		}
		if len(stale) == 0 {
			return 0
		}
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(stderr, "detach error: %v\n", err)
			return 1
		}
		if err := background.SpawnDetachedWorkers(exe, stale); err != nil {
			fmt.Fprintf(stderr, "detach error: %v\n", err)
			return 1
		}
		return 0
	}

	// Synchronous modes.
	var result *background.RefreshResult
	if force {
		result = refresher.ForceRefresh()
	} else {
		result = refresher.RefreshIfStale()
	}

	// Opportunistic session GC: prune session directories older than the
	// retention window. Best-effort — failures here do not affect the
	// refresh result.
	_ = state.CleanupStaleSessions(paths, time.Now())

	if result.ModelsRefreshed {
		fmt.Fprintln(stdout, "Models refreshed.")
	}
	if result.HealthRefreshed {
		fmt.Fprintln(stdout, "Health refreshed.")
	}
	if result.Error != "" {
		fmt.Fprintf(stderr, "Warning: %s\n", result.Error)
		return 1
	}
	return 0
}
