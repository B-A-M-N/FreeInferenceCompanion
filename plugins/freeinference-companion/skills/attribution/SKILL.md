---
name: attribution
description: Configure FreeInference commit attribution from Codex.
invocation: user
---

Run `freeinference attribution status --json`. Ask the user to choose one mode and do not infer:

- `off` — never modify commits.
- `append` — add inference provenance only when native agent attribution is present.
- `standalone` — add inference provenance to every eligible agent commit.

Then run `freeinference attribution set <mode>`.

This feature never creates, stages, or amends a commit and only rewrites the visible message of an already-authorized agent commit.
