# FreeInference Companion

Lightweight observability layer for FreeInference-powered coding-agent sessions. Shows live context metrics, rolling cache performance, model health, and context-pressure warnings — without adding latency, making network calls from hooks, or sending inference probes without explicit consent.

**Companion, not proxy.** No prompt interception, no transcript scraping, no automatic failover, no daemon.

Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Quick start

```bash
# Install the CLI into ~/.local/bin
make install

# Run diagnostics
fi doctor

# Browse available models
fi models --refresh

# Install the Claude Code status line (composes with any existing one)
fi status-line install
```

Supported platforms: Linux amd64, Linux arm64, macOS amd64, macOS arm64.
The release binary is fully static (`CGO_ENABLED=0`, verified with `ldd`).

## Architecture

```
fi CLI (Go, static binary)
  ├── reads/writes ~/.cache/freeinference-companion/
  │   ├── global/          # Provider health, model catalog, circuit breakers,
  │   │                    # session index, refresh locks
  │   └── sessions/        # Per-session snapshots and advisory locks
  ├── commands: status, sessions, snapshot, render, models, doctor,
  │             report, dashboard, context, refresh, status-line
  └── hook: fi hook claude-code|codex <event>

Claude Code plugin → scripts/run-hook.sh → fi hook claude-code <event>
Codex plugin       → scripts/run-hook.sh → fi hook codex <event>
```

Plugin hooks resolve the `fi` binary from `PATH`, the plugin-bundled `bin/fi`,
or `~/.local/bin/fi` — and exit 0 no matter what.

### Where the data shows up

FreeInference Companion is **not a separate TUI**. It composes into the
surfaces the user already has:

- **Claude Code** — the status line command (`fi status --compact`) renders
  into the client's existing statusline footer, below the prompt bar. The
  installer (`fi status-line install`) preserves and replays stdin to any
  prior statusline, so an existing footer segment keeps working alongside
  ours. Nothing takes over the prompt or the transcript.
- **Codex** — Codex has no arbitrary script-backed statusline in the same
  sense; we expose the data through `fi status` / `fi snapshot --json` /
  `fi render` for whoever the user wires in (their shell prompt, DevDesktop,
  tmux status bar, etc.).
- **External integrators** — `fi snapshot --json` and `fi render --mode line`
  are stable contracts. DevDesktop, tmux, and similar panels can subscribe
  without redesigning core state.

### Design principles

- **Status line reads live Claude JSON from stdin + cached health data** — zero network, p95 <10ms target
- **Hooks do local computation only** — no network, p95 <25ms target, always fail open (exit 0)
- **Every session mutation holds a cross-process file lock** — concurrent hooks and status lines coordinate writes; lock contention returns immediately (fail-open) and is counted in `state.DroppedMutations()`
- **Warnings use JSON `systemMessage`** — never plain stdout, never `additionalContext`, never in model context; no warning → no output at all
- **Provider detection gates all warnings** — no FreeInference warning or health symbol ever appears in a non-FreeInference session
- **Background refreshes are detached and coalesced across processes** — file-lock single-flight, per-endpoint circuit breakers (2→30min backoff), `Retry-After` honored
- **No inference probes for monitoring** — `fi doctor --probe --model <name>` is manual only, marked `X-Probe: synthetic`
- **Advisory projection warning** — before each prompt, the hook estimates the next request's output reserve from local data and warns if it would be inadequate (confidence labeled, never blocks)
- **Schema validation + quarantine** — corrupt or unsupported state files are renamed aside so subsequent writes start fresh; hooks never block on bad state
- **Sanitized structured event log** — per-session `events.jsonl` records only event types and short categories; never prompt text, responses, transcripts, paths, keys, or raw error bodies

## CLI reference

