# CLI and configuration reference

This page is the technical reference for the commands and environment
variables introduced in the [README](../README.md). For Codex-specific provider
and plugin setup, see [Codex with FreeInference](codex.md).

## Commands

| Command | Description |
| --- | --- |
| `freeinference status [--compact\|--level summary\|standard\|detailed] [--client <type>] [--session <id>]` | Show session metrics at the requested detail |
| `freeinference sessions` | List known sessions from the local index |
| `freeinference snapshot --json [--session <id>]` | Print a machine-readable normalized view |
| `freeinference render --mode line\|standard\|expanded [--session <id>]` | Render a stable panel or footer view |
| `freeinference models [--model <name>] [--refresh]` | List the cached model catalog; `--refresh` updates only models |
| `freeinference doctor [--probe --model <name>]` | Diagnose configuration and connectivity; probes are manual |
| `freeinference report [--client <type>] [--session <id>] [--format markdown\|json]` | Generate a sanitized support report |
| `freeinference failures [--client <type>] [--session <id>] [--model <name>] [--since <duration>] [--json]` | Aggregate retained local failure incidents |
| `freeinference dashboard [--status] [--print-url]` | Open the account dashboard or public status page |
| `freeinference fi-status [--json] [--problems\|--down] [--details]` | Fetch unauthenticated public service status |
| `freeinference run claude\|codex [args...]` | Launch a verified client with optional per-process trace correlation |
| `freeinference trace [setup\|uninstall] [codex] [--json]` | Inspect or manage reversible trace setup |
| `freeinference context [--session <id>]` | Show context pressure |
| `freeinference cache [--session <id>]` | Show cache pattern classification and diagnoses |
| `freeinference refresh [--force] [--if-stale --detach] [--worker <name>]` | Refresh provider metadata and public status |
| `freeinference hook <client> <event>` | Process a lifecycle hook event; used by the Claude integration (Codex marketplace plugin is skill-only) |
| `freeinference status-line install\|uninstall` | Manage the composing Claude status line |
| `freeinference codex-footer install\|uninstall\|status` | Manage Codex's native footer settings |

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
