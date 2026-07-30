---
name: freeinference-refresh
description: This skill manages refresh operations for FreeInference Companion. Use when the user wants to force a refresh of the model catalog, context data, cache metrics, or other cached state. Use when the user asks about refreshing data, stale info, or updating the model list. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
---

# FreeInference Refresh

Trigger and monitor refresh operations for FreeInference Companion cached state. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

There are two refresh modes:

### Forced refresh

Run `freeinference refresh --json` to force a complete refresh of all cached state:

```bash
freeinference refresh --json
```

This forces:
- Model catalog refresh from the API
- Provider health check
- Cache metrics reset and re-collection

### Conditional (stale-only) refresh

Run `freeinference refresh --if-stale --json` to refresh only if data is older than the configured threshold:

```bash
freeinference refresh --if-stale --json
```

This:
- Checks staleness of each data source independently
- Only refreshes sources that exceed their threshold
- Skips already-fresh data (saves time and API calls)

### Showing refresh results

After running a refresh, the JSON output includes:

- `refreshed`: list of data sources that were refreshed
- `skipped`: list of sources that were already fresh
- `errors`: any sources that failed to refresh
- `timestamp`: when the refresh completed
- `next_stale`: when each source will next need refreshing

### Example output

```json
{
  "timestamp": "2026-07-29T16:00:00Z",
  "refreshed": ["model-catalog", "provider-health"],
  "skipped": ["cache-metrics"],
  "errors": [],
  "next_stale": {
    "model-catalog": "2026-07-29T17:00:00Z",
    "provider-health": "2026-07-29T16:30:00Z",
    "cache-metrics": "2026-07-29T16:05:00Z"
  }
}
```

### Checking staleness

Before refreshing, you can check staleness with:

```bash
freeinference status --json
```

The status output includes staleness indicators for each data source.

### Refresh behavior notes

- Forced refresh always hits the API; use `--if-stale` to avoid unnecessary calls
- Cache metrics are not truly "refreshable" — they are collected over time
- Model catalog refresh re-downloads the full catalog from `/v1/models`
- Provider health refresh sends a minimal connectivity check
- Background refresh coalescence: multiple simultaneous refresh requests are coalesced into one to avoid redundant API calls

### Notes

- Forced refresh is useful when models seem out of date or provider status is stale.
- `--if-stale` is the recommended default — it avoids unnecessary network calls.
- Refresh does not affect active sessions or current model usage.
- If `freeinference refresh` is not yet implemented, explain the current auto-refresh behavior and suggest the manual approach of restarting the companion.
