# Installation

For normal users, install a published release. `make install` is a source-
checkout development command and is documented in [DEVELOPMENT.md](DEVELOPMENT.md).

## Release binary

Download the signed, checksummed `v0.1.0` artifact for your platform from the
[GitHub release](https://github.com/b-a-m-n/freeinference-companion/releases),
along with `checksums.txt`. Verify the exact archive before extracting it:

```bash
VERSION=0.1.0
PLATFORM=linux-amd64       # linux-arm64, darwin-amd64, or darwin-arm64
ARCHIVE="freeinference-companion-${VERSION}-${PLATFORM}.tar.gz"

curl -fLO "https://github.com/b-a-m-n/freeinference-companion/releases/download/v${VERSION}/${ARCHIVE}"
curl -fLO "https://github.com/b-a-m-n/freeinference-companion/releases/download/v${VERSION}/checksums.txt"
grep "  ${ARCHIVE}$" checksums.txt | sha256sum -c -

tar -xzf "${ARCHIVE}"
install -d -m 755 "$HOME/.local/bin"
install -m 755 "freeinference-companion-${VERSION}-${PLATFORM}/freeinference" "$HOME/.local/bin/freeinference"
```

Ensure `$HOME/.local/bin` is on `PATH`, then run:

```bash
freeinference doctor
freeinference status
```

The combined platform ZIP also contains the Claude Code and Codex plugin
trees. The CLI installer can consume a release `marketplace.json` and will
verify the platform ZIP checksum before installing:

```bash
freeinference install --platform linux-amd64
```

## Codex plugin

Codex uses its native marketplace/plugin manager. For a checked-out repository
or an extracted plugin marketplace, install the local marketplace and plugin:

```bash
codex plugin marketplace add /path/to/FreeInferenceCompanion/codex-marketplace
codex plugin add freeinference-companion@freeinference-companion-local
codex plugin list --json
```

The plugin contributes local lifecycle recording and diagnostic skills. It
does not proxy inference traffic or make background API requests by default.
Use `FI_AUTO_REFRESH=1` only when you explicitly want stale metadata refresh;
those refreshes are throttled, coalesced, and circuit-breaker protected.

## HarvardClaude and other local proxies

If a launcher routes Claude through a local compatibility proxy, install or
load the Claude plugin in that launcher's profile and have the launcher export
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
