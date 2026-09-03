---
name: freeinference-refresh
description: This skill manages explicit refresh operations for FreeInference Companion metadata. Use when the user asks about stale model, health, account-usage, or public-status data. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference Refresh

Trigger and monitor explicit refresh operations for FreeInference Companion
metadata. Community-built and unofficial. Not affiliated with or endorsed by
FreeInference.

## Usage

There are two refresh modes:

### Forced refresh

Run `freeinference refresh --json` to force a complete refresh of all cached state:

```bash
freeinference refresh --json
```

This forces the selected metadata workers. Cache metrics are client
observations and are never reset or fetched by this command.

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

After running a refresh, `--json` reports boolean fields such as
`models_refreshed`, `health_refreshed`, `account_usage_refreshed`, and
`public_status_refreshed`. It may also report
`account_usage_capability` or an `error` string. There is no durable refresh
history or `next_stale` map in the command output.

### Example output

```json
{
  "models_refreshed": true,
  "health_refreshed": false,
  "account_usage_refreshed": false,
  "public_status_refreshed": true
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
- Provider health refresh sends a bounded metadata request when a health URL is
  configured
- Background refresh coalescence: multiple simultaneous refresh requests are coalesced into one to avoid redundant API calls

### Notes

- Forced refresh is useful when models seem out of date or provider status is stale.
- `--if-stale` is the recommended default — it avoids unnecessary network calls.
- Refresh does not affect active sessions or current model usage.
- Refreshes are explicit network operations. If the user wants to minimize
  provider load, recommend `--if-stale` and avoid `--force`.
