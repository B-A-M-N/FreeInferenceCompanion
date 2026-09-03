# FreeInference Companion

FreeInference Companion is a small observability layer for FreeInference-powered
Claude Code and Codex sessions. It helps answer:

- What model am I actually using?
- How much context am I consuming?
- Is caching behaving normally?
- Is the provider healthy?
- What useful, sanitized information can I share when something breaks?

It reads the lifecycle and status data the client already exposes, keeps that
state locally, and stays out of the inference path.

**Companion, not proxy.** No prompt interception, no transcript scraping, no
automatic failover, no daemon, and no monitoring inference calls.

## Why this exists

I built this after using FreeInference for inference access during development.
I wanted a small, inspectable tool that makes everyday use easier and helps
people understand the service they depend on.

More broadly, I believe providing inference to the public is a vital and
increasingly necessary resource. As capable models become part of writing,
research, education, software, and ordinary problem-solving, access to useful
inference should not be treated as a luxury available only to people with
large budgets or as something easy to overlook and dismiss. Publicly available
inference gives more people room to learn, experiment, build, and participate.
That value is often most visible only after it disappears.

FreeInference is one example of that kind of public resource. This companion is
my small way of helping its users understand and care for it, while keeping the
provider's traffic direct and leaving optional network behavior under the
user's control.

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

## What you get

- Live Claude context metrics and context-pressure warnings.
- Rolling cache-pattern classification with likely diagnoses.
- Cached model health and public service status.
- Optional account-budget projection when the provider exposes validated,
  authoritative usage data.
- Sanitized failure summaries and support reports.
- A native Claude status-line integration and a native Codex footer setup.

Example output (illustrative):

```text
$ freeinference status --compact
🛡 glm-5.1  cache 78%  fresh 12.4K  ctx 41%  healthy
```

Warnings are advisory. Unknown telemetry stays unknown rather than becoming a
made-up zero or an overconfident conclusion.

## Install

For normal use, download a release and follow [Installation](docs/INSTALL.md).
For source-checkout development, see [Development](docs/DEVELOPMENT.md).

```bash
# After installing the freeinference binary
freeinference doctor
freeinference status-line install       # Claude Code
freeinference codex-footer install      # Codex native footer
```

The repository also includes native Claude Code and Codex plugin bundles. The
Codex marketplace/plugin commands are documented in [Codex with
FreeInference](docs/codex.md).

Supported release platforms are Linux amd64/arm64 and macOS amd64/arm64. The
release binary is fully static (`CGO_ENABLED=0`).

## How it behaves

Normal lifecycle operation is local-only:

- Installing the plugin, receiving hooks, recording sessions, and rendering
  status do not make API calls.
- Client inference traffic remains direct to FreeInference; the Companion is
  never a gateway or traffic interceptor.
- Hooks fail open and never block the user's client.
- `freeinference fi-status` is an explicit, unauthenticated public-status
  request. `doctor --probe` is an explicit manual inference probe.

Optional metadata refresh is disabled by default. If `FI_AUTO_REFRESH=1` is
set, stale refreshes run detached and are protected by one shared provider
slot, one-minute spacing, `Retry-After`, rate-limit cooldowns, and per-worker
circuit breakers. If a refresh is deferred, the cache remains stale and a
later lifecycle event or explicit refresh can retry it safely; the hook never
builds a request backlog.

## What it observes—and what it never observes

It records bounded lifecycle events, client-provided status metrics, cached
provider metadata, and local cache-analysis observations. It can produce
`snapshot --json`, `render --mode line`, and sanitized `report` output for
users, panels, or support conversations.

It never stores or sends prompts, responses, transcripts, credentials, raw
headers, raw error bodies, or path-shaped client inputs. It does not perform
automatic model switching, compaction, cloud synchronization, benchmarking,
or background inference probes.

Codex exposes less context and cache telemetry than Claude Code, so those
fields are reported as unavailable rather than inferred. Cache diagnoses are
local heuristics, not provider-confirmed causes or probabilities.

## Documentation

Choose the level of detail you need:

- [Installation](docs/INSTALL.md) — release binaries and plugin setup.
- [CLI and configuration reference](docs/CLI.md) — commands, environment, and
  client routes.
- [Architecture](docs/ARCHITECTURE.md) — data flow, local state, and design
  constraints.
- [Observability](docs/OBSERVABILITY.md) — freshness, warning thresholds,
  account usage, and event retention.
- [Cache diagnostics](docs/CACHE_DIAGNOSTICS.md) — patterns, scores, and
  heuristic limits.
- [Codex with FreeInference](docs/codex.md) — provider, profiles, footer, and
  native plugin instructions.
- [Compatibility](docs/COMPATIBILITY.md) — supported and unavailable
  telemetry by client.
- [Trace correlation](docs/TRACING.md) — optional launch-scoped support
  correlation and privacy tradeoffs.
- [Security](SECURITY.md) — credential boundary, filesystem protections,
  response limits, and vulnerability reporting.
- [Development](docs/DEVELOPMENT.md) — build, test, package, and CI checks.

## Quick command reference

```bash
freeinference status                         # current session
freeinference status --level detailed        # deeper diagnostics
freeinference models                         # cached model catalog
freeinference models --refresh               # explicit catalog refresh
freeinference cache                          # cache pattern diagnosis
freeinference report --format markdown       # sanitized support report
freeinference fi-status --json               # public service status
```

## License

MIT
