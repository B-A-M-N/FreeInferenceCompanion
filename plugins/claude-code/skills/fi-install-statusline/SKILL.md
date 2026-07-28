---
name: fi-install-statusline
description: Install or uninstall the FreeInference status line for the Claude Code bottom bar. This configures Claude Code to show real-time model, context, and cache metrics. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "install|uninstall"
allowed-tools: Bash, Read, Write
disable-model-invocation: true
---

# Install FreeInference Status Line

Configure the Claude Code status line to show FreeInference metrics. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `fi status-line install` to:

1. Generate a status line wrapper script at `~/.claude/statusline-freeinference.sh`
2. Compose with any existing status line (both run; outputs are joined)
3. Configure `~/.claude/settings.json` atomically and record installation metadata
   under `~/.config/freeinference-companion/installations/`

The status line shows:

```
FI minimax-m3 ● | ctx 42% | read 78%
```

- Current model name
- Health indicator (● = healthy, ◐ = degraded, ✗ = failure)
- Context usage percentage
- Cache read share percentage
- Pressure state (WATCH, WARN, CRIT) when applicable

### Uninstall

Run `fi status-line uninstall` to:

1. Remove the wrapper script
2. Restore the previous statusLine value (or remove the key) — the rest of
   your settings file is left untouched

### Notes

- If an existing status line is too complex to compose safely, installation stops and explains instead of overwriting it
- The status line reads live Claude JSON from stdin plus cached health data
- Zero network requests from the status line (under 10ms target)
- Restart Claude Code after installing to see the status line