| Command | Description |
|---------|-------------|
| `fi status [--compact] [--client <type>] [--session <id>]` | Show session metrics (resolves the current session automatically) |
| `fi sessions` | List known sessions from the local index |
| `fi snapshot --json [--session <id>]` | Machine-readable normalized view model |
| `fi render --mode line\|expanded [--session <id>]` | Stable line/expanded render for panels |
| `fi models [--model <name>] [--refresh]` | List FreeInference models |
| `fi doctor [--probe --model <name>]` | Diagnose connectivity and configuration |
| `fi report [--client <type>] [--session <id>] [--format markdown\|json]` | Generate a sanitized support report |
| `fi dashboard` | Open FreeInference status page in browser |
| `fi context [--session <id>]` | Show context pressure information |
| `fi refresh [--force] [--if-stale --detach] [--worker models\|health]` | Refresh cached provider metadata |
| `fi hook <client> <event>` | Process a lifecycle hook event (internal) |
| `fi status-line install\|uninstall` | Manage the Claude Code status line |

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `FREEINFERENCE_API_KEY` | — | FreeInference API key (starts with `hyi-`) |
| `FREEINFERENCE_BASE_URL` | `https://freeinference.org/v1` | API base URL |
| `FI_HEALTH_URL` | — | Provider health monitoring URL (optional) |
| `FI_CACHE_DIR` | `~/.cache/freeinference-companion` | State cache directory |
| `FI_SESSION_ID` | — | Explicit session override for status/context/report |
| `FI_PROVIDER` | — | Set to `freeinference` to force provider detection |
| `FI_NO_BACKGROUND` | — | Set to `1` to disable detached background refresh |

Provider detection order: `FI_PROVIDER` → `FREEINFERENCE_BASE_URL` →
`ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` pointing at a FreeInference host →
`FREEINFERENCE_API_KEY` with no conflicting provider configuration.

## State model

The plugin uses three separate concepts for metrics:

| Source | Authoritative | Description |
|--------|:---:|-------------|
| `live_context` | ✓ | Latest status-line snapshot from the coding client (session totals kept separate from latest-request usage) |
| `usage_observations` | ✗ | Rolling window of up to 20 unique request samples (fingerprint-deduplicated); feeds the 5-sample cache analysis |
| `account_usage` | ✓ | FreeInference account data (null until an endpoint exists) |

Missing fields are `null` — never converted to zero. A zero-token field remains zero; a missing field remains null.

Cache-low warnings qualify only when all hold: 3+ unique observations, ≥50K
active context, read share <20% for 3 sequential observations, confirmed
FreeInference provider, and a 30-minute cooldown. They resolve after 3
sequential observations above 40%.

Projection warnings qualify when active context is at least 60% of the
model's window and the projected next request (active + estimated prompt +
tool overhead + safety margin) would leave less than the configured output
reserve (default 16,000 tokens). Confidence is labeled `low` or `medium` —
never `high` in v0.1.0 because the companion does not see the full request
body the client sends.

### Sanitized event log

Each session has a bounded `events.jsonl` recording only lifecycle event
types (`session_started`, `status_observed`, `prompt_submitted`,
`turn_stopped`, `turn_failed`, `compaction_started`, `compaction_completed`,
`session_ended`, `warning_shown`, `warning_resolved`) and short sanitized
details. Rotation kicks in past 256 KiB or 1,000 events per session.
Sessions older than 30 days are cleaned up opportunistically by
`CleanupStaleSessions`.

## Codex installation

The Codex plugin bundles skills and optional lifecycle hooks using Codex's
native plugin layout: `.codex-plugin/plugin.json` (manifest), `hooks/hooks.json`
(lifecycle hooks), and `skills/` (skills). Codex automatically discovers
`hooks/hooks.json` from an enabled plugin — no separate installer is needed
and nothing is written to `~/.codex/hooks/`.

After installing the plugin in Codex:

1. Start Codex
2. Run `/hooks`
3. Review and trust the FreeInference Companion hooks

Plugin hooks do not run merely because the plugin is installed. Codex requires
you to review and trust the exact hook definition; changed hooks require review
again. Until you trust them, command hooks are skipped — but the skills remain
fully usable, so `$fi-status`, `$fi-models`, `$fi-doctor`, `$fi-report`, and
`$fi-dashboard` work regardless of hook trust state.

Once trusted, the hooks record session lifecycle events (start/end, prompt
submissions, turns, compaction) to the same local cache Claude Code uses, and
the skills read that cache.

