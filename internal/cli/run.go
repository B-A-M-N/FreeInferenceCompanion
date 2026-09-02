package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

// cmdRun is the preferred Companion launch boundary. It does not proxy or
// inspect client traffic; it prepares the client's documented configuration,
// then replaces itself with the requested client process.
func cmdRun(args []string, _ io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printRunUsage(stderr)
		if len(args) > 0 {
			return 0
		}
		return 2
	}
	target, ok := launchTargetFor(args[0])
	if !ok {
		fmt.Fprintf(stderr, "usage error: run target must be claude or codex, got %q\n", args[0])
		return 2
	}
	targetPath, err := exec.LookPath(target.Executable)
	if err != nil {
		fmt.Fprintf(stderr, "error: cannot find %q: %v\n", target.Executable, err)
		return 1
	}

	prepared, err := prepareLaunch(target.ClientKind, os.Environ(), time.Now().UTC(), codexProfileArg(args[1:]))
	if err != nil {
		// Trace preparation is intentionally fail-open. The target still runs
		// with its original environment if configuration is malformed or a
		// private receipt cannot be created.
		fmt.Fprintf(stderr, "warning: trace correlation unavailable; launching %s without trace (%s)\n",
			target.CLIName, tracePreparationReason(err))
		prepared = launchPreparation{Env: os.Environ()}
	}
	if prepared.ReceiptPath != "" {
		defer func() {
			// Normal SessionStart consumption removes the receipt. This cleanup
			// covers an exec failure and test doubles.
			tracing.RemoveLaunchReceipt(prepared.ReceiptPath)
		}()
	}

	argv := append([]string{target.Executable}, args[1:]...)
	if err := execTarget(targetPath, argv, prepared.Env); err != nil {
		fmt.Fprintf(stderr, "error: failed to launch %s: %v\n", target.CLIName, err)
		return 1
	}
	return 0
}

func tracePreparationReason(err error) string {
	if err == nil {
		return "preparation failed"
	}
	// Error details are reduced to one bounded, redacted line. In particular,
	// do not echo config paths, headers, credentials, or provider bodies.
	reason := secure.Redact(secure.SanitizeField(err.Error()))
	if reason == "" || reason == secure.RedactedPlaceholder {
		return "preparation failed"
	}
	if len(reason) > 160 {
		reason = reason[:160] + "..."
	}
	return reason
}

type launchTarget struct {
	CLIName    string
	ClientKind string
	Executable string
}

func launchTargetFor(name string) (launchTarget, bool) {
	switch name {
	case "claude":
		return launchTarget{CLIName: "claude", ClientKind: schema.ClientClaudeCode, Executable: "claude"}, true
	case "codex":
		return launchTarget{CLIName: "codex", ClientKind: schema.ClientCodex, Executable: "codex"}, true
	default:
		return launchTarget{}, false
	}
}

type launchPreparation struct {
	Env         []string
	ReceiptPath string
	Trace       *schema.TraceInfo
}

