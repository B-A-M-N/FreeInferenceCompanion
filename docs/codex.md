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
It is a **skill-only** package: it exposes user-requested provider diagnostics
and model discovery, but does not install lifecycle hooks or record automatic
Codex session telemetry. It does not proxy prompts or select models for you.

After installation, these skills are available:

- `$freeinference-status`
- `$freeinference-models`
- `$freeinference-doctor`
- `$freeinference-report`
- `$freeinference-dashboard`
- `$freeinference-cache`

Codex does not expose a script-backed status line, live context-window counts,
or cache-token metrics through this package. Accordingly, unavailable values
are reported as `unknown` rather than invented. Use `freeinference models`,
`freeinference doctor`, or `freeinference status --client codex` when you
explicitly want the available local/provider state.

For interactive reports, choose the amount of detail explicitly:

```bash
freeinference status --client codex --level summary
freeinference status --client codex --level standard
freeinference status --client codex --level detailed
```

Set the usual level once with `freeinference config set reporting.level
standard`; `FI_REPORTING_LEVEL` is a non-persistent override. These levels
describe only local companion state—Codex-unavailable context and cache
telemetry remains `unknown`.

## References

- [FreeInference integrations](https://doc.freeinference.org/integrations)
- [FreeInference model catalog](https://doc.freeinference.org/models)
- [Codex provider configuration](https://developers.openai.com/codex/config-reference)
- [Codex profiles](https://developers.openai.com/codex/config-advanced)
