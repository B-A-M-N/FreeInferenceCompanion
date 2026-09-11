---
name: freeinference-cache
description: Show bounded local Codex rollout cache telemetry without inventing metrics.
allowed-tools: Bash
---

# FreeInference cache diagnostics

Run `freeinference cache --client codex --json` when the user asks about cache
behavior. The command reads Codex's latest local rollout counters and reports
fresh/cache-read/cache-write shares. If no completed usage event exists, the
result is explicitly pending/unavailable; it is never a zero or a fabricated
hit rate.
