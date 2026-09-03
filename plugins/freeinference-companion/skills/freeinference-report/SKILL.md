---
name: freeinference-report
description: Generate a sanitized local support report for a Codex session.
argument-hint: "[--session <id>] [--format markdown|json]"
allowed-tools: Bash
---

# FreeInference report

Run `freeinference report --client codex --format markdown`, or use
`--format json` for a structured artifact. The report is assembled from local
state and contains no prompts, responses, credentials, auth headers,
repository contents, or raw error bodies. Codex context and cache fields are
reported as `unavailable`, not guessed.

Review the output before sharing it. Session identifiers are masked by
default.

