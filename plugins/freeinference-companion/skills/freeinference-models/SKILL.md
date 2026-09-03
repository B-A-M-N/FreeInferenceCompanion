---
name: freeinference-models
description: List or inspect the cached FreeInference model catalog for Codex.
argument-hint: "[--model <name>] [--refresh]"
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference models

Run `freeinference models` to inspect the local cached catalog, or add
`--model <name>` for one model. This reads local state and does not make a
request unless `--refresh` is explicitly supplied.

`freeinference models --refresh` makes one authenticated catalog request. Use
it only when needed; it does not send an inference request.
