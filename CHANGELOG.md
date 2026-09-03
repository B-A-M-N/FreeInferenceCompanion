# Changelog

All notable changes to FreeInference Companion are recorded here.

## 0.1.0 — 2026-09-03

First public stable release.

### Added

- Local Claude Code lifecycle/status-line observation with context and cache
  diagnostics.
- Explicit Codex provider diagnostics, model discovery, setup guidance, and
  native marketplace skills. The Codex plugin is skill-only and installs no
  lifecycle hooks.
- Sanitized reports and retained failure summaries for troubleshooting without
  prompt, response, transcript, credential, or raw-header collection.
- Provider health, public-status, account-usage, trace-correlation, and
  rate-limit-aware metadata refresh surfaces.
- Cross-platform Linux amd64/arm64 and macOS amd64/arm64 release archives,
  checksums, SPDX SBOM, and unsigned provenance metadata.

### Safety and compatibility

- Ordinary hooks, status rendering, plugin installation, and Codex skill
  installation make no provider API calls.
- Metadata refresh is disabled by default; explicit refreshes are bounded,
  coalesced, spaced, and circuit-breaker protected.
- Synthetic inference probes are explicit, opt-in, and never automatic.
- Unknown and unsupported telemetry remains unknown or unavailable rather than
  being fabricated.
- Claude compatibility is validated against the supported CI matrix; Codex
  context/cache telemetry is not available through the plugin.

### Known limitations

- The Companion cannot observe complete outgoing prompts or server-side cache
  policy, so cache causes and future context projections are heuristics.
- Codex does not expose live context, cache-token, or compaction-effectiveness
  telemetry through this integration.
- The first release does not publish signed provenance automatically; the
  generated provenance file is explicitly unsigned and must be attested by
  the release operator if required.
