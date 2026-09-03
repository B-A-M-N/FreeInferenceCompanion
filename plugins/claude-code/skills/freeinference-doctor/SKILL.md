---
name: freeinference-doctor
description: Diagnose FreeInference Companion installation and provider configuration.
argument-hint: "[--json] [--probe --model <name>]"
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference doctor

Run `freeinference doctor --json` when the user explicitly asks for a full
diagnostic. Doctor checks local installation and may contact the configured
metadata endpoints to test reachability and catalog access. It does not send
an inference request unless `--probe --model <name>` is explicitly requested.

`doctor --probe` is a real synthetic inference request and consumes provider
resources; ask for explicit confirmation before using it.

In Claude Code the slash command is:

```text
/freeinference-companion:freeinference-doctor
```

