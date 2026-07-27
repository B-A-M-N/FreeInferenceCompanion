---
name: fi-install-statusline
description: Install or uninstall the FreeInference status line for the Claude Code bottom bar. This configures Claude Code to show real-time model, context, and cache metrics.
argument-hint: install|uninstall
allowed-tools: Bash, Read, Write
disable-model-invocation: true
---

# Install FreeInference Status Line

Configure the Claude Code status line to show FreeInference metrics.

## Usage

Run `fi status-line install` to:

1. Generate a status line wrapper script at `~/.claude/statusline-freeinference.sh`
2. Configure `~/.claude/settings.json` with the `statusLine` entry
3. Create a backup of your existing settings

The status line shows:

```
FI minimax-m3 ● | ctx 42% | cache 78%
```

- Current model name
- Health indicator (● = healthy, ◐ = degraded, ✗ = failure)
- Context usage percentage
- Cache read share percentage
- Pressure state (WATCH, WARN, CRIT) when applicable

### Uninstall

Run `fi status-line uninstall` to:

1. Remove the wrapper script
2. Restore the original settings.json from backup
3. Or remove the statusLine entry from settings

### Notes

- Claude Code only supports one status line command — this replaces any existing one
- The status line reads live Claude JSON from stdin plus cached health data
- Zero network requests from the status line (under 10ms target)
- Restart Claude Code after installing to see the status line