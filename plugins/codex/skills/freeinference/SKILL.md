---
name: freeinference
description: FreeInference Companion — manual provider diagnostics, model discovery, and configuration guidance for Codex. This skill package does not install automatic lifecycle telemetry; Codex context and cache metrics are reported as unavailable.
---

# FreeInference Companion (Codex)

Community-built and unofficial skill package for FreeInference-powered Codex sessions. It runs user-requested provider diagnostics and model discovery; it does **not** install automatic lifecycle telemetry. **Codex has no status-line system** — the `status-line` subcommand is not applicable. Not affiliated with or endorsed by FreeInference.

## Overview

The `freeinference` CLI provides the following commands. Run any command with `--json` for machine-readable output, and `--help` for per-command help.

## Commands

### `freeinference fi-status`

Fetch public FreeInference service status. This is stateless and
unauthenticated; it does not require the Codex provider to be active.

```bash
freeinference fi-status --json
freeinference fi-status --problems
freeinference fi-status --details
```

All models are shown by default. Use `--problems`/`--down` for only down or
unknown models. `--refresh` and `--all` remain deprecated compatibility
no-ops.

### `freeinference status`

Show current session status with pressure and the telemetry availability state.

```bash
freeinference status --json
freeinference status --client codex
freeinference status --level standard --client codex
```

Note: this skill does not receive lifecycle events, and Codex does not expose live context metrics; values report as `unavailable`.

Flags: `--client <type>`, `--compact`, `--level summary|standard|detailed`, `--session <id>`, `--json`

Use `--level summary` for an at-a-glance check, `standard` for core session
state, and `detailed` for cache/compaction/circuit/account diagnostics. To
persist the preference, run `freeinference config set reporting.level <level>`.

### `freeinference sessions`

List all recorded sessions across clients.

```bash
freeinference sessions --json
```

Flags: `--include-identifiers`, `--json`

### `freeinference snapshot`

Output the full session snapshot in JSON format.

```bash
freeinference snapshot --json --client codex
```

### `freeinference render`

Render session status as human-readable output.

```bash
freeinference render --mode expanded --client codex
```

### `freeinference models`

List available models from the catalog.

```bash
freeinference models
freeinference models --model minimax-m3
freeinference models --refresh
```

### `freeinference doctor`

Run diagnostic checks on the companion installation.

```bash
freeinference doctor --json
freeinference doctor --probe --model minimax-m3 --json
```

### `freeinference report`

Generate a sanitized report suitable for sharing with support.

```bash
freeinference report --json
freeinference report --client codex --session <id> --format json
```

### `freeinference dashboard`

Open the FreeInference dashboard in your browser.

```bash
freeinference dashboard --print-url
```

### `freeinference context`

Codex live context telemetry is unsupported by this integration. The command
reports `unavailable` rather than treating missing values as zero.

```bash
freeinference context --json
```

### `freeinference cache`

Codex cache telemetry is unsupported by this integration. The command reports
`unavailable` rather than presenting a pseudo-analysis.

```bash
freeinference cache --json
```

### `freeinference refresh`

Refresh cached data (models, health, and account usage when the provider capability is available).

```bash
freeinference refresh --force --json
freeinference refresh --if-stale
```

### `freeinference status-line`

Install or uninstall the status-line wrapper. **Not applicable for Codex** — Codex has no status-line system.

```bash
# This subcommand is for Claude Code only
freeinference status-line status --json
```

### `freeinference config`

Manage persistent configuration. Settings persist across sessions in `~/.config/freeinference-companion/config.json`.

```bash
freeinference config show --json
freeinference config set context.warn_enter 75
freeinference config reset
freeinference config path
```

Subcommands: `show`, `set <key> <value>`, `reset [<key>]`, `path`

**Context pressure thresholds** (percentage of context window used):
| Key | Default | Effect |
|-----|---------|--------|
| `context.watch_enter` | 70 | Context above this shows "watching" pressure. |
| `context.warn_enter` | 80 | Context above this shows a warning. Shield turns yellow. |
| `context.critical_enter` | 90 | Context above this is critical. Shield turns red. |
| `context.watch_leave` | 60 | Recovery threshold below watch. |
| `context.warn_leave` | 65 | Recovery threshold below warn. |
| `context.critical_leave` | 75 | Recovery threshold below critical. |
| `context.output_reserve` | 16000 | Tokens reserved for output in projection warnings. |

