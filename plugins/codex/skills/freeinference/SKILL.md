---
name: freeinference
description: FreeInference Companion — session monitoring, cache analysis, diagnostics, and provider management (Codex edition). Context and cache metrics are Codex-unavailable and reported as unknown.
---

# FreeInference Companion (Codex)

Community-built and unofficial observability layer for FreeInference-powered Codex sessions. Surfaces live model health, session lifecycle metrics, and provider status. **Codex has no status-line system** — the `status-line` subcommand is not applicable. Not affiliated with or endorsed by FreeInference.

## Overview

The `freeinference` CLI provides the following commands. Run any command with `--json` for machine-readable output, and `--help` for per-command help.

## Commands

### `freeinference status`

Show current session status with context usage, pressure, and cache analysis.

```bash
freeinference status --json
freeinference status --client codex
```

Note: Codex does not expose live context metrics; values may report as `unknown`.

Flags: `--client <type>`, `--compact`, `--session <id>`, `--json`

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

Show current context usage. Note: Codex may report unknown values.

```bash
freeinference context --json
```

### `freeinference cache`

Analyze cache efficiency. Note: Cache metrics are not available from Codex.

```bash
freeinference cache --json
```

### `freeinference refresh`

Refresh cached data (models, health, account usage).

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

- `FREEINFERENCE_API_KEY` — API key
- `FREEINFERENCE_BASE_URL` — API base URL (default: `https://freeinference.org/v1`)
- `FI_HEALTH_URL` — Health monitoring URL (optional)
- `FI_CACHE_DIR` — Cache directory (default: `~/.cache/freeinference-companion`)
- `FI_SESSION_ID` — Explicit session override
- `FI_PROVIDER` — Force provider (e.g., `freeinference`)
- `FI_NO_BACKGROUND` — Disable background refresh
- `FI_DISABLED` — Set to `1` to disable all companion features
- `FI_ALLOW_INSECURE_LOCALHOST` — Allow `http://` loopback (development only)

## Known Differences from Claude Code

1. **No status-line system** — Codex does not support status-line wrappers. `freeinference status-line` is not applicable.
2. **Context metrics** — Codex does not expose live context window usage to plugins. Context values may report as `unknown`.
3. **Cache metrics** — Codex does not expose cache metrics. Cache analysis may show limited or no data.
4. **Hook events** — Codex supports fewer hook events: SessionStart, SessionEnd, UserPromptSubmit, PreCompact, PostCompact, Stop (no StopFailure).

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
