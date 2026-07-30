# Development

The repository uses Go and builds a static `freeinference` binary. Common
checks are:

```bash
make build
make test
make test-race
make check
```

`make install` installs the current checkout into `$HOME/.local/bin`; it is
not the recommended end-user release path. `make package-smoke` and
`make plugin-clean-install` extract the generated archives and verify bundled
binary/plugin layouts. `make release` runs the release gate, packaging,
reproducibility, archive smoke tests, and clean plugin-install checks.
The publication checklist is in [RELEASING.md](RELEASING.md).

Codex plugin iteration uses the repository marketplace at
`.agents/plugins/marketplace.json`. Validate it and the
plugin manifest before testing with Codex:

```bash
make plugin-syntax-check
make plugin-validate
```

These repository-local checks are the portable baseline. If you have the
Codex plugin-creator tooling installed separately, run its validators from
that installation rather than relying on an author-specific filesystem path.

The authoritative plugin validation in CI includes a real pinned Codex CLI
install/list check in addition to static manifest checks.
