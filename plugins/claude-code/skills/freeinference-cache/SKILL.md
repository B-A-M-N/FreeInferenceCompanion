---
name: freeinference-cache
description: Explain locally observed Claude cache behavior and likely causes.
allowed-tools: Bash
---

# FreeInference cache diagnostics

Run `freeinference cache` for the current Claude session, or
`freeinference cache --json` for structured output. The analysis uses the
bounded status-line observation window and labels its conclusions as
client-observed or heuristic. Missing cache fields are unknown, never zero.

Cache classifications are evidence-based possibilities, not provider claims;
the command cannot prove a server-side TTL or exact miss cause.
