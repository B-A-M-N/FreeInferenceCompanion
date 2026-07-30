package cli

import (
	"encoding/json"
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
//	freeinference refresh                        synchronous if-stale refresh
//	freeinference refresh --force                synchronous refresh ignoring staleness
//	freeinference refresh --if-stale --detach    spawn detached workers for stale caches
//	freeinference refresh --worker models|health|account-usage single worker under a process lock
func cmdRefresh(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	force := false
	ifStale := false
	detach := false
	worker := ""
	jsonOut := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--force":
			force = true
		case "--json":
			jsonOut = true
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

	// Validate mutually exclusive modes.
	modeCount := 0
	if force {
		modeCount++
	}
	if ifStale {
		modeCount++
	}
	if detach {
		modeCount++
	}
	if worker != "" {
		modeCount++
	}
	if modeCount > 1 {
		fmt.Fprintln(stderr, "usage error: --force, --if-stale, --detach, and --worker are mutually exclusive")
		return 2
	}

	client, err := newAPIClient()
	if err != nil {
		fmt.Fprintln(stderr, "error: "+endpointFailDetail(err))
		return 1
	}
	refresher := background.NewRefresher(client, paths, os.Getenv("FI_HEALTH_URL"))

	// Worker mode: do the actual fetch under a cross-process lock.
	if worker != "" {
		result := refresher.WorkerRefresh(worker)
		if result.Skipped {
			if result.SkipReason == "unknown worker" {
				fmt.Fprintf(stderr, "error: unknown worker %q (valid: models, health, account-usage)\n", worker)
				return 2
			}
			// Another worker is running — not an error, but report it.
			fmt.Fprintf(stdout, "Worker %s: %s.\n", worker, result.SkipReason)
			return 0
		}
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

	if jsonOut {
		r := map[string]any{
			"models_refreshed": result.ModelsRefreshed,
			"health_refreshed": result.HealthRefreshed,
		}
		if result.Error != "" {
			r["error"] = result.Error
		}
		out, _ := json.Marshal(r)
		fmt.Fprintln(stdout, string(out))
		if result.Error != "" {
			return 1
		}
		return 0
	}

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
