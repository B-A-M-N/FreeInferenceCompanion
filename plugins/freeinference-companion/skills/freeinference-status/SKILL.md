---
name: freeinference-status
description: Show verified FreeInference provider status and locally available Codex diagnostics.
argument-hint: "[--level summary|standard|detailed]"
allowed-tools: Bash
---

# FreeInference status

Run `freeinference status --client codex` for the current provider/session
diagnostic. Use `--level summary`, `standard`, or `detailed`; add `--json` for
machine-readable output.

This marketplace plugin is skill-only and installs no Codex lifecycle hooks.
Provider identity can be confirmed from Codex configuration, while context and
cache telemetry remain `unavailable` because Codex does not expose those
measurements here. The command does not make a provider request.

