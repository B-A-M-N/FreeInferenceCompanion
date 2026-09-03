---
name: fi-status
description: Fetch the public FreeInference service status without using session state or credentials.
allowed-tools: Bash
---

# FreeInference service status

Run the canonical stateless command:

```bash
freeinference fi-status
```

For scripts, use `freeinference fi-status --json`. The command performs one
unauthenticated GET to `https://status.freeinference.org/api/status`; it does
not read an active session, send an API key, or run an inference probe. Human
output shows all models by default; use `--problems` (or `--down`) to filter
to incidents and `--details` for ranges, completion tokens, and current
state duration.

In this plugin the slash command is namespaced by Claude Code as:

```text
/freeinference-companion:fi-status
```

`--refresh` and `--all` are accepted as deprecated compatibility no-ops. The
command is already a direct fetch and does not maintain a local cache.
