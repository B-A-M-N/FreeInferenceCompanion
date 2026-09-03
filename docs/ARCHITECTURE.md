# Architecture

FreeInference Companion is a local observer around Claude Code and Codex. The
client continues to send inference traffic directly to FreeInference (or to an
explicitly declared local compatibility proxy); the Companion records
lifecycle data the client already exposes and reads cached provider metadata
when a surface uses it.

## Shape of the system

```text
freeinference CLI (static Go binary)
  ├── local session snapshots, observations, locks, and events
  ├── cached provider metadata with isolated refresh workers
  ├── advisory analysis and normalized renderers
  └── Claude Code / Codex plugin hook runners
```

The default path is deliberately boring:

1. A lifecycle hook receives the client's event.
2. The adapter validates and normalizes supported fields.
3. The state layer writes a bounded local snapshot under a cross-process lock.
4. The renderer reads local state and emits a status line, JSON, or report.

No inference request is needed for that path. An explicitly enabled detached
refresh may update stale model, health, account-usage, or public-status caches;
those workers are isolated from hook latency and share provider-wide safety
controls.

## Local state

State lives under `~/.cache/freeinference-companion/` by default:

```text
providers/<activation-id>/global/  provider health, models, status, breakers, locks
global/                            legacy unnamespaced global state
sessions-index/                    cross-provider session discovery index
sessions/                          per-session snapshots, events, and locks
```

Session directory names are hashes of session IDs. State files are owner-only,
and invalid or unsupported cache artifacts are quarantined before a later
refresh rebuilds them.

## Surfaces

- Claude Code composes `freeinference status --compact` into an existing
  status line and replays any prior statusline output.
- Codex keeps its native footer. Companion state is available through
  `status`, `snapshot --json`, `render`, and diagnostic skills; the plugin does
  not scrape Codex's screen.
- External panels can consume `snapshot --json` or `render --mode line`.

## Design constraints

- Hooks fail open and do local computation only.
- Provider detection gates FreeInference-specific warnings and metadata.
- A loopback Claude route requires `FI_PROXY_UPSTREAM_URL` naming the approved
  Anthropic-compatible FreeInference upstream; `FI_PROVIDER` alone never
  activates anything.
- Missing telemetry remains unknown; it is never fabricated as zero.
- Warnings are advisory and use the client's supported JSON warning channel.
- Refreshes are opt-in, detached, coalesced, spaced, rate-limit-aware, and
  protected by per-worker circuit breakers.
- Monitoring never sends inference probes. `doctor --probe --model <name>` is
  an explicit manual operation and marks its request as synthetic.
- Event logs retain bounded lifecycle categories and sanitized metadata, never
  prompts, responses, paths, credentials, or raw error bodies.

For freshness rules, warning thresholds, account-usage capability handling,
and event retention, see [Observability](OBSERVABILITY.md). For the security
boundary and response limits, see [SECURITY.md](../SECURITY.md).
