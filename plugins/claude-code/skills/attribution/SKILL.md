---
name: attribution
description: Configure FreeInference commit attribution from Claude Code.
invocation: user
---

Run `freeinference attribution status --json`. Present exactly three choices:

1. Off — never modify commits.
2. Append to existing agent attribution.
3. Standalone FreeInference inference attribution.

Use AskUserQuestion. After the user chooses, run `freeinference attribution set off|append|standalone`. Never infer a choice. This does not modify Claude's native `attribution.commit` setting and never originates commits.
