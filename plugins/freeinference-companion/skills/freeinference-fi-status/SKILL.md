---
name: freeinference-fi-status
description: Show the public FreeInference service status without using credentials or session state.
allowed-tools: Bash
disable-model-invocation: true
---

# FreeInference service status

Run the canonical stateless command:

```bash
freeinference fi-status
```

Use `--json` for structured output, `--problems` (or `--down`) to filter to
down or unknown models, and `--details` for timing and observed-duration
fields. This makes one unauthenticated GET to the public status endpoint. It
does not read session state, send a provider credential, or run inference.

This skill is intentionally explicit because public service status is a
network request, even though it cannot consume an inference slot.
