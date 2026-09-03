---
name: freeinference-sessions
description: List locally retained FreeInference Companion sessions.
argument-hint: "[--json]"
allowed-tools: Bash
---

# FreeInference sessions

Run `freeinference sessions --json` to list sessions retained in local state.
The command reads local files only and does not contact FreeInference. Session
identifiers are masked unless `--include-identifiers` is explicitly supplied.
