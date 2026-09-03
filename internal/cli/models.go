package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/background"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// cmdModels implements `freeinference models`.
func cmdModels(paths state.Paths, args []string, stdout, stderr io.Writer) int {
	var modelName string
	var forceRefresh bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--model":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage error: --model requires a value")
				return 2
			}
			i++
			modelName = args[i]
		case "--refresh":
			forceRefresh = true
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintf(stderr, "usage error: unknown flag %q\n", a)
				return 2
			}
			fmt.Fprintf(stderr, "usage error: unexpected argument %q\n", a)
			return 2
		}
	}

	gs := loadGlobal(paths)

	if forceRefresh {
		client, err := newAPIClient()
		if err != nil {
			fmt.Fprintln(stderr, "error: "+endpointFailDetail(err))
			return 1
		}
		refresher := background.NewRefresher(client, paths, os.Getenv("FI_HEALTH_URL"))
		result := refresher.ForceWorkerRefresh(background.WorkerModels)
		if result.Error != "" {
			fmt.Fprintf(stderr, "refresh error: %s\n", result.Error)
			return 1
		}
		gs = loadGlobal(paths)
	}

	if gs.Models == nil || len(gs.Models.Models) == 0 {
		fmt.Fprintln(stdout, "No model data available. Use --refresh to fetch.")
		return 0
	}

	if modelName != "" {
		for _, m := range gs.Models.Models {
			if m.ID == modelName || m.Name == modelName {
				printModelDetail(stdout, m)
				return 0
			}
		}
		fmt.Fprintf(stdout, "Model '%s' not found in catalog.\n", secure.SafeField(modelName))
		return 1
	}

	fmt.Fprintf(stdout, "FreeInference Models (cached at %s):\n", gs.Models.FetchedAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "%-24s %-12s %-12s %s\n", "MODEL", "CONTEXT", "MAX OUTPUT", "FEATURES")
	fmt.Fprintln(stdout, repeat("-", 82))
	for _, m := range gs.Models.Models {
		features := secure.SafeField(strings.Join(m.Features, ","))
		if len(features) > 30 {
			features = features[:30] + "..."
		}
		fmt.Fprintf(stdout, "%-24s %-12s %-12s %s\n",
			secure.SafeField(m.ID),
			formatTokenCount(int64(m.ContextLength)),
			formatTokenCount(int64(m.MaxOutputLength)),
			features,
		)
	}
	return 0
}

func printModelDetail(stdout io.Writer, m schema.CatalogModel) {
	fmt.Fprintf(stdout, "Model: %s\n", secure.SafeField(m.ID))
	if m.Name != "" {
		fmt.Fprintf(stdout, "Name:  %s\n", secure.SafeField(m.Name))
	}
	fmt.Fprintf(stdout, "Context Window: %s\n", formatTokenCount(int64(m.ContextLength)))
	fmt.Fprintf(stdout, "Max Output:     %s\n", formatTokenCount(int64(m.MaxOutputLength)))
	access := m.AccessState
	if access == schema.AccessUnknown {
		access = "unknown (catalog presence does not confirm access)"
	}
	fmt.Fprintf(stdout, "Access:         %s\n", access)
	if len(m.Features) > 0 {
		fmt.Fprintf(stdout, "Features:       %s\n", secure.SafeField(strings.Join(m.Features, ", ")))
	}
	if len(m.Pricing) > 0 {
		fmt.Fprintln(stdout, "Pricing (per MTok):")
		for k, v := range m.Pricing {
			fmt.Fprintf(stdout, "  %s: $%s\n", secure.SafeField(k), secure.SafeField(v))
		}
	}
}
