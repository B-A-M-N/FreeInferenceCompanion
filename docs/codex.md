# Codex with FreeInference

This guide configures the Codex CLI to use FreeInference directly, then
explains the optional FreeInference Companion plugin.

## 1. Configure the FreeInference provider

Codex uses FreeInference's OpenAI-compatible Responses API at
`https://freeinference.org/v1`. This is different from Claude Code's
Anthropic-compatible endpoint; do not use `https://freeinference.org/anthropic`
in Codex.

Keep the credential in your shell environment or a secrets manager. Do not
put it in `~/.codex/config.toml`:

```bash
export FREEINFERENCE_API_KEY='Free_Inference_API'
```

Add the provider once to `~/.codex/config.toml`. Preserve any unrelated
settings already in that file.

```toml
model_provider = "freeinference"

[model_providers.freeinference]
name = "FreeInference"
base_url = "https://freeinference.org/v1"
env_key = "FREEINFERENCE_API_KEY"
wire_api = "responses"
```

`wire_api = "responses"` is required because Codex custom providers use the
Responses protocol. The configuration above was verified with Codex and
FreeInference's `glm-5.1` model.

For launch-time support correlation, the Companion uses Codex's documented
environment-header mapping. Install it explicitly; `freeinference run codex`
only verifies the mapping and fails open when setup is absent:

```toml
[model_providers.freeinference.env_http_headers]
"X-Session-ID" = "FI_TRACE_SESSION_ID"
```

Use the reversible lifecycle commands:

```bash
freeinference trace setup codex
freeinference trace uninstall codex
```

The equivalent `--client codex` form is also accepted.

Setup uses a one-time recovery backup, preserves unrelated configuration,
refuses conflicts, and uninstall removes the Companion-owned mapping only
when it is still unchanged.

## 2. Add model profiles

Codex profiles are small configuration layers. Create one file per model in
`~/.codex`; each file needs only its model selection.

```toml
# ~/.codex/glm.config.toml
model = "glm-5.1"
```

```toml
# ~/.codex/long-context.config.toml
model = "minimax-m3"
```

```toml
# ~/.codex/coding.config.toml
model = "kimi-k2.7-code"
```

Start a session with the desired profile:

```bash
codex --profile glm
codex --profile long-context
codex --profile coding
```

For a one-off selection, use `codex --model glm-5.1` instead. Codex loads the
base `~/.codex/config.toml` first and then layers
`~/.codex/<profile>.config.toml` on top.

## 3. Select models from the correct catalog

Model availability is endpoint-specific. Add Codex profiles only for IDs
returned by FreeInference's OpenAI-compatible catalog:

```bash
freeinference models --refresh
```

You can also query `https://freeinference.org/v1/models` directly. A model
that is only available from the Anthropic-compatible endpoint belongs in a
Claude Code configuration, not a Codex profile.

## 4. Optional: FreeInference Companion plugin

The companion plugin is separate from the model-provider configuration above.
It installs standard local Codex lifecycle hooks, diagnostic skills, and a
bounded rollout reader. The hooks record only sanitized lifecycle metadata;
the rollout reader extracts model, session, context-window, input/cache, and
output counters from Codex's local JSONL records. It does not proxy prompts,
rewrite requests, scrape the screen, or add inference calls. Metadata refresh
is disabled by default; `FI_AUTO_REFRESH=1` is an explicit opt-in for
throttled, detached refresh work.

Install it through Codex's supported marketplace flow:

```bash
codex plugin marketplace add B-A-M-N/FreeInferenceCompanion --ref master
codex plugin add freeinference-companion@freeinference-companion
codex plugin list --json
```

Updating the marketplace or plugin later is explicit, so Codex never silently
changes the package.

When `freeinference install` or `update` installs the bundled payload, it
records the local payload, marketplace registration, and plugin registration
as separate states. If Codex is not installed or native registration fails,
the payload may be present while native registration remains incomplete; rerun
the command after fixing Codex. Codex may require a new session before the
newly registered skills are visible.

