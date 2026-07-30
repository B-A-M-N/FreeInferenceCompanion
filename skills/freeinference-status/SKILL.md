---
name: freeinference-status
description: This skill is automatically triggered when the user asks about FreeInference session status, current metrics, token usage, or context consumption. Use when the user wants to see their current session's token counts, cache performance, model information, or context pressure level. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "[--level summary|standard|detailed]"
allowed-tools: Bash
---

# FreeInference Status

Display current session metrics for the FreeInference Companion. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `freeinference status` to show:
- Current model and session ID
- Live context window usage (fresh tokens, cache reads, cache writes, output)
- Context pressure state (healthy/watch/warn/critical)
- Cache analysis (read share, creation share, fresh input share)
- Provider health (if configured)
- Last failure info (if any)

### Options

- `--compact` — Single-line output suitable for embedding in prompts or scripts
- `--level summary` — One-line, at-a-glance session state
- `--level standard` — Essential provider, context, usage, pressure, and turn state
- `--level detailed` — Standard view plus cache history, compaction, circuit, and account diagnostics
- `--session <id>` — Show a specific session (default: current)

When the user asks for “a quick check,” use `--level summary`; for “full
details” or troubleshooting, use `--level detailed`. The persistent default
can be changed with `freeinference config set reporting.level <level>`.

### Example output

```
FreeInference Companion 0.1.0
Session:  sess_abc123 (active)
Client:   claude-code
Provider: freeinference (source: FREEINFERENCE_API_KEY)
Model:    minimax-m3 (1.0M context)

Live Context (from claude_statusline):
  Context:    446K / 1.0M (42.5% used)
  Latest request:
    Fresh:      48K
    Cache read: 380K
    Cache new:  18K
    Output:     3K

Pressure: warn
Turn:     active

Cache Analysis (12 unique samples):
  Read share:  78%
  New share:   6%
  Fresh share: 16%
  Trend:       stable
```

### Notes

- Context data comes from the Claude Code status line (live, authoritative)
- Cache analysis uses a rolling window of the last 5 unique requests (duplicate status-line renders are deduplicated)
