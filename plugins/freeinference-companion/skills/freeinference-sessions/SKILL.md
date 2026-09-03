---
name: freeinference-sessions
description: List locally retained FreeInference Companion sessions from Codex and other clients.
argument-hint: "[--json]"
allowed-tools: Bash
---

# FreeInference sessions

Run `freeinference sessions --json` to list locally retained sessions. This
reads local state only and does not contact FreeInference. Session identifiers
are masked unless `--include-identifiers` is explicitly supplied.
