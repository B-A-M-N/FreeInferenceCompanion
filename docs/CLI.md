# CLI and configuration reference

This page is the technical reference for the commands and environment
variables introduced in the [README](../README.md). For Codex-specific provider
and plugin setup, see [Codex with FreeInference](codex.md).

## Commands

### Commit attribution

```text
freeinference attribution status [--json]
freeinference attribution set off|append|standalone [--json]
```

| Mode | Native agent attribution present | Native attribution absent |
| --- | --- | --- |
| `off` (default) | no change | no change |
| `append` | append inference attribution | no change |
| `standalone` | append and deduplicate provenance | append inference attribution |

The formatter emits one of these exact footer forms:

When the model is known:

```text
Inference: <model> via FreeInference.org
Support-FreeInference: https://freeinference.org/
```

When the model is unavailable, the fallback is:

```text
Inference provided via FreeInference.org.
Support-FreeInference: https://freeinference.org/
```

Companion never stages or originates a commit. Ambiguous Git grammar fails open unchanged. Native Claude attribution settings are not modified. Lower-level equivalent: `freeinference config set attribution.commit_mode <mode>`.

By default, `install` and `update` also reconcile alternate Claude/Codex roots whose currently selected configuration routes to an approved FreeInference endpoint. `--no-integration-discovery` limits plugin fan-out to canonical roots.

### Client environment integrations

```text
freeinference integrations list [--json]
freeinference integrations discover [--json]
freeinference integrations add --client claude-code|codex --root /path/to/root [--proxy-upstream <url>]
freeinference integrations remove --client claude-code|codex --root /path/to/root
freeinference integrations diagnose --client claude-code|codex --root /path/to/root [--model <id>] [--json]
```

Identity is `(client type, configuration root)`; model profiles inside one root remain one environment. Alternate roots must independently prove the approved FreeInference route for their client:

- Claude: `settings.json` `env.ANTHROPIC_BASE_URL`
- Codex: selected provider's `model_providers.<id>.base_url`

Discovery never recursively scans projects and never uses launcher names. Explicit `add` supports arbitrary roots. Codex loopback routes are candidates and require `--proxy-upstream <approved FI /v1 URL>`; direct `https://freeinference.org/v1` roots verify automatically. `integrations diagnose` reports route state and generated model capabilities without mutating `models.json`; pass `--model <id>` to evaluate only the selected catalog entry, otherwise the diagnostic uses the conservative strictest result across the catalog. Existing unowned Companion directories are refused; modified owned installs are preserved until reconciled safely. `remove` deletes only paths derived from the recorded identity and only after digest ownership checks pass.

Route matching is exact by client: Claude uses `https://freeinference.org/anthropic` and Codex uses `https://freeinference.org/v1`. Trailing slashes are ignored, but prefixes, alternate paths, query strings, fragments, credentials, escaped paths, and non-approved ports are rejected. Port `443` is equivalent to the default HTTPS port. Claude profiles still using the legacy FreeInference `/v1` route are skipped with a migration warning; change `env.ANTHROPIC_BASE_URL` to `https://freeinference.org/anthropic` and rerun discovery or install.

| Command | Description |
| --- | --- |
| `freeinference status [--compact\|--level summary\|standard\|detailed] [--client <type>] [--session <id>] [--json]` | Show local session metrics at the requested detail; no network request |
| `freeinference sessions [--include-identifiers] [--json]` | List locally retained sessions |
| `freeinference snapshot --json [--client <type>] [--session <id>]` | Print a machine-readable local session view |
| `freeinference render --mode line\|standard\|expanded [--client <type>] [--session <id>]` | Render a local panel or footer view |
| `freeinference context [--client <type>] [--session <id>]` | Show local context pressure; Codex uses its latest local rollout usage |
| `freeinference cache [--client <type>] [--session <id>] [--json]` | Show local cache classification; Codex uses bounded rollout counters |
| `freeinference report [--client <type>] [--session <id>] [--format markdown\|json]` | Generate a sanitized local support report |
| `freeinference failures [--client <type>] [--session <id>] [--model <name>] [--since <duration\|timestamp>] [--json]` | Aggregate retained local failure incidents |
| `freeinference models [--model <name>] [--refresh]` | List the cached model catalog; `--refresh` makes one catalog request |
| `freeinference doctor [--json] [--probe --model <name>]` | Run local checks and, when active, one bounded catalog request; `--probe` adds one explicit synthetic inference request |
| `freeinference refresh [--force\|--if-stale] [--detach] [--worker <name>] [--json]` | Explicitly refresh selected metadata/status caches; never inference |
| `freeinference fi-status [--json] [--problems\|--down] [--details] [--fail-degraded]` | Make one unauthenticated public-status request |
| `freeinference dashboard [--account\|--status] [--print-url]` | Open or print a dashboard URL; the browser makes any resulting request |
| `freeinference run claude\|codex [args...]` | Explicitly launch a client with optional per-process trace correlation |
| `freeinference trace [status\|setup\|uninstall] [--client claude-code\|codex] [--json]` | Inspect or manage reversible trace setup |
| `freeinference status-line install\|uninstall\|status` | Manage Claude's local status-line wrapper |
| `freeinference codex-footer install\|uninstall\|status` | Manage Codex's local native footer settings |
| `freeinference install [options]` / `update [options]` | Download and install or update a release bundle |
| `freeinference uninstall` | Remove installer-owned files while preserving local history |
| `freeinference config show\|set\|reset\|path` | Manage local Companion configuration |
| `freeinference companion status\|enable\|disable` | Inspect or change the local Companion kill switch |
| `freeinference hook <client> <event>` | Process a Claude Code or Codex lifecycle event; hooks are fail-open and local-only |
| `freeinference version [--json]` | Show binary and state-schema version information |
| `freeinference integrations list [--json]` | Show additional client environments owned by the installer |
| `freeinference integrations discover [--json]` | Show canonical, exported, and bounded-discovery client roots |
| `freeinference integrations diagnose --client <type> --root <path> [--model <id>] [--json]` | Inspect the selected route and generated model capabilities without mutation |
| `freeinference integrations add\|remove --client claude-code\|codex --root <path>` | Explicitly register or remove one alternate client environment |

