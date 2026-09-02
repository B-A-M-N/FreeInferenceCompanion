---
name: freeinference
description: FreeInference Companion — session monitoring, cache analysis, diagnostics, and provider management
---

# FreeInference Companion

Community-built and unofficial observability layer for FreeInference-powered agents. Surfaces live model health, session lifecycle metrics, and provider status. Not affiliated with or endorsed by FreeInference.

## Overview

The `freeinference` CLI provides the following commands. Run any command with `--json` for machine-readable output, and `--help` for per-command help.

## Commands

### `freeinference run` and `freeinference trace`

Use the explicit launcher when you want a fresh, per-process support
correlation. It injects the documented `X-Session-ID` plus fixed
client/version/workload classification headers for a verified FreeInference
route; ordinary client launches are unaffected.

```bash
freeinference run claude
freeinference trace
freeinference trace --json
freeinference config set tracing.enabled false
```

Trace correlation sends and records no prompts, responses, paths, raw headers,
or credentials.

### `freeinference fi-status`

Fetch public service health without session state or credentials. In this
plugin the namespaced slash command is `/freeinference-companion:fi-status`.

```bash
freeinference fi-status
freeinference fi-status --json
freeinference fi-status --problems
freeinference fi-status --details
```

All models are shown by default. `--problems`/`--down` filters to down or
unknown models; `--refresh` and `--all` are deprecated compatibility no-ops.

### `freeinference status`

Show current session status with context usage, pressure, and cache analysis.

```bash
freeinference status --json
freeinference status --compact
freeinference status --level standard
```

Flags: `--client <type>`, `--compact`, `--level summary|standard|detailed`, `--session <id>`, `--json`

Use `--level summary` for a quick one-line check, `standard` for core session
state, and `detailed` for cache/compaction/circuit/account diagnostics. To
persist the preference, run `freeinference config set reporting.level <level>`.

### `freeinference sessions`

List all recorded sessions across clients.

```bash
freeinference sessions --json
```

Flags: `--include-identifiers`, `--json`

### `freeinference snapshot`

Output the full session snapshot in JSON format for machine consumption.

```bash
freeinference snapshot --json
```

### `freeinference render`

Render session status as human-readable output.

```bash
freeinference render --mode expanded
freeinference render --mode line
```

### `freeinference models`

