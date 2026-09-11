---
name: attribution
description: Configure FreeInference commit attribution from Claude Code.
invocation: user
allowed-tools: Bash
---

Run `freeinference attribution status --json`, then present exactly these choices with AskUserQuestion:

1. **Off** — never modify commits.
2. **Append to existing agent attribution** — add inference provenance only when native agent attribution is present.
3. **Standalone inference attribution** — add inference provenance to every eligible agent commit.

After the user chooses, run:

```bash
freeinference attribution set off|append|standalone
```

Never infer a choice, never modify Claude's native `attribution.commit`, and never create, stage, or amend a commit.