Codex exposes no live token telemetry, so context and cache metrics stay
`unknown` for Codex sessions — they are never fabricated. The hooks fire on
lifecycle events only; they do not intercept prompts (only the byte length is
read, never the contents) and they add no inference traffic.

## Development

```bash
make build      # Static build into build/fi (ldd-verified)
make test       # Run tests
make test-race  # Run tests with the race detector
make vet        # Run go vet
make fmt-check  # Verify gofmt cleanliness
make bench      # Run performance benchmarks (status p95<10ms, hook p95<25ms targets)
make check      # fmt + vet + test + race + plugin validation + git diff --check
make release    # Cross-compile all platforms + checksums
make smoke      # Quick smoke test
```

## Project layout

```
FreeInference/
├── cmd/fi/                    # Thin entry point (+ binary integration tests)
├── internal/
│   ├── cli/                   # Command implementations (exit codes, no os.Exit)
│   ├── state/                 # Snapshots, global cache, locks, session index
│   ├── engine/                # Pressure state machine, rolling cache analysis
│   ├── api/                   # FreeInference HTTP client (bounded, sanitized)
│   ├── background/            # Detached refresh workers, circuit breakers
│   ├── adapters/              # Client-specific: claude.go, codex.go, provider.go
│   ├── install/               # Status-line installer (composing, reversible)
│   └── render/                # Normalized view model → line/expanded/JSON
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

The companion handles API keys and (eventually) account-usage data. The
security model is layered and intentional.

**Credential handling:**
- The FreeInference API key is read from the environment at request time and
  lives only in the in-process `api.Client.APIKey` field for the duration of
  a request.
- It is **never** persisted to disk by any code path.
- It is sent only in `Authorization: Bearer` headers to the configured
  FreeInference endpoint.
- It does not appear in any `Snapshot`, `Event`, `GlobalState`, report,
  doctor output, log line, or error message.

**Defense in depth (output):**
- **Allowlist construction** — reports and account-usage renders are built
  only from explicitly-named fields. Unknown upstream fields are silently
  dropped, never redacted-after-the-fact. A future endpoint adding a
  `billing_email` or `api_key_hint` field will not leak.
- **Pattern-based redaction** (`internal/secure`) — any string leaving the
  process through state, an event, or a report passes through a redactor
  that recognizes key shapes (`hyi-*`, `sk-*`, `Bearer *`,
  `*_API_KEY=...`, labeled JSON secret fields).
- **Identifier obfuscation** — session IDs are SHA-256-hashed for the
  on-disk directory name; `secure.ShortHash` / `MaskSessionID` utilities
  are available for any future display context that does not need the full ID.

**Defense in depth (filesystem):**
- All state files are `0600` (owner read/write only).
- All state directories are `0700`.
- The session directory name is a SHA-256 of the session ID, preventing
  path-traversal and hiding the raw ID from a casual `ls`.
- Path-shaped client inputs (`transcript_path`, `cwd`, `current_dir`,
  `project_dir`) are never copied into persisted state.

**Response bounds:**
- Catalog response bounded at 2 MiB, health at 1 MiB.
- Error bodies bounded at 64 KiB and run through the redactor before
  entering our error type.
- The synthetic inference probe body is bounded at 1 MiB.

**Why no encryption-at-rest:** Local state is already `0600` and contains no
secrets. Encrypting it would require a key stored on the same machine (env
var, keyfile, or OS keystore); an attacker who can read the cache directory
can almost certainly read the key too. We treat OS-level file permissions as
the boundary and skip encryption-at-rest as security theatre. When account
usage lands, the same model applies: the persisted fields are numeric
quotas and timestamps — not secrets — and the API key still never touches
disk. An OS keystore integration (macOS Keychain / Linux secret-service)
would be the meaningful next step if we ever needed to persist a refresh
token, which v0.1.0 does not.

**Verifying the model:**
`TestSecretNeverPersistsOrRenders` in `cmd/fi/security_test.go` walks every
persisted file and every output path (status, snapshot, render, report,
events, doctor) and fails if any secret-shaped string appears. It is the
regression guard for the security model.

## License

MIT
