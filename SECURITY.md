# Security

## Supported versions

Beginning with v0.1.0, the latest tagged release is supported. Older releases
may not contain current activation, credential-safety, or state-migration
fixes.

## Private reports

Please report suspected vulnerabilities privately through [GitHub Security
Advisories](https://github.com/B-A-M-N/FreeInferenceCompanion/security/advisories/new).
Do not publish credentials, private prompts, or an exploit before a fix is
available. Include the affected version, operating system, reproduction steps,
and sanitized logs.

## Data and credentials

The companion reads provider credentials from the environment and keeps them
in memory only for the request. It never intentionally persists API keys,
authorization headers, prompts, responses, transcripts, or raw error bodies.
Local state may contain sanitized model/session metadata, nullable token
measurements, cache shares, lifecycle timestamps, and short failure categories.
Session identifiers are masked in normal output and hashed in event logs.

### Defense in depth

- Reports and account-usage renders use explicit allowlists; unknown upstream
  fields are dropped rather than redacted after the fact.
- Persisted strings pass through pattern-based redaction for key shapes such
  as `hyi-*`, `sk-*`, bearer tokens, environment assignments, and labeled JSON
  secret fields.
- State files are `0600`, state directories are `0700`, and session directory
  names are SHA-256 hashes of session IDs. Path-shaped client inputs such as
  `cwd` and `transcript_path` are not persisted.
- Catalog responses are bounded at 2 MiB, health responses at 1 MiB, error
  bodies at 64 KiB, and synthetic probe bodies at 1 MiB.

There is no encryption at rest in v0.1.0 because the local state contains no
credentials and an encryption key stored on the same machine would generally
be available to an attacker who can read the cache directory. File ownership
is the boundary; an OS keystore would be the appropriate next step if the
companion ever needed to persist refresh tokens.

`TestSecretNeverPersistsOrRenders` in `cmd/fi/security_test.go` walks persisted
files and output paths as a regression guard against secret-shaped data
appearing in state, events, reports, doctor output, or renders.

Automatic hooks and status-line updates make no inference or monitoring
network calls. `fi-status` is the exception: it makes one unauthenticated GET
to the public status endpoint and sends no provider credential. Explicit
`doctor --probe` is a user-requested inference probe.

## Trace correlation

Explicit `freeinference run claude` and `freeinference run codex` launches may
attach a fresh cryptographically random `X-Session-ID` so FreeInference can
correlate requests from one client process. The identifier contains no user,
repository, path, hostname, client session, or API-key material. It increases
linkability between requests in that launch, which is why it is documented,
configurable, and never used as a durable installation identifier. Traffic
still goes directly from the client to FreeInference; the Companion is not a
proxy and does not record request content.

Only trace metadata is retained in private session state. The Companion does
not retain raw headers, prompts, responses, transcripts, or credentials, and
does not place raw trace IDs in routine event lines. Disable new launch
injection with `freeinference config set tracing.enabled false` or
`FI_TRACING=0`. See [Trace correlation](docs/TRACING.md) and FreeInference's
[API header documentation](https://doc.freeinference.org/api_headers).

If you believe a credential was exposed, revoke it first, preserve only
sanitized evidence, and mention the affected endpoint and version in the
private report.
