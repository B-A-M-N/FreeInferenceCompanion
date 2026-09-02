# FreeInference Companion

Lightweight observability layer for FreeInference-powered coding-agent sessions. Shows live Claude context metrics, rolling cache pattern classification with likely diagnoses, model health, optional account-budget projection, and context-pressure warnings — without adding latency, making network calls from hooks, or sending inference probes without explicit consent. The companion provides conversational management through Claude Code and a Codex skill package so users can query supported state naturally.

**Companion, not proxy.** No prompt interception, no transcript scraping, no automatic failover, no daemon.

Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Quick start

```bash
# Install the CLI into ~/.local/bin
make install

# Run diagnostics
freeinference doctor

# Check public service health without credentials or session state
freeinference fi-status

# Browse available models
freeinference models --refresh

# Install the Claude Code status line (composes with any existing one)
freeinference status-line install
```

Supported platforms: Linux amd64, Linux arm64, macOS amd64, macOS arm64.
The release binary is fully static (`CGO_ENABLED=0`, verified with `ldd`).

## Architecture

```
freeinference CLI (Go, static binary)
  ├── reads/writes ~/.cache/freeinference-companion/
  │   ├── global/          # Provider health, model catalog, circuit breakers,
  │   │                    # session index, refresh locks
  │   └── sessions/        # Per-session snapshots and advisory locks
  ├── commands: status, sessions, snapshot, render, models, doctor,
  │             report, dashboard, context, cache, fi-status, refresh, status-line
  └── hook: freeinference hook claude-code <event>

Claude Code plugin → scripts/run-hook.sh → freeinference hook claude-code <event>
Codex plugin       → skills only → user-requested CLI diagnostics
```

Plugin hooks resolve the `freeinference` binary from `PATH`, the plugin-bundled `bin/freeinference`,
or `~/.local/bin/freeinference` — and exit 0 no matter what.

### Where the data shows up

FreeInference Companion is **not a separate TUI**. It composes into the
surfaces the user already has:

- **Claude Code** — the status line command (`freeinference status --compact`) renders
  into the client's existing statusline footer, below the prompt bar. The
  installer (`freeinference status-line install`) preserves and replays stdin to any
  prior statusline, so an existing footer segment keeps working alongside
  ours. Nothing takes over the prompt or the transcript.
- **Codex** — Codex has no arbitrary script-backed statusline in the same
  sense; we expose the data through `freeinference status` / `freeinference snapshot --json` /
  `freeinference render` for whoever the user wires in (their shell prompt, DevDesktop,
  tmux status bar, etc.).
- **External integrators** — `freeinference snapshot --json` and `freeinference render --mode line`
  are stable contracts. DevDesktop, tmux, and similar panels can subscribe
  without redesigning core state.

### Design principles

- **Status line reads live Claude JSON from stdin + cached health data** — zero network, average-latency target only
- **Hooks do local computation only** — no network, average-latency target only, always fail open (exit 0)
- **Every session mutation holds a cross-process file lock** — concurrent hooks and status lines coordinate writes; lock contention returns immediately (fail-open) and is counted in `state.DroppedMutations()`
- **Warnings use JSON `systemMessage`** — never plain stdout, never `additionalContext`, never in model context; no warning → no output at all (zero bytes)
- **Surface eligibility is gated by seven checks** — runtime active, client matches, session matches, session active, activation identity matches, observation fresh, provider confirmed FreeInference; any gate failing produces zero bytes
- **Provider detection gates all warnings** — no FreeInference warning or health symbol ever appears in a non-FreeInference session
- **Background refreshes are detached and coalesced across processes** — file-lock single-flight, per-endpoint circuit breakers (2→30min backoff), `Retry-After` honored
- **No inference probes for monitoring** — `freeinference doctor --probe --model <name>` is manual only, marked `X-Probe: synthetic`
- **Advisory warnings, never blocking** — context pressure, projection overflow, cache-low with pattern classification and likely diagnosis, cache TTL expiry; all labeled with confidence, all advisory
- **Schema validation + quarantine** — corrupt or unsupported state files are renamed aside so subsequent writes start fresh; hooks never block on bad state
- **Sanitized structured event log** — per-session `events.jsonl` records only event types and short categories; never prompt text, responses, transcripts, paths, keys, or raw error bodies

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
| `freeinference dashboard [--status] [--print-url]` | Open FreeInference account dashboard (`--status` for service health page) |
| `freeinference fi-status [--json] [--refresh] [--all]` | Fetch public service status without credentials or local session state |
| `freeinference context [--session <id>]` | Show context pressure information |
| `freeinference cache [--session <id>]` | Show cache efficiency pattern classification and likely diagnoses |
| `freeinference refresh [--force] [--if-stale --detach] [--worker models\|health\|account-usage]` | Refresh cached provider metadata |
| `freeinference hook <client> <event>` | Process a lifecycle hook event (internal) |
| `freeinference status-line install\|uninstall` | Manage the Claude Code status line |

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
| `FI_NO_BACKGROUND` | — | Set to `1` to disable detached background refresh |

The companion activates only when the current client has an approved
FreeInference runtime route and its matching credential. Claude Code uses
`ANTHROPIC_BASE_URL` plus `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY`.
Codex activation is established from the selected provider in
`~/.codex/config.toml` and that provider's `env_key`; a generic
`FREEINFERENCE_API_KEY` alone is not evidence that either client is using it.

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

## State model

The plugin uses three separate concepts for metrics:

| Source | Authoritative | Description |
|--------|:---:|-------------|
| `live_context` | ✓ | Latest Claude status-line snapshot; total-token semantics are recorded as current-context, cumulative-session, or unknown |
| `usage_observations` | ✗ | Up to 20 retained observations with request identity/epoch metadata; observed, analyzed, and usable counts are separate |
| `account_usage` | ✓ when capability is supported | Provider quota data, omitted unless a validated account-usage capability response is available |

