---
name: freeinference-refresh
description: Explicitly refresh cached FreeInference metadata with rate-limit-aware controls.
argument-hint: "[--if-stale|--force] [--worker models|health|account-usage|public-status]"
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference refresh

Refreshes are not part of normal Claude hooks or status rendering. Only run a
refresh when the user explicitly requests it.

Use `freeinference refresh --if-stale --json` to avoid unnecessary requests.
`--force` intentionally bypasses freshness checks. Metadata refresh is bounded
and coalesced, but still makes network requests; it does not send inference
requests. `--worker public-status` is an unauthenticated status request.