List available models from the catalog, optionally showing a specific model.

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
freeinference report --session <id> --format json
```

### `freeinference dashboard`

Open the FreeInference dashboard in your browser.

```bash
freeinference dashboard --print-url
```

### `freeinference context`

Show current context usage for the active session.

```bash
freeinference context --json
```

### `freeinference cache`

Analyze cache efficiency and provide recommendations.

Automatic context/cache warnings are emitted only on Claude Code's
`UserPromptSubmit` hook; the status line remains a local display surface
between prompts.

```bash
freeinference cache --json
```

### `freeinference refresh`

Refresh cached data (models, health, and account usage when the provider capability is available).

```bash
freeinference refresh --force --json
freeinference refresh --if-stale --json
freeinference refresh --detach
```

### `freeinference status-line`

Install or uninstall the status-line wrapper for Claude Code.

```bash
freeinference status-line status --json
freeinference status-line install --scope user --json
freeinference status-line uninstall --scope user
```

Subcommands: `install`, `uninstall`, `status`
Flags: `--scope <user|project|local>`, `--project <dir>`, `--json`

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
| `context.watch_enter` | 70 | Context usage above this shows "watching" pressure. |
| `context.warn_enter` | 80 | Context usage above this shows a warning. The status line turns yellow. |
| `context.critical_enter` | 90 | Context usage above this shows critical pressure. The shield turns red. Hooks produce a critical warning. |
| `context.watch_leave` | 60 | When recovering from watch, pressure drops back to healthy below this. |
| `context.warn_leave` | 65 | When recovering from warn, drops to recovering below this. |
| `context.critical_leave` | 75 | When recovering from critical, drops to recovering below this. |
| `context.output_reserve` | 16000 | Tokens reserved for model output in projection warnings. Higher values trigger earlier warnings. |

**Cache diagnostics thresholds:**
| Key | Default | Effect |
|-----|---------|--------|
| `cache.warn_threshold` | 0.20 | Cache read share below this (20%) counts as "low" — triggers cache efficiency warnings. |
| `cache.recovered_threshold` | 0.40 | Cache read share above this (40%) counts as "recovered" — resolves active cache warnings. |
| `cache.cooldown_mins` | 30 | Minutes between successive cache-low warnings to avoid nagging. |

**Other:**
| Key | Default | Effect |
|-----|---------|--------|
| `reporting.level` | `detailed` | Default interactive status detail: `summary`, `standard`, or `detailed`. |
| `refresh.interval_mins` | 5 | How often background refresh checks for stale provider data. |
| `privacy.diagnostic_probes` | true | Allow diagnostic inference probes (`doctor --probe`). Set to `false` to disable. |

**Using `config show --json`**: Each field shows its current `value`, `source` (default, config_file, or environment), and `valid` status. If an environment variable is set, it overrides the file value.

**Using `config set`**: Values are validated before saving. Invalid values produce an error. Use `config reset` to restore all defaults.

**Using environment variables**: Any setting can be set via `FI_<NAME>` (e.g., `FI_CONTEXT_WARN_ENTER=75`). These take precedence over the config file but do not modify it.

### `freeinference companion`

Enable, disable, or check the status of the companion.

```bash
freeinference companion status --json
freeinference companion enable
freeinference companion disable
```

Subcommands: `status`, `enable`, `disable`

### `freeinference version`

Show the companion version and schema information.

```bash
freeinference version --json
```

## Common Flags

Many commands accept `--json` for machine-readable JSON output and `--help` for usage help. The `--client <type>` flag accepts `claude-code` (default) or `codex`.

## Environment Variables

- `FREEINFERENCE_API_KEY` — API credential for the OpenAI-compatible API
- `FREEINFERENCE_BASE_URL` — generic provider API URL; not Claude activation evidence
- `ANTHROPIC_AUTH_TOKEN` — Claude Code credential for the Anthropic-compatible endpoint
- `FI_HEALTH_URL` — Health monitoring URL (optional)
- `FI_CACHE_DIR` — Cache directory (default: `~/.cache/freeinference-companion`)
- `FI_SESSION_ID` — Explicit session override
- `FI_PROVIDER` — Attribution metadata only; it does not activate the companion
- `FI_AUTO_REFRESH` — Set to `1` to opt in to stale metadata refreshes from lifecycle hooks
- `FI_NO_BACKGROUND` — Disable detached background refresh after opting in
- `FI_DISABLED` — Set to `1` to disable all companion features
- `FI_ALLOW_INSECURE_LOCALHOST` — Allow `http://` loopback (development only)
- `FI_TRACING` — Enable/disable launch-time `X-Session-ID` correlation (`1` by default for `freeinference run`)

## Claude Code Runtime Setup

Claude Code uses FreeInference's Anthropic-compatible endpoint, not `/v1`.
Set these values in `~/.claude/settings.json`; use `Free_Inference_API` only
as a placeholder and keep the real credential outside shared files.

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

Use public model IDs for both model variables. The built-in Anthropic defaults
are not FreeInference public-catalog models. Model availability can differ
from the OpenAI-compatible `/v1` route used by Codex.

## Example Workflows

**Quick status check:**
```bash
freeinference status --json
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

**List recent sessions:**
```bash
freeinference sessions --json
```

**Launch and inspect trace correlation:**
```bash
freeinference run claude
freeinference trace --json
freeinference config set tracing.enabled false
```

**View configuration:**
```bash
freeinference config show --json
freeinference config set context.watch_enter 60
```
