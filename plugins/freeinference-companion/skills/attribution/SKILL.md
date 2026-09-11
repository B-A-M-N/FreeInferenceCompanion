---
name: attribution
description: Configure FreeInference commit attribution from Codex.
invocation: user
---

Run `freeinference attribution status --json`. Ask the user to choose `off`, `append`, or `standalone`; do not infer. Then run `freeinference attribution set <mode>`. This does not originate Git commits and only affects already-authorized agent commits.
