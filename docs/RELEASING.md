# Releasing

The authoritative local release gate is:

```bash
make release-check
```

It checks a clean tree, formatting, vet, module integrity/tidiness, unit and
race tests, vulnerability scanning, the average-latency benchmark gate,
plugin JSON/Bash syntax, the launch/header trace contract, static cross-builds,
static linkage, and staticcheck. The CI workflow also runs native macOS
runtime tests. `bench-ci`
reports and gates Go benchmark average `ns/op`; it does not calculate p95 and
must not be described as a p95 verification.

Plugin syntax checks are intentionally labeled as syntax checks. CI tests the
Claude Code compatibility matrix with pinned releases. Versions that expose
the non-interactive strict validator run vendor validation; the legacy
`2.1.132` CLI exposes a validation command that can hang on this plugin, so CI
does not invoke that command and reports a notice after the internal manifest
and wrapper checks pass. This keeps the legacy job finite while still testing
the plugin against that release.

For a current Claude Code release, run vendor validation explicitly:

```bash
claude plugin validate --strict plugins/claude-code
```

For a legacy release without `--strict`, run the internal checks instead:

```bash
make plugin-syntax-check
```

Before publishing, also run the plugin clean-install smoke tests (which repeat
the trace contract), inspect the generated archives, verify checksums/SBOM and
the explicitly unsigned `provenance.unsigned.intoto.jsonl` file (sign it with
the release-attestation process before publication), and confirm that the compatibility
and security documents match the release. Never package a dirty tree or put
credentials in a config fixture.