**Cache and other:**
| Key | Default | Effect |
|-----|---------|--------|
| `reporting.level` | `detailed` | Default interactive status detail: `summary`, `standard`, or `detailed`. |
| `cache.warn_threshold` | 0.20 | Cache read share below this triggers low-cache warnings. |
| `cache.recovered_threshold` | 0.40 | Cache read share above this resolves warnings. |
| `cache.cooldown_mins` | 30 | Cooldown between cache warnings. |
| `refresh.interval_mins` | 5 | Background refresh check interval. |
| `privacy.diagnostic_probes` | true | Allow diagnostic inference probes. |

`config show --json` shows each field's value, source (default/config_file/environment), and validity. Environment variables override the config file. `config reset` restores all defaults.

### `freeinference companion`

Enable, disable, or check the status of the companion.

```bash
freeinference companion status --json
freeinference companion enable
freeinference companion disable
```

### `freeinference version`

Show the companion version and schema information.

```bash
freeinference version --json
```

## Common Flags

Many commands accept `--json` for machine-readable JSON output and `--help` for usage help. The `--client <type>` flag accepts `codex` (default for this plugin) or `claude-code`.

## Environment Variables

- `FREEINFERENCE_API_KEY` — API credential for the OpenAI-compatible API
- `FREEINFERENCE_BASE_URL` — generic provider API URL; Codex activation comes from `config.toml`
- `FI_HEALTH_URL` — Health monitoring URL (optional)
- `FI_CACHE_DIR` — Cache directory (default: `~/.cache/freeinference-companion`)
- `FI_SESSION_ID` — Explicit session override
- `FI_PROVIDER` — Attribution metadata only; it does not activate the companion
- `FI_NO_BACKGROUND` — Disable background refresh
- `FI_DISABLED` — Set to `1` to disable all companion features
- `FI_ALLOW_INSECURE_LOCALHOST` — Allow `http://` loopback (development only)

## Codex Runtime Setup and Model Switching

Codex uses FreeInference's OpenAI-compatible Responses endpoint, not the
Anthropic-compatible endpoint used by Claude Code. Keep the credential in the
environment rather than in `config.toml`:

```bash
export FREEINFERENCE_API_KEY='Free_Inference_API'
```

Add this provider once to `~/.codex/config.toml`:

```toml
model_provider = "freeinference"

[model_providers.freeinference]
name = "FreeInference"
base_url = "https://freeinference.org/v1"
env_key = "FREEINFERENCE_API_KEY"
wire_api = "responses"
```

Create a profile per model, for example `~/.codex/glm.config.toml` with
`model = "glm-5.1"` and `~/.codex/coding.config.toml` with
`model = "kimi-k2.7-code"`. Switch with `codex --profile glm` or
`codex --profile coding`; use `codex --model <id>` for a one-off choice.
Only create profiles for models returned by the `/v1/models` catalog—some
models are endpoint-exclusive and belong in Claude Code's Anthropic setup.

## Known Differences from Claude Code

1. **No status-line system** — Codex does not support status-line wrappers. `freeinference status-line` is not applicable.
2. **Context metrics** — Codex does not expose live context window usage to plugins. Context values report as `unavailable`.
3. **Cache metrics** — Codex does not expose cache metrics. Cache analysis reports `unavailable`.
4. **Automatic telemetry** — this package intentionally installs no Codex lifecycle hooks. Session metrics are available only when another supported integration records them.

## Example Workflows

**Quick status check:**
```bash
freeinference status --client codex --json
```

**Full diagnostic:**
```bash
freeinference doctor --json
freeinference report --json
```

**Check cache performance:**
```bash
freeinference cache --json
freeinference refresh --if-stale
```

**Browse models:**
```bash
freeinference models
freeinference models --model minimax-m3
```

**View configuration:**
```bash
freeinference config show --json
```
