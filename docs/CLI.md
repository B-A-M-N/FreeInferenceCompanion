# CLI and configuration reference

This page is the technical reference for the commands and environment
variables introduced in the [README](../README.md). For Codex-specific provider
and plugin setup, see [Codex with FreeInference](codex.md).

## Commands

| Command | Description |
| --- | --- |
| `freeinference status [--compact\|--level summary\|standard\|detailed] [--client <type>] [--session <id>] [--json]` | Show local session metrics at the requested detail; no network request |
| `freeinference sessions [--include-identifiers] [--json]` | List locally retained sessions |
| `freeinference snapshot --json [--client <type>] [--session <id>]` | Print a machine-readable local session view |
| `freeinference render --mode line\|standard\|expanded [--client <type>] [--session <id>]` | Render a local panel or footer view |
| `freeinference context [--client <type>] [--session <id>]` | Show local context pressure; Codex reports unavailable |
| `freeinference cache [--client <type>] [--session <id>] [--json]` | Show local cache classification; Codex reports unavailable |
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

The `--refresh` options and `refresh` command are explicit network operations.
They are not part of ordinary hooks, status rendering, plugin installation, or
Codex skill installation. `doctor --probe --model <name>` is the only normal
command path that intentionally sends a synthetic inference request, and it
must be requested explicitly.

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
