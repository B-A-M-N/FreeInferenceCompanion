# Installation

For normal users, install a published release. `make install` is a source-
checkout development command and is documented in [DEVELOPMENT.md](DEVELOPMENT.md).

## Release binary

Download the checksummed release artifact for your platform from the
[GitHub releases](https://github.com/B-A-M-N/FreeInferenceCompanion/releases),
along with `checksums.txt`. Set `VERSION` to the release you are installing
and verify the exact archive before extracting it:

```bash
VERSION=0.1.1
PLATFORM=linux-amd64       # linux-arm64, darwin-amd64, or darwin-arm64
ARCHIVE="freeinference-companion-${VERSION}-${PLATFORM}.tar.gz"

curl -fLO "https://github.com/B-A-M-N/FreeInferenceCompanion/releases/download/v${VERSION}/${ARCHIVE}"
curl -fLO "https://github.com/B-A-M-N/FreeInferenceCompanion/releases/download/v${VERSION}/checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  grep "  ${ARCHIVE}$" checksums.txt | sha256sum -c -
else
  grep "  ${ARCHIVE}$" checksums.txt | shasum -a 256 -c -
fi

tar -xzf "${ARCHIVE}"
install -d -m 755 "$HOME/.local/bin"
install -m 755 "freeinference-companion-${VERSION}-${PLATFORM}/freeinference" "$HOME/.local/bin/freeinference"
```

Ensure `$HOME/.local/bin` is on `PATH`, then run:

```bash
freeinference status
```

`freeinference doctor` is optional. When FreeInference is active, it performs
one bounded authenticated model-catalog check in addition to local checks. It
does not query health/account/public-status endpoints or send an inference
request unless `--probe --model <name>` is explicitly supplied.

The combined platform ZIP contains the Claude Code plugin tree. Codex is
installed separately through its native marketplace manager. The CLI installer
can consume a release `marketplace.json` and will verify the platform ZIP
checksum before installing:

```bash
freeinference install --platform linux-amd64
```

## Codex plugin

Codex uses its native marketplace/plugin manager. Install the public repository
marketplace and plugin explicitly:

```bash
codex plugin marketplace add B-A-M-N/FreeInferenceCompanion --ref master
codex plugin add freeinference-companion@freeinference-companion
codex plugin list --json
```

That installs the Companion skills, not a second Codex runtime. To configure
Codex itself to use FreeInference, follow [Codex with
FreeInference](codex.md), which covers the OpenAI-compatible `/v1` endpoint,
the environment credential, `wire_api = "responses"`, model profiles, and the
optional trace-header mapping.

The plugin contributes diagnostic skills only. It does not install lifecycle
hooks, proxy inference traffic, or make background API requests by default.
Use `FI_AUTO_REFRESH=1` only when you explicitly want stale metadata refresh;
those refreshes are throttled, coalesced, and circuit-breaker protected.

## Additional client environments

`freeinference install` targets canonical `~/.claude` and `~/.codex`, then
reconciles alternate Claude/Codex environments whose selected configuration
routes to an approved FreeInference `/v1` endpoint. Bounded discovery examines
`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, immediate hidden Codex homes under `$HOME`,
and `$XDG_CONFIG_HOME` to two directory levels. It does not crawl projects,
backups, or arbitrary launcher scripts.

For a root discovery cannot infer, register it explicitly:

```bash
freeinference integrations add --client claude-code --root /path/to/profile
freeinference integrations add --client codex --root /path/to/codex-home
freeinference integrations list
freeinference integrations remove --client codex --root /path/to/codex-home
```

Loopback-backed Codex profiles are candidates, never automatic integrations.
Register them explicitly with an approved upstream attestation:

```bash
freeinference integrations add --client codex --root /path/to/profile \
  --proxy-upstream https://freeinference.org/v1
```

Diagnose one profile, including generated model capability flags:

```bash
freeinference integrations diagnose --client codex --root /path/to/profile [--json]
```

A usable Codex plugin environment must allow both:

```json
{"include_skills_usage_instructions": true, "include_plugin_usage_instructions": true}
```

FIC detects blocking catalogs read-only; it never rewrites an
externally generated `models.json`. Use
`freeinference install --no-integration-discovery` to restrict fan-out to
canonical roots. Existing unowned Companion directories are never overwritten.
Already-installed profiles are upgraded on later installs, including when the
core release version is unchanged.

## HarvardClaude and other local proxies

If a launcher routes Claude through a local compatibility proxy, load the
Claude plugin in that launcher's profile and have the launcher export
the verified upstream route:

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:8765 \
FI_PROXY_UPSTREAM_URL=https://freeinference.org/anthropic \
FI_ALLOW_INSECURE_LOCALHOST=1
```

`FI_PROXY_UPSTREAM_URL` is required in addition to the loopback URL. This is
an explicit integration declaration, not a provider guess: the Companion
ignores `FI_PROVIDER` and a bare localhost endpoint. When the declaration is
valid, status and lifecycle surfaces identify the effective FreeInference
origin; when it is absent or wrong, they remain silent.
