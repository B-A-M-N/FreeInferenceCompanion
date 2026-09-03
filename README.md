# FreeInference Companion

Lightweight observability layer for FreeInference-powered coding-agent sessions. Shows live Claude context metrics, rolling cache pattern classification with likely diagnoses, model health, optional account-budget projection, and context-pressure warnings — without proxying inference traffic, adding inference calls, or sending inference probes without explicit consent. Lifecycle hooks record local state; detached metadata refreshes are opt-in, then protected by shared throttling and rate-limit cooldowns. The companion provides conversational management through Claude Code and Codex plugins so users can query supported state naturally.

**Companion, not proxy.** No prompt interception, no transcript scraping, no automatic failover, no daemon.

Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Why this exists

I built this after using FreeInference for inference access during development.
I wanted a small, inspectable companion that helps people understand context
pressure, cache behavior, model availability, and failures from the telemetry
their client already exposes. The goal is to make day-to-day use easier while
keeping the provider's inference path direct and leaving users in control of
any optional refresh or trace-correlation behavior.

This is an independent, unofficial community project. FreeInference did not
ask for it, commission it, direct it, fund it, pay for it, review it, or
approve it. Nothing here implies sponsorship, partnership, endorsement, or
representation by FreeInference or an affiliated organization. HarvardMadSys,
if mentioned as part of the project's motivation, is not a reviewer, approver,
funder, or sponsor.

