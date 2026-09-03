# Observability and network behavior

FreeInference Companion records Claude Code’s existing lifecycle/status data
locally. Codex has no lifecycle hooks in this release; its skill-only plugin
runs explicit local diagnostics. Neither path observes by sending extra
inference requests, proxying traffic, scraping prompts/transcripts, or running
a daemon.

Normal Claude hook/status-line operation and Codex plugin installation are
local-only. In particular, installing or using either plugin does not make an
API call and cannot consume the provider’s inference rate limit. Explicit
commands such as `fi-status`, `models --refresh`, `refresh`, and
`doctor --probe` are the exceptions described by their command help.

Metadata refresh is opt-in. Set `FI_AUTO_REFRESH=1` to allow lifecycle hooks
to request detached refresh workers for stale model/health/account metadata.
The default is disabled. A refresh pass selects at most one stale authenticated
worker, uses a provider-scoped spacing slot, honors `Retry-After`, and applies
backoff/circuit-breaker cooldowns. Public service status is unauthenticated
and separate from account usage.

Use `freeinference refresh --worker models` or `freeinference models --refresh`
for an explicit model-catalog refresh. The latter refreshes only the catalog;
it does not fan out into health, account usage, or public-status requests.

Cached responses carry timestamps and are marked stale when they exceed their
TTL. Stale account usage is never used for budget ETA calculations. Invalid or
semantically inconsistent cache files are quarantined and rebuilt on a later
refresh.

## What is recorded

The local state model keeps three sources separate:

| Source | Authority | Meaning |
| --- | --- | --- |
| `live_context` | client observation | The latest Claude status-line snapshot; token totals retain their declared current-context, cumulative-session, or unknown semantics |
| `usage_observations` | diagnostic only | Up to 20 retained observations with request identity/epoch metadata; observed, analyzed, and usable counts remain distinct |
| `account_usage` | authoritative when supported | Provider quota data, accepted only after schema and freshness validation |

Missing fields are `null`, never zero-filled. A zero-token value remains zero;
an absent value remains unknown.

The status line reads local state and the status JSON supplied by the client.
It does not fetch public health or model metadata synchronously. The public
monitor may appear in detailed surfaces only when a separately cached result
exists.

## Warning and projection semantics

Cache-low warnings require at least three usable observations, 50K active
context tokens, read share below 20% for three sequential observations, a
confirmed FreeInference runtime, and a 30-minute cooldown. They resolve after
three sequential observations above 40%.

Projection warnings require active context of at least 60% of the model window
and a projected request that would leave less than the configured output
reserve (16,000 tokens by default). Confidence is `low` or `medium`; it is
never `high` in the current release because the companion cannot see the client's full
request body.

Cache-TTL warnings require provider-confirmed TTL telemetry. Idle time alone is
not treated as proof of expiry. When confirmed, the warning says the next
request may rebuild the cached prefix; it requires at least 10K active context
tokens and has a 30-minute cooldown.

Account budget projection is shown only for fresh, schema-valid, authoritative
usage. The capability state is `supported`, `unsupported`, `forbidden`, or
`unknown`; known unsupported and forbidden endpoints are not retried
automatically. Status tiers are healthy (>30% remaining), watch (15–30%), low
(5–15%), and critical (<5%).

## Status, reports, and retention

Use summary, standard, or detailed reporting levels:

```bash
freeinference status --level summary
freeinference status --level standard
freeinference status --level detailed
```

`--compact` is reserved for status-line integrations and `--json` is the
machine-readable contract. A session's bounded `events.jsonl` records
lifecycle types and short sanitized details. Rotation begins at 256 KiB or
1,000 events; sessions older than 30 days become eligible for cleanup. Only
the explicit `refresh` maintenance command invokes stale-session cleanup;
normal hooks do not scan or delete history.

`freeinference failures` aggregates retained incidents by category, model, and
client. It never includes error bodies. The categories include rate limits,
authentication/permission failures, invalid requests, timeouts,
model-not-found, overload, gateway/server errors, network/TLS errors,
cancellation, and output limits.
