---
name: fi-status
description: This skill is automatically triggered when the user asks about FreeInference session status, current metrics, token usage, or context consumption. Use when the user wants to see their current session's token counts, cache performance, model information, or context pressure level.
argument-hint: [--compact]
allowed-tools: Bash
---

# FreeInference Status

Display current session metrics for the FreeInference Companion.

## Usage

Run `fi status` to show:
- Current model and session ID
- Live context window usage (fresh tokens, cache reads, cache writes, output)
- Context pressure state (healthy/watch/warn/critical)
- Cache analysis (read share, creation share, fresh input share)
- Provider health (if configured)
- Last failure info (if any)

### Options

- `--compact` — Single-line output suitable for embedding in prompts or scripts
- `--session <id>` — Show a specific session (default: current)

### Example output

```
FreeInference Companion 0.1.0
Session: sess_abc123 (active)
Model:   minimax-m3 (1048576 context)

Live Context (from claude_statusline):
  Window:     1.0M (42.5% used)
  Fresh:      48K
  Cache Read: 1.2M
  Cache New:  94K
  Output:     38K

Pressure:   warn (projected 84%, confidence: high)
  Reason:   context above warn threshold

Cache Analysis (15 samples):
  Read Share:  78%
  New Share:   6%
  Fresh Share: 16%
  Trend:       stable
```

### Notes

- Context data comes from the Claude Code status line (live, authoritative)
- Cache analysis uses a rolling window of recent requests
- Pressure projections are estimates labeled with confidence