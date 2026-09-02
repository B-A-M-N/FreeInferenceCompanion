# Releasing

The authoritative local release gate is:

```bash
make release-check
```

It checks a clean tree, formatting, vet, module integrity/tidiness, unit and
race tests, vulnerability scanning, the average-latency benchmark gate,
plugin JSON/Bash syntax, static cross-builds, and static linkage. `bench-ci`
reports and gates Go benchmark average `ns/op`; it does not calculate p95 and
must not be described as a p95 verification.

Plugin syntax checks are intentionally labeled as syntax checks. CI also
installs the pinned Claude Code validator and runs strict validation. For
local releases, run the same vendor validation explicitly:

```bash
claude plugin validate --strict plugins/claude-code
```

Before publishing, also run the plugin smoke tests, inspect the generated
archives, verify checksums/SBOM/provenance, and confirm that the compatibility
and security documents match the release. Never package a dirty tree or put
credentials in a config fixture.
