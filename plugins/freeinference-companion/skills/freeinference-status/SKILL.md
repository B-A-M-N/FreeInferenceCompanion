---
name: freeinference-status
description: Show verified FreeInference provider status and locally available Codex diagnostics.
argument-hint: "[--level summary|standard|detailed]"
allowed-tools: Bash
---

# FreeInference status

Run these local commands together so the result shows every diagnostic that
Codex can currently provide:

```bash
freeinference status --client codex --level standard
freeinference context --client codex
freeinference cache --client codex --json
```

Use `--level summary` or `--level detailed` for the status command; add
`--json` when a machine-readable result is needed. The status command reports
verified provider configuration even when no Codex lifecycle snapshot exists.

This marketplace plugin is skill-only and installs no Codex lifecycle hooks.
Provider identity can be confirmed from Codex configuration, while context and
cache telemetry remain `unavailable` because Codex does not expose those
measurements here. Codex's native footer remains the source for its model,
remaining-context, and current-directory fields. These commands do not make a
provider request.
