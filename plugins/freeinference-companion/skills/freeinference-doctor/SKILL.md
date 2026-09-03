---
name: freeinference-doctor
description: Diagnose FreeInference Companion and Codex provider configuration.
argument-hint: "[--json] [--probe --model <name>]"
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference doctor

Run `freeinference doctor --json` only when the user explicitly asks for a
full diagnostic. Doctor checks local installation and may contact configured
metadata endpoints for reachability and catalog access. It does not send an
inference request unless `--probe --model <name>` is explicitly requested.

`doctor --probe` is a real synthetic inference request and consumes provider
resources; ask for explicit confirmation before using it.