The `--refresh` options and `refresh` command are explicit network operations.
They are not part of ordinary hooks, status rendering, plugin installation, or
Codex skill installation. `doctor --probe --model <name>` is the only normal
command path that intentionally sends a synthetic inference request, and it
must be requested explicitly.
| `freeinference install --help` | Install a release; supports `--no-integration-discovery` for canonical-only fan-out |
| `freeinference update --help` | Update a release; supports `--no-integration-discovery` for canonical-only fan-out |

## Environment

| Variable | Default | Description |
| --- | --- | --- |
| `FREEINFERENCE_API_KEY` | — | FreeInference API credential |
| `FREEINFERENCE_BASE_URL` | — | API fallback URL; not client activation evidence |
| `ANTHROPIC_AUTH_TOKEN` | — | Claude Code credential for the Anthropic-compatible route |
| `FI_HEALTH_URL` | — | Optional provider health URL |
| `FI_CACHE_DIR` | `~/.cache/freeinference-companion` | Local state directory |
| `FI_SESSION_ID` | — | Explicit session override for status/context/report |
| `FI_PROVIDER` | — | Attribution metadata only; does not activate the Companion |
| `FI_PROXY_UPSTREAM_URL` | — | Explicit approved upstream route for a local Claude compatibility proxy; ignored unless `ANTHROPIC_BASE_URL` is loopback |
| `FI_ALLOW_INSECURE_LOCALHOST` | — | Allows an `http://` loopback runtime endpoint; use only with an explicitly trusted local proxy |
| `FI_AUTO_REFRESH` | `0` | Opt in to detached stale-metadata refreshes |
| `FI_NO_BACKGROUND` | — | Disable detached refreshes |
| `FI_TRACING` | `1` for `freeinference run` | Enable or disable launch-time trace correlation |

The Companion activates only when the current client has an approved
FreeInference route and matching credential. A generic FreeInference key by
itself does not prove that Claude Code or Codex is using FreeInference.

## Client routes

| Client | Runtime endpoint | Credential | Protocol |
| --- | --- | --- | --- |
| Claude Code | `https://freeinference.org/anthropic` | `ANTHROPIC_AUTH_TOKEN` | Anthropic-compatible |
| Codex | selected `model_providers.<id>.base_url` | selected provider `env_key` | OpenAI Responses |

### Claude Code

Add this to `~/.claude/settings.json`, replacing only the key value:

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

Set both model variables to public IDs from the Anthropic route's catalog.
Availability can differ from the OpenAI-compatible route.

#### Claude through a local compatibility proxy

Some launchers, including HarvardClaude-style integrations, keep Claude's
runtime URL on loopback and forward it to FreeInference. A loopback URL alone
does not activate the Companion. The launcher must also provide the exact
approved upstream route in `FI_PROXY_UPSTREAM_URL`:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8765
export ANTHROPIC_AUTH_TOKEN=Free_Inference_API
export FI_PROXY_UPSTREAM_URL=https://freeinference.org/anthropic
export FI_ALLOW_INSECURE_LOCALHOST=1  # only for an intentional http loopback proxy
```

The Companion then records the effective FreeInference origin for local state
and management-cache identity while Claude continues to send inference traffic
to the local proxy. It rejects missing, non-FreeInference, or non-Anthropic
upstream declarations, so an ordinary local Claude proxy remains silent.

### Codex

See [Codex with FreeInference](codex.md) for provider setup, model profiles,
trace mapping, native footer installation, and the native marketplace plugin
installation path.

## Trace correlation

Trace correlation is explicit and launch-scoped. `freeinference run claude`
uses Claude's `ANTHROPIC_CUSTOM_HEADERS`; `freeinference run codex` uses the
selected provider's `env_http_headers` mapping. It adds only an opaque
`X-Session-ID`. It never adds `X-Request-ID`, prompts, paths, or credentials.

See [Trace correlation](TRACING.md) for the privacy tradeoff and opt-out.
