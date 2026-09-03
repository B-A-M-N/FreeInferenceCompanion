---
name: freeinference-doctor
description: Diagnose FreeInference Companion installation and provider configuration.
argument-hint: "[--json] [--probe --model <name>]"
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference doctor

Run `freeinference doctor --json` when the user explicitly asks for a full
diagnostic. When FreeInference is active, doctor performs one bounded
authenticated model-catalog check in addition to local checks. It does not
query health/account/public-status endpoints or send an inference request
unless `--probe --model <name>` is explicitly requested.

`doctor --probe` is a real synthetic inference request and consumes provider
resources; ask for explicit confirmation before using it.

In Claude Code the slash command is:

```text
/freeinference-companion:freeinference-doctor
```