// prepareLaunch is kept separate from process replacement so the safety and
// gating contract can be exhaustively tested without starting a real client.
func prepareLaunch(client string, env []string, startedAt time.Time, selectedProfile ...string) (launchPreparation, error) {
	if client != schema.ClientClaudeCode && client != schema.ClientCodex {
		return launchPreparation{Env: env}, nil
	}

	mgr, err := config.NewManager()
	if err != nil {
		return launchPreparation{Env: env}, err
	}
	eff, err := mgr.Resolve()
	if err != nil || !eff.Tracing.Enabled.Valid || !eff.Tracing.Enabled.Value {
		return launchPreparation{Env: env}, err
	}

	var (
		activation runtime.Activation
		traceID    string
		source     tracing.Source
		newEnv     = env
	)
	metadata, metadataErr := tracing.NewCorrelationMetadata(client, version.Version)
	if metadataErr != nil {
		return launchPreparation{Env: env}, metadataErr
	}
	switch client {
	case schema.ClientClaudeCode:
		activation = runtime.EvaluateForClient(runtime.ClientClaudeCode)
		if !activation.Active {
			return launchPreparation{Env: env}, nil
		}
		generated, genErr := tracing.GenerateTraceID()
		if genErr != nil {
			return launchPreparation{Env: env}, genErr
		}
		composed, id, composedSource, composeErr := tracing.ComposeClaudeCustomHeadersWithMetadata(lookupEnv(env, "ANTHROPIC_CUSTOM_HEADERS"), generated, metadata)
		if composeErr != nil || id == "" || composedSource == tracing.SourceNone {
			return launchPreparation{Env: env}, composeErr
		}
		traceID, source = id, composedSource
		newEnv = tracing.ReplaceEnv(env, map[string]string{
			"ANTHROPIC_CUSTOM_HEADERS": composed,
		})
	case schema.ClientCodex:
		profile := ""
		if len(selectedProfile) > 0 {
			profile = selectedProfile[0]
		}
		if profile != "" {
			evidence, resolveErr := runtime.ResolveCodexProviderConfigurationForProfile(profile)
			if resolveErr != nil {
				return launchPreparation{Env: env}, resolveErr
			}
			activation = runtime.EvaluateForClient(runtime.ClientCodex, evidence)
		} else {
			activation = runtime.EvaluateForClient(runtime.ClientCodex)
		}
		if !activation.Active || activation.Evidence.ProviderID == "" {
			return launchPreparation{Env: env}, nil
		}
		path, pathErr := runtime.CodexConfigPath()
		if pathErr != nil {
			return launchPreparation{Env: env}, pathErr
		}
		mapping, mappingErr := runtime.InspectCodexTraceHeaders(path, activation.Evidence.ProviderID)
		if mappingErr != nil {
			return launchPreparation{Env: env}, mappingErr
		}
		if len(mapping.Conflicts) > 0 {
			return launchPreparation{Env: env}, fmt.Errorf("Codex trace mapping conflicts with Companion metadata")
		}
		if !mapping.Ready {
			return launchPreparation{Env: env}, fmt.Errorf("Codex trace setup required; run freeinference trace setup --client codex")
		}
		traceID, err = tracing.GenerateTraceID()
		if err != nil {
			return launchPreparation{Env: env}, err
		}
		source = tracing.SourceCompanionGenerated
	}

	if !tracing.ValidateTraceID(traceID) || !sourceValid(source) {
		return launchPreparation{Env: env}, fmt.Errorf("trace preparation produced an invalid correlation")
	}
	receipt := tracing.LaunchReceipt{
		TraceID:        traceID,
		Client:         client,
		Provider:       schema.ProviderFreeInference,
		EndpointOrigin: activation.Origin,
		StartedAt:      startedAt.UTC(),
		HeaderName:     tracing.SessionHeader,
		Source:         source,
	}
	receiptPath, err := tracing.WriteLaunchReceipt(receipt)
	if err != nil {
		return launchPreparation{Env: env}, err
	}
	newEnv = tracing.ReplaceEnv(newEnv, map[string]string{
		tracing.TraceSessionEnv:          traceID,
		tracing.TraceManagedEnv:          "1",
		tracing.TraceSourceEnv:           string(source),
		tracing.TraceClientEnv:           client,
		tracing.TraceCompanionVersionEnv: metadata.CompanionVersion,
		tracing.TraceWorkloadEnv:         metadata.Workload,
		tracing.TraceReceiptEnv:          receiptPath,
	})
	if client == schema.ClientCodex {
		if profile := codexProfileArgFromPreparation(selectedProfile); profile != "" {
			newEnv = tracing.ReplaceEnv(newEnv, map[string]string{"CODEX_PROFILE": profile})
		}
	}
	return launchPreparation{
		Env:         newEnv,
		ReceiptPath: receiptPath,
		Trace: &schema.TraceInfo{
			Enabled:        true,
			SessionID:      traceID,
			Source:         string(source),
			StartedAt:      startedAt.UTC(),
			Provider:       schema.ProviderFreeInference,
			Client:         client,
			Header:         tracing.SessionHeader,
			EndpointOrigin: activation.Origin,
		},
	}, nil
}

func codexProfileArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--profile" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], "--profile=") {
			return strings.TrimPrefix(args[i], "--profile=")
		}
	}
	return ""
}

func codexProfileArgFromPreparation(profiles []string) string {
	if len(profiles) == 0 {
		return ""
	}
	return profiles[0]
}

func sourceValid(source tracing.Source) bool {
	return source == tracing.SourceCompanionGenerated || source == tracing.SourceExistingHeader
}

func lookupEnv(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

var execTarget = syscall.Exec

func printRunUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: freeinference run claude|codex [args...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Launch a client with a fresh per-process FreeInference trace correlation when its selected runtime is verified.")
}
