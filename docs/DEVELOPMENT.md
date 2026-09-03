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

Codex plugin iteration uses the repository marketplace at
`codex-marketplace/.agents/plugins/marketplace.json`. Validate it and the
plugin manifest before testing with Codex:

```bash
python3 /home/bamn/.codex/skills/.system/plugin-creator/scripts/read_marketplace_name.py \
  --marketplace-path codex-marketplace/.agents/plugins/marketplace.json
python3 /home/bamn/.codex/skills/.system/plugin-creator/scripts/validate_plugin.py plugins/codex
```

The authoritative plugin validation in CI includes a real pinned Codex CLI
install/list check in addition to static manifest checks.
