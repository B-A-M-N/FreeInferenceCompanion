---
name: freeinference-refresh
description: Explicitly refresh cached FreeInference metadata with rate-limit-aware controls.
argument-hint: "[--if-stale|--force] [--worker models|health|account-usage|public-status]"
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference refresh

Refreshes are never part of Codex plugin installation or automatic skill use.
Run one only when the user explicitly requests it.

Use `freeinference refresh --if-stale --json` to avoid unnecessary requests.
`--force` intentionally bypasses freshness checks. Refreshes make bounded
metadata/status requests but never send inference requests.
