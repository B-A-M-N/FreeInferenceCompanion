---
name: freeinference-status
description: Show verified FreeInference provider status and locally available Codex diagnostics.
argument-hint: "[--level summary|standard|detailed]"
allowed-tools: Bash
---

# FreeInference status

Run these local commands together so the result shows the rollout-backed
diagnostics alongside Codex's native footer:

```bash
freeinference status --client codex --level standard
freeinference context --client codex
freeinference cache --client codex --json
```

Use `--level summary` or `--level detailed` for the status command; these
cannot be combined with `--json`. Use `--json` instead when a machine-readable
result is needed. The status command reports
verified provider configuration even when no Codex lifecycle snapshot exists.

The status command reads the latest bounded local Codex rollout record even
when no Companion lifecycle snapshot exists. Context percentage, cache shares,
fresh input, output, and model come from that record; provider identity comes
from the selected Codex configuration. Before a completed turn, metrics are
explicitly pending. Codex's native footer remains the source for its own
model, remaining-context, and current-directory fields. These commands do not
make a provider request.
