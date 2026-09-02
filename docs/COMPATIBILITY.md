# Compatibility

This release records capability boundaries explicitly; missing telemetry is
reported as unavailable or unknown rather than inferred.

- Claude Code: current status-line contract supported; reliable current-context
  token totals require Claude Code `>= 2.1.132`. Older or missing versions use
  `used_percentage × context_window_size` when available and mark total-token
  semantics as cumulative or unknown.
- Codex: provider diagnostics and model discovery are supported through the
  selected `[model_providers.<id>]` configuration. Automatic lifecycle,
  context-window, cache-token, and compaction telemetry are unsupported.
- Claude cache telemetry: status-line `current_usage` fields are client-
  observed. Cache percentages are rolling over the current model/context epoch,
  not provider-confirmed miss causes.
- FreeInference runtime routes: Claude uses `/anthropic`; Codex uses the
  OpenAI-compatible `/v1` route. Automatic state requires client-specific
  route and credential evidence.
- Public service status: `freeinference fi-status` performs an unauthenticated
  GET to `https://status.freeinference.org/api/status`; it is separate from
  provider health, account usage, and session telemetry. Its versioned JSON
  contract is schema v1; it reports model states as `up`, `down`, or
  `unknown` and keeps monitor freshness separate from service state. Missing
  telemetry, malformed individual models, and stale cycles are not converted
  into a false outage.
- State schema: version 3. Newer snapshots are rejected; older snapshots are
  migrated without fabricating token semantics.

Known limitations include provider-specific cache policy details, exact cache
miss reasons, any Codex live token/context fields not exposed by Codex, and
Codex profile selection when the active profile is not exposed to child
commands; that selection is reported as unverified.
