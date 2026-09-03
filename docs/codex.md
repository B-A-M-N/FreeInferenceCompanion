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
It is intentionally skill-only: it provides user-requested provider
diagnostics, model-discovery guidance, and setup help without installing
lifecycle hooks, proxying prompts, rewriting requests, or adding inference
calls. Metadata refresh is disabled by default; `FI_AUTO_REFRESH=1` is an
explicit opt-in for throttled, detached refresh work from the CLI's supported
lifecycle paths (Codex itself does not install those hooks).

Install it through Codex's supported marketplace flow:

```bash
codex plugin marketplace add B-A-M-N/FreeInferenceCompanion --ref master
codex plugin add freeinference-companion@freeinference-companion
codex plugin list --json
```

Updating the marketplace or plugin later is explicit, so Codex never silently
changes the package.

Codex also owns a native footer. To configure Codex to show its own model,
remaining-context, and current-directory items while preserving existing
footer items:

```bash
freeinference codex-footer install
freeinference codex-footer status
freeinference codex-footer uninstall
```

This is native Codex rendering, not scraped screen telemetry. The companion
does not treat `context-remaining` as a hook field or as live plugin context
usage.

The stateless service command is available regardless of provider activation:

```bash
freeinference fi-status
freeinference fi-status --json
```

It makes only an unauthenticated GET to `https://status.freeinference.org/api/status`.

Codex provider/session boundaries are intentional:

- Supported: explicit local session inspection, provider configuration
  diagnostics, model discovery, native Codex footer configuration, `doctor`,
  `dashboard`, and public `fi-status`.
- Unsupported: live context percentage, cache read/write counts, and
  compaction effectiveness.

`freeinference context --client codex` and `freeinference cache --client codex`
therefore report `unavailable`; they do not infer zeros from absent Codex
telemetry.

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

Codex does not expose an arbitrary script-backed FreeInference status line,
live context-window counts, or cache-token metrics through this package.
Accordingly, unavailable values are reported as `unavailable` rather than
invented. Use `freeinference models`,
`freeinference doctor`, or `freeinference status --client codex` when you
explicitly want the available local/provider state. Codex-unavailable values
are reported as `unavailable`, not `unknown` or zero. `$freeinference-status`
also presents the verified provider configuration and this explicit
availability boundary by running the local `status`, `context`, and `cache`
commands together. It does not create a session record or make a provider
request.

For interactive reports, choose the amount of detail explicitly:

```bash
freeinference status --client codex --level summary
freeinference status --client codex --level standard
freeinference status --client codex --level detailed
```

Set the usual level once with `freeinference config set reporting.level
standard`; `FI_REPORTING_LEVEL` is a non-persistent override. These levels
describe only local companion state—Codex-unavailable context and cache
telemetry remains `unavailable`.

## References

- [FreeInference integrations](https://doc.freeinference.org/integrations)
- [FreeInference model catalog](https://doc.freeinference.org/models)
- [Codex provider configuration](https://developers.openai.com/codex/config-reference)
- [Codex profiles](https://developers.openai.com/codex/config-advanced)