Codex also owns a native footer. To configure Codex to show model/reasoning,
remaining context, and current directory while preserving existing footer
items:

```bash
freeinference codex-footer install
freeinference codex-footer status
freeinference codex-footer uninstall
```

This is native Codex rendering, not scraped screen telemetry. The companion
does not treat native footer fields as hook fields. Its richer cache/freshness
line uses the latest bounded local rollout record instead; Codex's native
status-line schema has no `CODEX_HOME`/profile-name or script-backed custom
item.

To show the rich line outside Codex's native footer, use the first-class
host-neutral surface command. HarvardCodex and other launchers can feed it to
tmux, terminal titles, or another status host:

```bash
freeinference codex-surface render --color=never
```

The command resolves the exact current Codex session through the local
launch-to-session binding (or explicit session pointer) and emits no line when
that session cannot be proven.

The command is local-only and returns no line until a completed rollout usage
record exists.

Automatic rollout-backed rendering is session-specific. The Companion resolves
the rollout whose verified `session_meta` matches the current session and never
falls back to another session. For manual diagnostics, commands may still select
the newest rollout when no explicit session is supplied.

The stateless service command is available regardless of provider activation:

```bash
freeinference fi-status
freeinference fi-status --json
```

It makes only an unauthenticated GET to `https://status.freeinference.org/api/status`.

Codex provider/session boundaries are intentional:

- Supported: lifecycle session recording, rollout-backed context percentage,
  cache read/write counts, provider configuration diagnostics, model discovery,
  native Codex footer configuration, `doctor`, `dashboard`, and public
  `fi-status`.
- Still unavailable: server-side cache policy and compaction effectiveness;
  those require provider-side evidence that local rollout records do not
  contain.

`freeinference context --client codex` and `freeinference cache --client codex`
read the latest local rollout counters; before the first completed turn they
report an explicit pending/unavailable state rather than inferring zeros.

When Codex does not expose its active profile to child commands, provider
selection is reported as unverified. The companion remains fail-closed rather
than treating a generic FreeInference key as proof that the current Codex
session uses that provider.

To launch Codex with a fresh per-process trace correlation, use the explicit
launcher. It does not proxy traffic:

```bash
freeinference run codex
freeinference trace --client codex --json
```

Disable new trace injection with `freeinference config set tracing.enabled
false` or `FI_TRACING=0`. If the selected provider is unverified, off-host, or
already has a different `X-Session-ID` mapping, the launcher starts Codex
normally and does not replace that mapping.


After installation, these skills are available in the Codex TUI:

- `$freeinference` (router and full reference)
- `$freeinference-status`
- `$freeinference-fi-status`
- `$freeinference-models`
- `$freeinference-doctor`
- `$freeinference-report`
- `$freeinference-cache`
- `$freeinference-sessions`
- `$freeinference-refresh`

Codex's native footer still does not accept an arbitrary script-backed status
line. The Companion therefore preserves that footer and exposes the rich line
through `freeinference codex-footer render`, `freeinference status`, and the
optional tmux launcher integration. The line uses bounded local rollout data;
it does not create a provider request. `$freeinference-status` presents the
same rollout-backed status, context, and cache diagnostics together.

For interactive reports, choose the amount of detail explicitly:

```bash
freeinference status --client codex --level summary
freeinference status --client codex --level standard
freeinference status --client codex --level detailed
```

Set the usual level once with `freeinference config set reporting.level
standard`; `FI_REPORTING_LEVEL` is a non-persistent override. These levels
describe only local companion state. Codex context/cache telemetry is marked
with its rollout source and remains pending until a completed usage event is
present.

## References

- [FreeInference integrations](https://doc.freeinference.org/integrations)
- [FreeInference model catalog](https://doc.freeinference.org/models)
- [Codex provider configuration](https://developers.openai.com/codex/config-reference)
- [Codex profiles](https://developers.openai.com/codex/config-advanced)