Missing fields are `null` — never converted to zero. A zero-token field remains zero; a missing field remains null.

Cache-low warnings fire under these hypothetical conditions: 3+ usable
observations, ≥50K active context, read share <20% for 3 sequential observations, confirmed
FreeInference provider, and a 30-minute cooldown. They resolve after 3
sequential observations above 40%. The warning includes likely diagnosis
(see below).

Projection warnings qualify when active context is at least 60% of the
model's window and the projected next request (active + estimated prompt +
tool overhead + safety margin) would leave less than the configured output
reserve (default 16,000 tokens). Confidence is labeled `low` or `medium` —
never `high` in v0.1.0 because the companion does not see the full request
body the client sends.

Cache TTL expiry warnings may fire when a session has been idle past a
hypothetical prompt cache lifetime (~5 minutes). The next request might
re-read context at full price if the cached prefix has evicted. The
warning suggests sending a short warm-up message first to refill the
cache before the real query. Gated on ≥10K active context and a
30-minute cooldown.

### Cache miss pattern attribution

`freeinference cache` classifies cache miss patterns with likely diagnosis instead of
generic diagnostics:

| Pattern | Meaning | Example cause |
|---------|---------|---------------|
| **Thrashing** | High cache creation, low cache read | Dynamic content at the start of the system prompt; prefix keeps being rewritten |
| **No caching** | Almost all fresh input, negligible cache activity | Client not using `cache_control` breakpoints |
| **Decay** | Read share was good but is declining | Conversation growing past the cached prefix |
| **Intermittent** | Alternating good/bad observations | Tool results inserted before the cached prefix on some turns |

The inline cache-low warning also includes the diagnosis so the user gets
the likely cause at the moment it fires.

### Token budget projection

`freeinference status` and `freeinference report` show account quota status with a projected
exhaustion timeline only after the provider has returned a schema-valid,
authoritative account-usage response. The capability is recorded as
`supported`, `unsupported`, `forbidden`, or `unknown`; known unsupported and
forbidden endpoints are not retried automatically. When unavailable, no quota
or budget projection is rendered.

When the capability is supported, projection uses the session's observed token
burn rate:

```
Account Usage:
  Updated: 2026-07-29T15:30:00Z
  Requests: 4.2K / 10K (42.0%)
  Tokens:   1.2M / 5.0M (24.0%)
  Budget:   🟢 healthy — At current rate (~127K tok/hr over 1.2h), quota lasts until Jul 30 09:14.
```

Status tiers: healthy (>30% remaining), watch (15-30%), low (5-15%),
critical (<5%). Falls back to request-based quota when token limits aren't
reported.

### Status line rendering

The collapsed status line is width-aware and adapts to terminal column
count via the `COLUMNS` environment variable (set by Claude Code):

| Width | Segments shown |
|-------|---------------|
| **Wide (100+)** | Model, shield, cache read %, fresh tokens, context %, pressure |
| **Medium (60-99)** | Model, shield, cache read %, fresh tokens, context %, pressure |
| **Narrow (<60)** | Shield, cache read %, context % |

The shield icon `🛡` color tracks context usage: white when empty, orange
when getting high (60%+), red when critical (85%+). Unknown telemetry
renders as `—` (em dash), never fabricated as `0%`.

### Reporting levels

Ask the coding agent for a quick, normal, or detailed FreeInference check, or
use the matching CLI level directly:

```bash
freeinference status --level summary   # one line for an at-a-glance check
freeinference status --level standard  # current session essentials
freeinference status --level detailed  # essentials plus history and account diagnostics
```

Set the preferred default once with `freeinference config set reporting.level
standard`; `FI_REPORTING_LEVEL` can override it for a single shell or host.
`--compact` remains reserved for status-line integrations, and `--json`
remains the stable machine-readable contract.

### Sanitized event log

Each session has a bounded `events.jsonl` recording only lifecycle event
types (`session_started`, `status_observed`, `prompt_submitted`,
`turn_stopped`, `turn_failed`, `compaction_started`, `compaction_completed`,
`model_switch`, `session_ended`, `warning_shown`, `warning_resolved`) and short sanitized
details. Rotation kicks in past 256 KiB or 1,000 events per session.
Sessions older than 30 days are cleaned up opportunistically by
`CleanupStaleSessions`.

## Development

```bash
make build      # Static build into build/freeinference (ldd-verified)
make test       # Run tests
make test-race  # Run tests with the race detector
make vet        # Run go vet
make fmt-check  # Verify gofmt cleanliness
make bench      # Run performance benchmarks (average latency gate; not p95)
make check      # fmt + vet + test + race + plugin validation + git diff --check
make release    # Cross-compile all platforms + checksums
make smoke      # Quick smoke test
```

## Project layout

```
FreeInference/
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
│   └── codex/                 # .codex-plugin/, skills/ (no lifecycle hooks)
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

## Acknowledgment

This project was developed independently using FreeInference for inference
access during development. FreeInference did not commission, direct, fund, or
pay for this work. No representative of FreeInference reviewed or approved
this contribution, and this acknowledgment does not indicate sponsorship,
partnership, or endorsement by FreeInference or any affiliated organization.

I am acknowledging the service because access to capable inference
infrastructure can make meaningful open-source development more accessible
to developers and researchers who do not have the hardware or budget to run
these models themselves.

Organizations able to provide GPU capacity, hardware, cloud credits,
research funding, or other infrastructure resources should consider
supporting FreeInference so that it can continue making this capability
available for open-source development, research, and education.

## License

MIT
