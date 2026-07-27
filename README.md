# FreeInference Companion

Lightweight observability layer for FreeInference-powered coding-agent sessions. Shows live context metrics, cache performance, model health, and context-pressure warnings — without adding latency, making network calls from hooks, or sending inference probes without explicit consent.

**Companion, not proxy.** No prompt interception, no transcript scraping, no automatic failover, no daemon.

## Quick start

```bash
# Install the CLI
make install

# Run diagnostics
fi doctor

# Browse available models
fi models

# Install Claude Code status line
fi status-line install
```

## Architecture

```
fi CLI (Go, static binary)
  ├── reads/writes ~/.cache/freeinference-companion/
  │   ├── global/          # Provider health, model catalog, circuit breakers
  │   └── sessions/        # Per-session snapshots, event logs, locks
  ├── commands: status, models, doctor, report, dashboard, context, refresh
  └── hook: fi hook claude|codex <event>

Claude Code plugin → fi hook claude <event>
Codex plugin       → fi hook codex <event>
```

### Design principles

- **Status line reads live Claude JSON from stdin + cached health data** — zero network, p95 <10ms target
- **Hooks do local computation only** — no network, p95 <25ms target, always fail open
- **Warnings use JSON `systemMessage`** — never plain stdout, never `additionalContext`, never in model context
- **Background refreshes are detached and opportunistic** — no cron installed, single-flight coalescing, circuit breaker (2→30min backoff)
- **No inference probes for monitoring** — `fi doctor --probe` is manual only, marked `X-Probe: synthetic`

## CLI reference

| Command | Description |
|---------|-------------|
| `fi status [--compact]` | Show current session metrics |
| `fi models [--model <name>]` | List FreeInference models with health |
| `fi doctor [--probe]` | Diagnose connectivity and configuration |
| `fi report [--session <id>]` | Generate sanitized support bundle |
| `fi dashboard` | Open FreeInference status page in browser |
| `fi context [--session <id>]` | Show context pressure information |
| `fi refresh [--force]` | Refresh cached provider metadata |
| `fi hook <client> <event>` | Process a lifecycle hook event (internal) |
| `fi status-line install\|uninstall` | Manage Claude Code status line |

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `FREEINFERENCE_API_KEY` | — | FreeInference API key (starts with `hyi-`) |
| `FREEINFERENCE_BASE_URL` | `https://freeinference.org/v1` | API base URL |
| `FI_HEALTH_URL` | — | Provider health monitoring URL (optional) |
| `FI_CACHE_DIR` | `~/.cache/freeinference-companion` | State cache directory |

## State model

The plugin uses three separate concepts for metrics:

| Source | Authoritative | Description |
|--------|:---:|-------------|
| `live_context` | ✓ | Latest status-line snapshot from the coding client |
| `observed_session_usage` | ✗ | Best-effort local sample aggregation |
| `account_usage` | ✓ | FreeInference account data (null until endpoint exists) |

Missing fields are `null` — never converted to zero. A zero-token field remains zero; a missing field remains null.

## Codex installation

After installing the plugin in Codex:

1. Start Codex
2. Run `/hooks`
3. Review and trust the FreeInference Companion hooks
4. Use `$fi-status`, `$fi-models`, `$fi-doctor`, `$fi-report`, `$fi-dashboard`

## Development

```bash
make build      # Build the fi binary
make test       # Run tests
make lint       # Run go vet
make smoke      # Quick smoke test
make build-all  # Cross-compile for linux/darwin amd64/arm64
```

## Project layout

```
FreeInference/
├── cmd/fi/main.go               # CLI entry point
├── internal/
│   ├── cli/                     # CLI command implementations
│   ├── state/                   # Per-session files, global cache, atomic ops, locks
│   ├── engine/                  # Pressure state machine, cache analysis, dedup
│   ├── api/                     # FreeInference HTTP client (authenticated /v1/models)
│   ├── background/              # Detached refresh, circuit breaker
│   └── adapters/                # Client-specific: claude.go, codex.go
├── pkg/schema/                  # State structs, telemetry contract types
├── plugins/
│   ├── claude-code/             # .claude-plugin/, hooks/, skills/
│   └── codex/                   # .codex-plugin/, hooks/, skills/
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

## License

MIT