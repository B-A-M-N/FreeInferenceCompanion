# Changelog

All notable changes to FreeInference Companion are recorded here.

## 0.1.6 — 2026-09-11

### Fixed

- Let explicit Codex integration repair a stale fan-out checksum after a core
  payload reinstall when the current directory still matches verified core
  ownership.

## 0.1.5 — 2026-09-11

### Fixed

- Make the fail-open Claude attribution timeout prompt on slower macOS runners.

## 0.1.4 — 2026-09-11

Installer migration fix.

### Fixed

- Explicit Codex environment integration now adopts a pre-existing payload
  when it matches verified core ownership, while continuing to reject changed
  or unowned directories.

## 0.1.3 — 2026-09-11

Codex observability and install parity release.

### Added

- Lifecycle-enabled Codex plugin payload with standard hooks, an executable
  runner, and platform-bundled binaries.
- Bounded local Codex rollout parsing for model, context-window, fresh-input,
  cache-read/cache-write, and output counters.
- Rich Codex status/render/report/context/cache surfaces, including
  `codex-footer render` for tmux status segments alongside the native footer.

### Fixed

- Codex installs no longer ship as skill-only or report rollout-backed metrics
  as permanently unavailable.
- Installer, doctor, package-smoke, and plugin-clean-install checks now verify
  the complete Codex hooks/runner/binary payload.

## 0.1.2 — 2026-09-11

Feature and hardening release following the first public release.

### Added

- Automatic discovery and explicit registration for multiple Claude Code and
  Codex configuration roots, with read-only route and model-capability
  diagnostics.
- Safe commit attribution for eligible Claude Code commits, including
  fail-open hook behavior and idempotent provenance footers.
- Bundled Codex skill payload and native marketplace registration with durable
  payload-versus-registration status and retry guidance.

### Fixed and hardened

- Corrected the canonical GitHub repository URL in the installer default,
  generated marketplace manifest, release documentation, SBOM, and provenance
  metadata. The Go module import path remains unchanged.
- Made the hosted-runner benchmark gate tolerant of transient filesystem
  contention while retaining a conservative 20 ms CI ceiling for the local
  hook and status-line paths.
- Enforced client-specific endpoint matching, legacy Claude route migration
  guidance, loopback proxy attestation binding, and fail-closed catalog checks.
- Added transactional ownership, rollback, legacy-path cleanup, symlink
  defenses, and cross-platform installer coverage.

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
