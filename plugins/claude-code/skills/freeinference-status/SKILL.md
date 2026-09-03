---
name: freeinference-status
description: Show the current FreeInference Companion session status, context pressure, and cache observations.
argument-hint: "[--level summary|standard|detailed]"
allowed-tools: Bash
---

# FreeInference status

Run `freeinference status` for the current verified FreeInference Claude
session. Use `--level summary` for a quick check, `standard` for the useful
core view, and `detailed` for cache, account, and circuit details. `--json` is
available for machine-readable output.

These values are local observations from Claude's status-line contract. The
Companion does not make a provider request for this command.

In Claude Code the slash command is:

```text
/freeinference-companion:freeinference-status
```
