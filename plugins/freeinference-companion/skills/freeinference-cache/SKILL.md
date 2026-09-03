---
name: freeinference-cache
description: Show the Codex cache telemetry boundary without inventing metrics.
allowed-tools: Bash
---

# FreeInference cache diagnostics

Run `freeinference cache --client codex --json` when the user asks about cache
behavior. Codex does not expose per-request cache telemetry through this
plugin, so the result is `unavailable`; it is not a zero or a fabricated hit
rate.
