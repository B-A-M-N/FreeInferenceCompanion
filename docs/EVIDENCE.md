# Release proof

The README screenshots are evidence of the native integration paths, not
simulated product mockups. They were captured from isolated temporary runs of
the shipped binary and plugin flow:

| Client | Native client tested | What is shown |
| --- | --- | --- |
| Claude Code | 2.1.259 | A real FreeInference-backed turn with the Companion status line showing model, cache observation, fresh input, context percentage, and health state |
| Codex | 0.149.0 | A real FreeInference-backed turn with Codex's native footer and an explicit local Companion diagnostic showing provider verification and the unavailable context/cache boundary |

Both runs used the deterministic `qwen3.6-35b` proof model. The Companion was
run with `FI_NO_BACKGROUND=1` and `FI_AUTO_REFRESH=0`; no `doctor`, `refresh`,
or `probe` command was used. The clients made their own inference requests
directly to the configured provider. The Companion did not proxy those
requests or add monitoring requests during capture.

The Codex screenshot is intentionally different from the Claude screenshot:
the Codex plugin is skill-only, so Codex does not provide this Companion with
Claude-equivalent lifecycle, context-token, or cache-token telemetry. The
native Codex footer remains Codex-owned, and unavailable values are shown as
unavailable rather than estimated.

The proof environment was isolated from the repository. Credentials,
temporary configuration, transcript data, and unrelated host state were not
copied into the repository or release assets.
