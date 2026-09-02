# Trace correlation

FreeInference Companion can add one opaque, random `X-Session-ID` to a client
launch so FreeInference support can correlate requests from that process. This
is launch-time correlation, not a proxy, request monitor, or installation-wide
identifier. Client traffic remains direct from Claude Code or Codex to
FreeInference.

## What is sent

When a verified FreeInference client is started with `freeinference run`, the
Companion generates a fresh `fic-v1-...` identifier containing at least 128
bits of cryptographic randomness. The ID contains no username, repository,
path, hostname, client session ID, or API key. The launch adds that
`X-Session-ID` plus three fixed classification headers:
`X-FI-Client` (`claude-code` or `codex`), `X-FI-Companion-Version` (the
Companion release), and `X-FI-Workload` (`coding-agent`). Normal requests
never receive `X-Request-ID` from the Companion; FreeInference generates
`X-Request-ID` itself when needed.

Only a receipt-verified launch trace may be persisted in the private session snapshot:
the opaque ID, client, provider, header name, source, endpoint origin, and
launch time. It never records request or response content, raw headers,
transcripts, or credentials. Routine event lines do not contain the trace ID.
Inherited environment markers are display-only and explicitly labeled
unverified; they are never written as durable provenance.

`X-Probe: synthetic` is reserved for the explicit
`freeinference doctor --probe --model <name>` diagnostic inference probe. It
is never part of normal tracing. `freeinference fi-status` remains an
unauthenticated public-status request with no trace or provider headers.

## Enable, disable, and inspect

Tracing is enabled by default for explicit Companion launches only. A normal
`claude` or `codex` invocation is unchanged unless it is started through the
launcher:

```bash
freeinference run claude
freeinference run codex
freeinference trace
freeinference trace --json
```

Disable it persistently with:

```bash
freeinference config set tracing.enabled false
```

`FI_TRACING=0` disables it for one process environment; `FI_TRACING=1` can
enable it for that environment. Disabling tracing stops new injection. It
does not rewrite or delete prior private session metadata.

## Client mechanisms and privacy

Claude Code receives newline-separated correlation entries through its
documented `ANTHROPIC_CUSTOM_HEADERS` variable. Existing unrelated headers
and existing Companion correlation entries are preserved; conflicting
user-owned values cause the launcher to fail open without injecting any
Companion headers. If the custom-header block is malformed, the launcher
also fails open.

Codex receives these headers through the selected provider's documented
`env_http_headers` mapping:

```toml
[model_providers.freeinference.env_http_headers]
"X-Session-ID" = "FI_TRACE_SESSION_ID"
"X-FI-Client" = "FI_TRACE_CLIENT"
"X-FI-Companion-Version" = "FI_TRACE_COMPANION_VERSION"
"X-FI-Workload" = "FI_TRACE_WORKLOAD"
```

Install the Codex mapping explicitly:

```bash
freeinference trace setup codex
freeinference trace uninstall codex
```

The equivalent `--client codex` form is also accepted.

Setup keeps a mode-preserving backup and uses a lock, refuses conflicting
existing mappings, and uninstall restores the original file only when the
mapping is still Companion-owned. The launcher only inspects these mappings; it
does not mutate Codex configuration during every run. Setup preserves
unrelated TOML/comments and never replaces an existing Companion correlation
mapping. Codex trace injection is gated on the selected provider being
verified, using its approved FreeInference URL and its configured `env_key`;
an OpenAI provider, off-host provider, or unverified profile receives no
correlation headers.

Launch handoff receipts are short-lived private files (directory `0700`, file
`0600`) consumed by the SessionStart hook. Receipt paths are constrained to
the Companion-owned temporary directory before reading or deletion.

For upstream semantics, see the [FreeInference API header
documentation](https://doc.freeinference.org/api_headers) and the [Claude
custom environment header documentation](https://code.claude.com/docs/en/env-vars).
