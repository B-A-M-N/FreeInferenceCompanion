---
name: freeinference-report
description: Generate a sanitized local support report for a FreeInference session.
argument-hint: "[--session <id>] [--format markdown|json]"
allowed-tools: Bash
---

# FreeInference report

Run `freeinference report --format markdown` for a human-readable report or
`freeinference report --format json` for a shareable structured artifact. The
report is assembled from local state and contains no prompts, responses,
credentials, auth headers, repository contents, or raw error bodies.

Review the output before sharing it. Session identifiers are masked unless
`--include-identifiers` is passed. Use `--session <id>` to select a specific
session; it does not change masking.