If FreeInference is useful to you, please support FreeInference directly:
use its [official documentation](https://doc.freeinference.org/), send provider
feedback through its official channels, contribute where the project accepts
contributions, and support its work financially if and where the maintainers
provide that option. This companion is meant to complement the service, not
replace or impersonate it.

## Find your depth

Start here for the short version. The supporting docs keep the technical
details available without turning installation and everyday use into a design
document:

- [Install the release or Codex plugin](docs/INSTALL.md)
- [Understand network behavior and freshness](docs/OBSERVABILITY.md)
- [Read cache diagnoses and heuristic limits](docs/CACHE_DIAGNOSTICS.md)
- [Configure Codex with FreeInference](docs/codex.md)
- [Trace correlation and privacy](docs/TRACING.md)
- [Compatibility and unsupported telemetry](docs/COMPATIBILITY.md)
- [Security and vulnerability reporting](SECURITY.md)
- [Build, test, package, and validate](docs/DEVELOPMENT.md)

## Quick start

For a published release, follow [INSTALL.md](docs/INSTALL.md) to download and
verify the checksummed artifact. From a source checkout, use `make
install` as described in [DEVELOPMENT.md](docs/DEVELOPMENT.md).

```bash
# Source-checkout development install into ~/.local/bin
make install

# Run diagnostics
freeinference doctor

# Check public service health without credentials or session state
freeinference fi-status

# Launch a client with per-process support correlation (optional)
freeinference run claude
freeinference trace

# Browse available models
freeinference models --refresh

# Install the Claude Code status line (composes with any existing one)
freeinference status-line install

# Configure Codex's native model/context footer
freeinference codex-footer install
```

Supported platforms: Linux amd64, Linux arm64, macOS amd64, macOS arm64.
The release binary is fully static (`CGO_ENABLED=0`, verified with `ldd`).

## Architecture

```
freeinference CLI (Go, static binary)
  ├── reads/writes ~/.cache/freeinference-companion/
  │   ├── providers/<activation-id>/global/ # Provider health, models,
  │   │                                      # public status, breakers, locks
  │   ├── global/          # Legacy unnamespaced global state
  │   ├── sessions-index/  # Cross-provider session discovery index
  │   └── sessions/        # Per-session snapshots and advisory locks
  ├── commands: status, sessions, snapshot, render, models, doctor,
  │             report, failures, dashboard, context, cache, fi-status, run, trace,
  │             refresh, status-line, codex-footer
  └── hook: freeinference hook claude-code <event>

Claude Code plugin → scripts/run-hook.sh → freeinference hook claude-code <event>
Codex plugin       → hooks/scripts + skills → local lifecycle recording and diagnostics
```

Plugin hooks resolve the plugin-bundled platform binary first, then a generic
plugin binary, then `freeinference` from `PATH` — and exit 0 no matter what. Hook handlers only record local
state. Session start/end request no upstream work by default. If `FI_AUTO_REFRESH=1` is set,
stale metadata workers share a one-minute request spacing guard and provider-wide cooldown after
a rate limit.

### Where the data shows up

FreeInference Companion is **not a separate TUI**. It composes into the
surfaces the user already has:

- **Claude Code** — the status line command (`freeinference status --compact`) renders
  into the client's existing statusline footer, below the prompt bar. The
  installer (`freeinference status-line install`) preserves and replays stdin to any
  prior statusline, so an existing footer segment keeps working alongside
  ours. Nothing takes over the prompt or the transcript.
- **Codex** — Codex owns a native footer configured with
  `freeinference codex-footer install`; it renders Codex's own model and
  remaining-context items. Companion lifecycle state remains available through
  `freeinference status` / `freeinference snapshot --json` /
  `freeinference render`, while hook telemetry does not scrape that footer.
- **External integrators** — `freeinference snapshot --json` and `freeinference render --mode line`
  are stable contracts. DevDesktop, tmux, and similar panels can subscribe
  without redesigning core state.

### Design principles

- **Status line reads live Claude JSON from stdin + cached health/model-monitor data** — zero network, average-latency target only
- **Hooks do local computation only** — no inference/network work in the hook process or by default in lifecycle operation, average-latency target only, always fail open (exit 0); detached stale metadata refresh requires explicit `FI_AUTO_REFRESH=1`
- **Every session mutation holds a cross-process file lock** — concurrent hooks and status lines coordinate writes; lock contention returns immediately (fail-open) and is counted in `state.DroppedMutations()`
- **Warnings use JSON `systemMessage`** — never plain stdout, never `additionalContext`, never in model context; no warning → no output at all (zero bytes)
- **Surface eligibility is gated by seven checks** — runtime active, client matches, session matches, session active, activation identity matches, observation fresh, provider confirmed FreeInference; any gate failing produces zero bytes
- **Provider detection gates all warnings** — no FreeInference warning or health symbol ever appears in a non-FreeInference session
- **Opt-in background refreshes are detached and coalesced across processes** — stale caches, one-minute shared spacing for authenticated metadata requests, provider-wide cooldown after rate limits, per-endpoint circuit breakers (2→30min backoff), `Retry-After` honored; public model status uses no credentials
- **No inference probes for monitoring** — `freeinference doctor --probe --model <name>` is manual only, marked `X-Probe: synthetic`
- **Advisory warnings, never blocking** — context pressure, projection overflow, cache-low with pattern classification and likely diagnosis, cache TTL expiry; all labeled with confidence, all advisory
- **Schema validation + quarantine** — corrupt or unsupported state files are renamed aside so subsequent writes start fresh; hooks never block on bad state
- **Sanitized structured event log** — per-session `events.jsonl` records bounded failure categories and safe metadata; never prompt text, responses, transcripts, paths, keys, or raw error bodies

## CLI reference

| Command | Description |
|---------|-------------|
| `freeinference status [--compact\|--level summary\|standard\|detailed] [--client <type>] [--session <id>]` | Show session metrics at the requested detail (resolves the current session automatically) |
| `freeinference sessions` | List known sessions from the local index |
| `freeinference snapshot --json [--session <id>]` | Machine-readable normalized view model |
| `freeinference render --mode line\|standard\|expanded [--session <id>]` | Stable summary, standard, or detailed render for panels |
| `freeinference models [--model <name>] [--refresh]` | List FreeInference models |
| `freeinference doctor [--probe --model <name>]` | Diagnose connectivity and configuration |
| `freeinference report [--client <type>] [--session <id>] [--format markdown\|json]` | Generate a sanitized support report (includes budget projection when the provider capability is available) |
| `freeinference failures [--client <type>] [--session <id>] [--model <name>] [--since <duration>] [--json]` | Summarize retained, sanitized turn-failure incidents locally |
| `freeinference dashboard [--status] [--print-url]` | Open FreeInference account dashboard (`--status` for service health page) |
| `freeinference fi-status [--json] [--problems|--down] [--details]` | Fetch public service status without credentials or local session state; all models are shown by default |
| `freeinference run claude|codex [args...]` | Explicitly launch a verified client with a fresh per-process `X-Session-ID` correlation |
| `freeinference trace [setup\|uninstall] [codex] [--json]` | Show trace metadata or manage the reversible Codex header mapping |
| `freeinference context [--session <id>]` | Show context pressure information |
| `freeinference cache [--session <id>]` | Show cache efficiency pattern classification and likely diagnoses |
| `freeinference refresh [--force] [--if-stale --detach] [--worker models\|health\|account-usage\|public-status]` | Refresh cached provider metadata and public model status |
| `freeinference hook <client> <event>` | Process a lifecycle hook event (internal) |
| `freeinference status-line install\|uninstall` | Manage the Claude Code status line |
| `freeinference codex-footer install\|uninstall\|status` | Configure Codex's native model/context footer |

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `FREEINFERENCE_API_KEY` | — | FreeInference API credential |
| `FREEINFERENCE_BASE_URL` | — | Generic provider API URL (API fallback only; not client activation evidence) |
| `ANTHROPIC_AUTH_TOKEN` | — | FreeInference key for Claude Code's Anthropic-compatible endpoint |
| `FI_HEALTH_URL` | — | Provider health monitoring URL (optional) |
| `FI_CACHE_DIR` | `~/.cache/freeinference-companion` | State cache directory |
| `FI_SESSION_ID` | — | Explicit session override for status/context/report |
| `FI_PROVIDER` | — | Set to `freeinference` for attribution metadata only. Does NOT activate the companion. Activation requires a supported endpoint and credential. |
| `FI_AUTO_REFRESH` | `0` | Set to `1` to opt in to stale metadata refreshes from lifecycle hooks |
| `FI_NO_BACKGROUND` | — | Set to `1` to disable detached background refresh after opting in |
| `FI_TRACING` | `1` for `freeinference run` | Enable or disable launch-time trace correlation; it does not affect ordinary client launches |

The companion activates only when the current client has an approved
FreeInference runtime route and its matching credential. Claude Code uses
`ANTHROPIC_BASE_URL` plus `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY`.
Codex activation is established from the selected provider in
`~/.codex/config.toml` and that provider's `env_key`; a generic
`FREEINFERENCE_API_KEY` alone is not evidence that either client is using it.

Trace correlation is explicit and launch-scoped. `freeinference run claude`
uses Claude's `ANTHROPIC_CUSTOM_HEADERS`; `freeinference run codex` uses the
selected provider's `env_http_headers` mapping. It adds only the documented
opaque `X-Session-ID`—never `X-Request-ID`, `X-Probe`, prompts, paths, or
credentials. See [Trace correlation](docs/TRACING.md) for privacy, opt-out,
and compatibility details.

## Configure Claude Code and Codex

FreeInference has two API shapes. Configure the client for the shape it
actually speaks; do not point Codex at the Anthropic path or Claude Code at
the OpenAI-compatible path.

| Client | Runtime endpoint | Credential | Protocol |
|---|---|---|---|
| Claude Code | `https://freeinference.org/anthropic` | `ANTHROPIC_AUTH_TOKEN` | Anthropic-compatible |
| Codex | selected `model_providers.<id>.base_url` | selected provider `env_key` | OpenAI Responses |

### Claude Code

Add the following to `~/.claude/settings.json`, replacing only the key value:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://freeinference.org/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "Free_Inference_API",
    "ANTHROPIC_MODEL": "glm-5.1",
    "ANTHROPIC_SMALL_FAST_MODEL": "glm-5-turbo",
    "API_TIMEOUT_MS": "600000"
  }
}
```

FreeInference's public Anthropic catalog does not include Claude Code's
built-in Anthropic defaults, so set both model variables to public model IDs.
Choose model IDs from the Anthropic route's catalog; availability can differ
from the OpenAI-compatible route.

### Codex

The Codex provider setup, model profiles, model-catalog rules, and companion
plugin behavior are documented separately in [Codex with FreeInference](docs/codex.md).

## What you can observe

The companion keeps live client observations, provider metadata, and account
usage as separate sources. Missing values stay unknown, stale provider data is
labeled stale, and quota projections are suppressed unless the provider has
returned a validated, authoritative response.

It can show context pressure, cache-read patterns, model health, and an
optional budget projection. Cache diagnoses and confidence labels are local
heuristics, not provider-confirmed causes or probabilities. All warnings are
advisory and the status line remains width-aware.

For thresholds, state semantics, reporting levels, event retention, and the
full cache-pattern table, see [Observability](docs/OBSERVABILITY.md) and
[Cache diagnostics](docs/CACHE_DIAGNOSTICS.md). The stable machine-readable
surfaces are `snapshot --json`, `render --mode line`, and `report --format
json`.

## Development

See [DEVELOPMENT.md](docs/DEVELOPMENT.md) for build, test, release, and Codex
plugin validation commands.

## Project layout

```
FreeInferenceCompanion/
├── cmd/fi/                    # Thin entry point (+ binary integration tests)
├── docs/codex.md              # Codex provider, profiles, and companion guide
├── internal/
│   ├── cli/                   # Command implementations (exit codes, no os.Exit)
│   ├── state/                 # Snapshots, global cache, locks, session index
│   ├── engine/                # Pressure state machine, cache analysis, attribution,
│   │                          # cache TTL warnings, budget projection
│   ├── api/                   # FreeInference HTTP client (bounded, sanitized)
│   ├── background/            # Detached refresh workers, circuit breakers
│   ├── adapters/              # Client-specific: claude.go, codex.go, provider.go
│   ├── install/               # Status-line installer (composing, reversible)
│   └── render/                # Normalized view model → line/expanded/JSON,
│                              # width-aware footer, surface eligibility
├── pkg/schema/                # State structs, telemetry contract types
├── plugins/
│   ├── claude-code/           # .claude-plugin/, hooks/, scripts/, skills/
│   └── codex/                 # .codex-plugin/, hooks/, scripts/, skills/
└── Makefile
```

## What it does not do

- No local API proxy
- No automatic model failover or switching
- No standalone web dashboard
- No prompt or response telemetry
- No cloud synchronization
- No automatic compaction
- No full benchmarking
- No conversation storage
- No inference probes during normal operation
- No cron or systemd installation
- No blocking mode in v1 (all states advisory)

## Security model

The companion keeps credentials in memory for individual requests, never
persists API keys, and writes only bounded, sanitized local metadata. State
files are owner-only; prompts, responses, transcript paths, raw headers, and
raw error bodies are not retained. See [SECURITY.md](SECURITY.md) for the
threat model, response bounds, trace-correlation tradeoff, and private
vulnerability-reporting process.


## License

MIT
