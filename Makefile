.PHONY: build test test-race vet staticcheck fmt-check plugin-syntax-check plugin-validate trace-contract-check check release release-check checksums marketplace clean install lint tidy tidy-check mod-verify clean-tree-check security-scan smoke bench bench-ci run package package-smoke package-repro-check plugin-clean-install sbom provenance

BINARY=freeinference
BUILD_DIR=build
# Development builds must not expose a commit hash as the product version.
# Release jobs pass an explicit semver value (for example VERSION=v0.1.0).
VERSION?=0.1.0-dev
COMMIT?=$(shell git rev-parse HEAD 2>/dev/null || echo "dev")
# Release tags conventionally include a leading "v", while the CLI and
# manifests expose canonical semantic versions without it.
BUILD_VERSION=$(patsubst v%,%,$(VERSION))

# The binary embeds its release commit through LDFLAGS. Disable automatic VCS
# stamping so tests and helper builds also work from linked worktrees and other
# source distributions where Go cannot query repository metadata reliably.
GOFLAGS+=-buildvcs=false
export GOFLAGS

# Reproducible builds: set SOURCE_DATE_EPOCH from the latest commit
# so archive timestamps are deterministic. Override via environment.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo "")
ifneq ($(SOURCE_DATE_EPOCH),)
export SOURCE_DATE_EPOCH
endif

LDFLAGS=-ldflags "-s -w -X github.com/b-a-m-n/freeinference-companion/pkg/version.Version=$(BUILD_VERSION) -X main.commit=$(COMMIT)"
STATIC_FLAGS=CGO_ENABLED=0
RELEASE_DIR=release

# Supported MVP platforms: Linux amd64/arm64, macOS amd64/arm64.
PLATFORMS=linux-amd64 linux-arm64 darwin-amd64 darwin-arm64

build:
	$(STATIC_FLAGS) go build -trimpath $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/fi
	@if file $(BUILD_DIR)/$(BINARY) 2>/dev/null | grep -q "dynamically linked"; then \
		echo "error: binary is dynamically linked"; exit 1; \
	fi

build-linux-amd64:
	GOOS=linux GOARCH=amd64 $(STATIC_FLAGS) go build -trimpath $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/fi

build-linux-arm64:
	GOOS=linux GOARCH=arm64 $(STATIC_FLAGS) go build -trimpath $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/fi

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 $(STATIC_FLAGS) go build -trimpath $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/fi

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 $(STATIC_FLAGS) go build -trimpath $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/fi

build-all: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64

# release-check is the authoritative quality gate for both CI and local
# releases. Every gate listed here MUST pass or the release stops. This is the
# single source of truth — the tag workflow depends on this exact target.
release-check: clean-tree-check fmt-check vet staticcheck mod-verify tidy-check test test-race security-scan bench-ci plugin-syntax-check trace-contract-check build-all build
	@if file $(BUILD_DIR)/$(BINARY) 2>&1 | grep -q "statically linked"; then \
		echo "static binary verified"; \
	else \
		echo "error: binary is dynamically linked"; exit 1; \
	fi
	@echo "release gate passed"

# release runs the full quality gate, packages distributable artifacts, and
# validates those exact archives from a clean extraction before succeeding.
release: release-check package package-repro-check package-smoke plugin-clean-install
	@echo ""
	@echo "release $(VERSION) packaged in $(RELEASE_DIR)/"
	@ls -la $(RELEASE_DIR)/

# checksums writes sha256 checksums for every file in the release directory,
# excluding the checksum file itself (which is generated last).
checksums:
	@cd $(RELEASE_DIR) && for f in $$(ls | grep -v '^checksums.txt$$'); do \
		if command -v sha256sum >/dev/null 2>&1; then \
			sha256sum "$$f"; \
		elif command -v shasum >/dev/null 2>&1; then \
			shasum -a 256 "$$f"; \
		else \
			echo "error: neither sha256sum nor shasum found"; exit 1; \
		fi; \
	done > checksums.txt
	@echo "checksums written to $(RELEASE_DIR)/checksums.txt"

# package produces versioned distributable archives. Requires a clean tree
# and a semantic-version tag (no "dirty" suffix) so every release artifact is
# reproducible and traceable to a committed state.
#
# Standalone archives (tar.gz) have a single versioned top-level directory so
# extraction never pollutes the cwd. Each archive contains freeinference, README, and
# LICENSE under freeinference-companion-<version>-<plat>/.
#
# Installer archives (zip) contain the platform binary and the Claude Code
# plugin tree consumed by `freeinference install` / `update`. Codex uses the
# repository marketplace flow documented in docs/codex.md.
#
# Plugin bundles (zip) preserve the vendor's expected layout:
#   .claude-plugin/plugin.json, hooks/, scripts/, skills/, bin/<plat>/freeinference
#   .codex-plugin/plugin.json, skills/ (skill-only; no bundled executable)
# The version is patched only on the staged copy; source manifests are
# never mutated.
package: build-all
	@if echo "$(VERSION)" | grep -qE 'dirty'; then \
		echo "error: refusing to package a dirty version ($(VERSION)); commit and tag first"; \
		exit 1; \
	fi
	@if ! echo "$(VERSION)" | grep -qE '^v?[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "error: VERSION $(VERSION) is not a semantic version; tag a release first"; \
		exit 1; \
	fi
	@REL_VERSION=$$(echo "$(VERSION)" | sed 's/^v//' | sed 's/-.*//'); \
	epoch="$(SOURCE_DATE_EPOCH)"; \
	staging="$$(mktemp -d "$${TMPDIR:-/tmp}/freeinference-release-stage.XXXXXX")"; \
	trap 'rm -rf "$$staging"' EXIT; \
	rm -rf $(RELEASE_DIR); \
	mkdir -p $(RELEASE_DIR); \
	\
	for p in $(PLATFORMS); do \
		os=$$(echo "$$p" | cut -d- -f1); \
		arch=$$(echo "$$p" | cut -d- -f2); \
		archive_name="freeinference-companion-$$REL_VERSION-$$p"; \
		stage_dir="$$staging/$$archive_name"; \
		mkdir -p "$$stage_dir"; \
		install -m 0755 $(BUILD_DIR)/$(BINARY)-$$p "$$stage_dir/$(BINARY)"; \
		cp LICENSE SECURITY.md CHANGELOG.md README.md "$$stage_dir/"; \
		cp -R docs "$$stage_dir/"; \
		rm -f "$$stage_dir"/docs/audit-*.md; \
		archive="$(CURDIR)/$(RELEASE_DIR)/$$archive_name.tar.gz"; \
		if tar --version 2>/dev/null | grep -q 'GNU tar'; then \
			tar --sort=name --mtime="@$$epoch" --owner=0 --group=0 --numeric-owner --use-compress-program='gzip -n' -cf "$$archive" -C "$$staging" "$$archive_name"; \
		else \
			tmp_archive="$$archive.tmp"; \
			rm -f "$$tmp_archive" "$$archive"; \
			if ( cd "$$staging" && LC_ALL=C find "$$archive_name" -print | LC_ALL=C sort | COPYFILE_DISABLE=1 tar --no-recursion --format=ustar --mtime="@$$epoch" --uid=0 --gid=0 --uname=root --gname=root -cf "$$tmp_archive" -T - ) && gzip -n < "$$tmp_archive" > "$$archive"; then \
				rm -f "$$tmp_archive"; \
			else \
				rm -f "$$tmp_archive" "$$archive"; \
				echo "failed to package $$archive_name" >&2; \
				exit 1; \
			fi; \
		fi; \
		echo "packaged $$archive"; \
	done; \
	\
	for p in $(PLATFORMS); do \
		bundle_dir="$$staging/installer-$$p"; \
		mkdir -p "$$bundle_dir/plugins/claude-code/.claude-plugin" "$$bundle_dir/plugins/claude-code/bin/$$p"; \
		install -m 0755 $(BUILD_DIR)/$(BINARY)-$$p "$$bundle_dir/$(BINARY)"; \
		sed "s/\"version\": \".*\"/\"version\": \"$$REL_VERSION\"/" plugins/claude-code/.claude-plugin/plugin.json > "$$bundle_dir/plugins/claude-code/.claude-plugin/plugin.json"; \
		cp -R plugins/claude-code/hooks plugins/claude-code/scripts plugins/claude-code/skills "$$bundle_dir/plugins/claude-code/"; \
		install -m 0755 $(BUILD_DIR)/$(BINARY)-$$p "$$bundle_dir/plugins/claude-code/bin/$$p/$(BINARY)"; \
		find "$$bundle_dir" -exec touch -h -d "@$$epoch" {} +; \
		(cd "$$bundle_dir" && find . -type f -print | LC_ALL=C sort | zip -q -X -@ "$(CURDIR)/$(RELEASE_DIR)/freeinference-companion-$$REL_VERSION-$$p.zip"); \
		echo "packaged installer archive for $$p"; \
	done; \
	\
	stage_claude="$$staging/claude-code"; \
	mkdir -p "$$stage_claude/.claude-plugin"; \
	sed "s/\"version\": \".*\"/\"version\": \"$$REL_VERSION\"/" \
		plugins/claude-code/.claude-plugin/plugin.json \
		> "$$stage_claude/.claude-plugin/plugin.json"; \
	cp -R plugins/claude-code/hooks \
		plugins/claude-code/scripts \
		plugins/claude-code/skills \
		"$$stage_claude/"; \
	mkdir -p "$$stage_claude/bin"; \
	for p in $(PLATFORMS); do mkdir -p "$$stage_claude/bin/$$p"; install -m 0755 "$(BUILD_DIR)/$(BINARY)-$$p" "$$stage_claude/bin/$$p/$(BINARY)"; done; \
	find "$$stage_claude" -exec touch -h -d "@$$epoch" {} +; \
	(cd "$$stage_claude" && find . -type f -print | LC_ALL=C sort | zip -q -X -@ "$(CURDIR)/$(RELEASE_DIR)/freeinference-companion-claude_$$REL_VERSION.zip") && \
	echo "packaged Claude plugin bundle"; \
	\
	stage_codex="$$staging/codex"; \
	mkdir -p "$$stage_codex/.codex-plugin"; \
	sed "s/\"version\": \".*\"/\"version\": \"$$REL_VERSION\"/" \
		plugins/freeinference-companion/.codex-plugin/plugin.json \
		> "$$stage_codex/.codex-plugin/plugin.json"; \
	cp -R plugins/freeinference-companion/skills \
		"$$stage_codex/"; \
	find "$$stage_codex" -exec touch -h -d "@$$epoch" {} +; \
	(cd "$$stage_codex" && find . -type f -print | LC_ALL=C sort | zip -q -X -@ "$(CURDIR)/$(RELEASE_DIR)/freeinference-companion-codex_$$REL_VERSION.zip") && \
	echo "packaged Codex plugin bundle"; \
	\
	cp LICENSE SECURITY.md CHANGELOG.md README.md $(RELEASE_DIR)/; \
	$(MAKE) sbom RELEASE_DIR=$(RELEASE_DIR) VERSION=$(VERSION) STAGE_DIR="$$staging" && \
	$(MAKE) marketplace RELEASE_DIR=$(RELEASE_DIR) VERSION=$(VERSION) && \
	$(MAKE) provenance RELEASE_DIR=$(RELEASE_DIR) VERSION=$(VERSION) COMMIT=$(COMMIT) STAGE_DIR="$$staging" && \
	$(MAKE) checksums RELEASE_DIR=$(RELEASE_DIR) && \
		echo "packaging complete"

# package-repro-check builds the package twice with the same source epoch and
# compares every generated file. This keeps the reproducibility claim honest,
# including generated SBOM and provenance metadata.
package-repro-check: package
	@set -e; tmp="$$(mktemp -d "$${TMPDIR:-/tmp}/freeinference-package-repro.XXXXXX")"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	cp -a $(RELEASE_DIR) "$$tmp/first"; \
	$(MAKE) package VERSION=$(VERSION) COMMIT=$(COMMIT); \
	cp -a $(RELEASE_DIR) "$$tmp/second"; \
	diff -ru "$$tmp/first" "$$tmp/second"; \
	echo "package reproducibility check passed"

# marketplace creates the manifest published with each release. Its checksums
# are derived from the exact combined installer ZIPs created above, so the
# default install/update path and the release assets cannot drift.
marketplace:
	@rel_version=$$(echo "$(VERSION)" | sed 's/^v//' | sed 's/-.*//'); \
	out="$(RELEASE_DIR)/marketplace.json"; \
	hash_file() { \
		if command -v sha256sum >/dev/null 2>&1; then sha256sum "$$1" | awk '{print $$1}'; \
		else shasum -a 256 "$$1" | awk '{print $$1}'; fi; \
	}; \
	{ \
		echo '{'; \
		printf '  "version": "%s",\n' "$$rel_version"; \
		echo '  "platforms": {'; \
		first=1; \
		for p in $(PLATFORMS); do \
			asset="freeinference-companion-$$rel_version-$$p.zip"; \
			hash=$$(hash_file "$(RELEASE_DIR)/$$asset"); \
			test -n "$$hash" || { echo "error: cannot hash $$asset" >&2; exit 1; }; \
			if [ $$first -eq 0 ]; then echo ','; fi; \
			printf '    "%s": {"url": "https://github.com/b-a-m-n/freeinference-companion/releases/download/v%s/%s", "sha256": "%s"}' "$$p" "$$rel_version" "$$asset" "$$hash"; \
			first=0; \
		done; \
		echo; \
		echo '  },'; \
		printf '  "plugin_urls": {"claude-code": "https://github.com/b-a-m-n/freeinference-companion/releases/download/v%s/freeinference-companion-claude_%s.zip", "codex": "https://github.com/b-a-m-n/freeinference-companion/releases/download/v%s/freeinference-companion-codex_%s.zip"}\n' "$$rel_version" "$$rel_version" "$$rel_version" "$$rel_version"; \
		echo '}'; \
	} > "$$out"; \
	python3 -c "import json; json.load(open('$$out')); print('marketplace manifest written to $$out')"

# plugin-bin builds platform binaries into the Claude plugin directory so its
# installed hook wrapper can run without relying on a separate PATH binary.
plugin-bin: build-all
	@mkdir -p plugins/claude-code/bin
	@for p in $(PLATFORMS); do \
		os=$$(echo "$$p" | cut -d- -f1); \
		arch=$$(echo "$$p" | cut -d- -f2); \
		case "$$arch" in x86_64) arch="amd64" ;; esac; \
		bin_dir="plugins/claude-code/bin/$$os-$$arch"; \
		mkdir -p "$$bin_dir"; \
		cp $(BUILD_DIR)/$(BINARY)-$$p "$$bin_dir/$(BINARY)"; \
		echo "copied $$p into plugin bin/$$os-$$arch/"; \
	done

# package-smoke validates the packaged archives: extracts each platform archive
# into a temporary directory, runs the binary, and asserts it executes.
# Validates that source archives have a versioned top-level directory and that
# plugin bundles preserve the vendor-expected manifest directory layout.
package-smoke: package
	@tmp="$$(mktemp -d "$${TMPDIR:-/tmp}/freeinference-pkg-smoke.XXXXXX")"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	cur="$$(go env GOOS)-$$(go env GOARCH)"; \
	REL_VERSION=$$(echo "$(VERSION)" | sed 's/^v//' | sed 's/-.*//'); \
	\
	for p in $(PLATFORMS); do \
		archive_name="freeinference-companion-$$REL_VERSION-$$p"; \
		archive="$(RELEASE_DIR)/$$archive_name.tar.gz"; \
		if [ ! -f "$$archive" ]; then \
			echo "FAIL: $$p archive missing: $$archive"; exit 1; \
		fi; \
		extract="$$tmp/extract-$$p"; \
		mkdir -p "$$extract"; \
		tar -xzf "$$archive" -C "$$extract"; \
		if [ ! -d "$$extract/$$archive_name" ]; then \
			echo "FAIL: $$p archive missing top-level dir $$archive_name"; exit 1; \
		fi; \
		if [ ! -x "$$extract/$$archive_name/$(BINARY)" ]; then \
			echo "FAIL: $$p archive $(BINARY) not executable"; exit 1; \
		fi; \
		if [ ! -f "$$extract/$$archive_name/README.md" ] || [ ! -f "$$extract/$$archive_name/LICENSE" ] || [ ! -f "$$extract/$$archive_name/SECURITY.md" ] || [ ! -f "$$extract/$$archive_name/CHANGELOG.md" ]; then \
			echo "FAIL: $$p archive missing release documentation"; exit 1; \
		fi; \
		test -f "$$extract/$$archive_name/docs/INSTALL.md" || { echo "FAIL: $$p archive missing installation documentation"; exit 1; }; \
		test -z "$$(find "$$extract/$$archive_name/docs" -maxdepth 1 -type f -name 'audit-*.md' -print -quit)" || { echo "FAIL: $$p archive contains internal audit documentation"; exit 1; }; \
		test -f "$$extract/$$archive_name/docs/images/claude-code-native.png" || { echo "FAIL: $$p archive missing Claude native screenshot"; exit 1; }; \
		test -f "$$extract/$$archive_name/docs/images/codex-native.png" || { echo "FAIL: $$p archive missing Codex native screenshot"; exit 1; }; \
		echo "archive OK: $$p"; \
		installer="$(RELEASE_DIR)/$$archive_name.zip"; \
		test -f "$$installer" || { echo "FAIL: $$p installer archive missing: $$installer"; exit 1; }; \
		idir="$$tmp/installer-$$p"; \
		mkdir -p "$$idir"; \
		unzip -q "$$installer" -d "$$idir"; \
		test -x "$$idir/$(BINARY)" || { echo "FAIL: $$p installer missing executable"; exit 1; }; \
		test -f "$$idir/plugins/claude-code/.claude-plugin/plugin.json" || { echo "FAIL: $$p installer missing Claude plugin"; exit 1; }; \
		python3 -c "import json,sys; assert json.load(open(sys.argv[1]))['version'] == sys.argv[2], 'installer plugin version mismatch'" "$$idir/plugins/claude-code/.claude-plugin/plugin.json" "$$REL_VERSION"; \
		test -f "$$idir/plugins/claude-code/hooks/hooks.json" || { echo "FAIL: $$p installer missing Claude hooks"; exit 1; }; \
		test -x "$$idir/plugins/claude-code/scripts/run-hook.sh" || { echo "FAIL: $$p installer missing executable Claude hook runner"; exit 1; }; \
		echo "installer archive OK: $$p"; \
	done; \
	python3 -c "import hashlib,json,pathlib; m=json.load(open('$(RELEASE_DIR)/marketplace.json')); assert set(m['platforms']) == set('$(PLATFORMS)'.split()); assert all(len(info['sha256']) == 64 and hashlib.sha256((pathlib.Path('$(RELEASE_DIR)') / pathlib.PurePosixPath(info['url']).name).read_bytes()).hexdigest() == info['sha256'] for info in m['platforms'].values())"; \
	\
	extract="$$tmp/extract-$$cur"; \
	mkdir -p "$$extract"; \
	tar -xzf "$(RELEASE_DIR)/freeinference-companion-$$REL_VERSION-$$cur.tar.gz" -C "$$extract"; \
	bin="$$extract/freeinference-companion-$$REL_VERSION-$$cur/$(BINARY)"; \
	version_json="$$($$bin version --json)"; \
	python3 -c "import json,sys; assert json.loads(sys.argv[1])['version'] == sys.argv[2], 'binary version mismatch'" "$$version_json" "$$REL_VERSION"; \
	if "$$bin" help >/dev/null 2>&1; then \
		echo "smoke OK: $$cur (binary executes)"; \
	else \
		echo "FAIL: $$cur binary did not execute"; exit 1; \
	fi; \
	\
	for z in $(RELEASE_DIR)/freeinference-companion-claude*.zip; do \
		edir="$$tmp/zip-claude"; \
		rm -rf "$$edir"; \
		mkdir -p "$$edir"; \
		unzip -q "$$z" -d "$$edir"; \
		test -f "$$edir/.claude-plugin/plugin.json" || { echo "FAIL: $$(basename $$z) missing .claude-plugin/plugin.json"; exit 1; }; \
		python3 -c "import json,sys; assert json.load(open(sys.argv[1]))['version'] == sys.argv[2], 'Claude plugin version mismatch'" "$$edir/.claude-plugin/plugin.json" "$$REL_VERSION"; \
		test -d "$$edir/hooks" || { echo "FAIL: $$(basename $$z) missing hooks/"; exit 1; }; \
		test -d "$$edir/scripts" || { echo "FAIL: $$(basename $$z) missing scripts/"; exit 1; }; \
		test -d "$$edir/skills" || { echo "FAIL: $$(basename $$z) missing skills/"; exit 1; }; \
		echo "archive OK: $$(basename $$z)"; \
	done; \
	for z in $(RELEASE_DIR)/freeinference-companion-codex*.zip; do \
		edir="$$tmp/zip-codex"; \
		rm -rf "$$edir"; \
		mkdir -p "$$edir"; \
		unzip -q "$$z" -d "$$edir"; \
		test -f "$$edir/.codex-plugin/plugin.json" || { echo "FAIL: $$(basename $$z) missing .codex-plugin/plugin.json"; exit 1; }; \
		python3 -c "import json,sys; assert json.load(open(sys.argv[1]))['version'] == sys.argv[2], 'Codex plugin version mismatch'" "$$edir/.codex-plugin/plugin.json" "$$REL_VERSION"; \
		test -d "$$edir/skills" || { echo "FAIL: $$(basename $$z) missing skills/"; exit 1; }; \
		test ! -d "$$edir/bin" || { echo "FAIL: $$(basename $$z) unexpectedly contains a binary directory"; exit 1; }; \
		test ! -d "$$edir/hooks" || { echo "FAIL: $$(basename $$z) unexpectedly contains Codex hooks"; exit 1; }; \
		test ! -d "$$edir/scripts" || { echo "FAIL: $$(basename $$z) unexpectedly contains Codex scripts"; exit 1; }; \
		echo "archive OK: $$(basename $$z)"; \
	done; \
	echo "package smoke tests passed"

# plugin-clean-install extracts the Claude plugin ZIP into a temp directory with
# an empty HOME, removes `freeinference` from PATH, and exercises its hook wrapper.
# The wrapper must locate its bundled platform binary and exit zero.
plugin-clean-install: package trace-contract-check
	@tmpdir="$$(mktemp -d "$${TMPDIR:-/tmp}/freeinference-plugin.XXXXXX")"; \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	empty_path="$$tmpdir/empty-bin"; \
	mkdir -p "$$empty_path"; \
	empty_home="$$tmpdir/empty-home"; \
	mkdir -p "$$empty_home"; \
	for z in $(RELEASE_DIR)/freeinference-companion-claude*.zip; do \
		edir="$$tmpdir/extract-claude"; \
		rm -rf "$$edir"; \
		mkdir -p "$$edir"; \
		unzip -q "$$z" -d "$$edir"; \
		test -f "$$edir/.claude-plugin/plugin.json" || { echo "FAIL: $$(basename $$z) missing .claude-plugin/plugin.json"; exit 1; }; \
		test -x "$$edir/scripts/run-hook.sh" || { echo "FAIL: $$(basename $$z) run-hook.sh not executable"; exit 1; }; \
		hooks_file="$$edir/hooks/hooks.json"; \
		test -f "$$hooks_file" || { echo "FAIL: $$(basename $$z) missing hooks/hooks.json"; exit 1; }; \
		plat="$$(uname -s | tr '[:upper:]' '[:lower:]')-$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"; \
		bin="$$edir/bin/$$plat/$(BINARY)"; \
		test -x "$$bin" || { echo "FAIL: $$(basename $$z) missing bundled binary $$plat"; exit 1; }; \
		CLAUDE_PLUGIN_ROOT="$$edir" \
			HOME="$$empty_home" \
			PATH="/usr/bin:/bin" \
			FI_DISABLED=0 \
			bash "$$edir/scripts/run-hook.sh" SessionStart >/dev/null 2>&1; \
		rc=$$?; \
		test "$$rc" -eq 0 || { echo "FAIL: Claude run-hook.sh exited $$rc"; exit 1; }; \
		echo "clean-install OK: $$(basename $$z) ($$plat)"; \
	done; \
	echo "plugin clean-install smoke tests passed"

# sbom generates a minimal SPDX JSON SBOM of the module dependencies.
# For a richer SBOM, install syft (https://github.com/anchore/syft).
sbom:
	@go run ./cmd/sbomgen "$(VERSION)" "$(RELEASE_DIR)/sbom.spdx.json"
	@echo "SBOM written to $(RELEASE_DIR)/sbom.spdx.json"

# provenance generates an in-toto attestation-style provenance file.
# Lists every release artifact as a subject with its SHA-256 digest.
# For signed provenance, install cosign (https://github.com/sigstore/cosign).
provenance:
	@go run ./cmd/provenancegen "$(VERSION)" "$(COMMIT)" "$(RELEASE_DIR)" "$(RELEASE_DIR)/provenance.unsigned.intoto.jsonl"
	@echo "unsigned provenance written to $(RELEASE_DIR)/provenance.unsigned.intoto.jsonl"

install: build
	install -d -m 755 "$${HOME}/.local/bin"
	install -m 755 $(BUILD_DIR)/$(BINARY) "$${HOME}/.local/bin/$(BINARY)"

clean:
	rm -rf $(BUILD_DIR) $(RELEASE_DIR)

test:
	go test ./... -count=1

test-race:
	go test -race -tags=saltloader ./... -count=1

vet:
	go vet ./...

lint: vet staticcheck

# staticcheck is pinned so the release gate is reproducible across runners.
# The release version is injected explicitly by release jobs; development
# builds use the stable `0.1.0-dev` fallback above.
staticcheck:
	@STATICCHECK_VERSION=v0.7.0; \
	bin="$$(go env GOBIN)"; \
	if [ -z "$$bin" ]; then bin="$$(go env GOPATH)/bin"; fi; \
	if [ ! -x "$$bin/staticcheck" ]; then \
		echo "installing staticcheck $$STATICCHECK_VERSION..."; \
		GOBIN="$$bin" go install honnef.co/go/tools/cmd/staticcheck@$$STATICCHECK_VERSION; \
	fi; \
	"$$bin/staticcheck" ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "unformatted files:"; gofmt -l .; exit 1)

# plugin-syntax-check verifies that the plugin manifests parse as JSON and the
# hook wrapper scripts parse as bash. This is a SYNTAX check only — it does
# NOT validate against either vendor's plugin schema, and it does NOT verify
# that a plugin runtime will load plugin-local hooks. Codex is skill-only in
# this release and uses its native marketplace manager.
#
# Remaining gaps requiring real runtime validation:
# - Salt race detection: only exercised when test binaries are built with
#   the `saltloader` build tag (via `make test-race` or `go test -tags=saltloader`).
# - Full activation flow: end-to-end activation across hook, status-line, and
#   background refresh requires real FreeInference credentials and a live
#   endpoint. No unit test can exercise this without network access.
# - Plugin-SDK compatibility: verify the Claude Code hook contract on a real
#   installation after vendor platform updates.
plugin-syntax-check:
	@python3 -c "import json; json.load(open('plugins/claude-code/.claude-plugin/plugin.json')); json.load(open('plugins/claude-code/hooks/hooks.json')); json.load(open('plugins/freeinference-companion/.codex-plugin/plugin.json')); json.load(open('.agents/plugins/marketplace.json')); print('plugin manifests and marketplace are syntactically valid JSON')"
	@bash -n plugins/claude-code/scripts/run-hook.sh && echo "Claude hook wrapper is syntactically valid bash"

# trace-contract-check exercises launch-time ID/header/receipt behavior and
# client-specific activation gates without starting a real coding client.
# Keep this in both release and clean-install validation so a distributable
# bundle cannot regress the documented launcher contract silently.
trace-contract-check:
	@GOCACHE=$${GOCACHE:-/tmp/fic-gocache} go test ./internal/tracing ./internal/runtime ./internal/cli -run 'TestGenerateTraceID|TestValidateTraceID|TestComposeClaudeHeaders|TestReceiptIsPrivate|TestEnsureCodexTraceHeader|TestInspectCodexTraceHeader|TestPrepareLaunch|TestLaunchReceipt|TestSessionEventsNeverContainTraceID|TestReportIncludesTrace' -count=1

# plugin-validate is the legacy name for plugin-syntax-check. It is preserved
# for compatibility with existing CI jobs and scripts, but it does NOT perform
# full plugin validation. New callers should use plugin-syntax-check to make
# the contract honest, or invoke the official vendor validators directly.
plugin-validate: plugin-syntax-check

# tidy-check fails if `go mod tidy` would modify go.mod or go.sum.
# Always restores originals from backup on failure so the checkout is
# byte-identical whether this target passes or fails.
tidy-check:
	@cp go.mod go.mod.check && cp go.sum go.sum.check && \
	trap 'mv go.mod.check go.mod 2>/dev/null; mv go.sum.check go.sum 2>/dev/null; rm -f go.mod.check go.sum.check' EXIT && \
	go mod tidy && \
	if ! diff -q go.mod go.mod.check >/dev/null || ! diff -q go.sum go.sum.check >/dev/null; then \
		echo "error: go.mod or go.sum is out of date — run 'go mod tidy' and commit the result"; \
		exit 1; \
	fi

# mod-verify fails if the module cache has been tampered with.
mod-verify:
	go mod verify

# clean-tree-check refuses to release from a dirty working tree. A release
# must correspond to a reproducible commit, so any uncommitted change is a
# blocker. (CI runs release from a fresh checkout, so this gate is mostly
# load-bearing for local invocations.)
clean-tree-check:
	@if ! git diff --no-color --quiet HEAD 2>/dev/null; then \
		echo "error: working tree has uncommitted changes — commit or stash before releasing"; \
		git status --short; \
		exit 1; \
	fi
	@if [ -n "$$(git ls-files --others --exclude-standard)" ]; then \
		echo "error: untracked files present — commit or stash before releasing"; \
		git ls-files --others --exclude-standard; \
		exit 1; \
	fi
	@if git describe --tags --always --dirty 2>/dev/null | grep -q dirty; then \
		echo "error: git describe reports a dirty tree"; \
		exit 1; \
	fi

# security-scan runs govulncheck. Auto-installs it if missing so the release
# target works on a fresh CI runner. Pinned to a specific version for
# reproducible scanning.
security-scan:
	@GOVULNCHECK_VERSION=v1.1.4; \
	if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "installing govulncheck $$GOVULNCHECK_VERSION..."; \
		go install golang.org/x/vuln/cmd/govulncheck@$$GOVULNCHECK_VERSION; \
	fi
	govulncheck ./...

check: fmt-check vet staticcheck test test-race plugin-syntax-check trace-contract-check mod-verify tidy-check
	@git diff --check
	@echo "all checks passed"

# bench runs the full benchmark suite locally (not CI-gated).
bench:
	go test ./... -bench=. -benchmem -run=^$$

# bench-ci enforces conservative average-latency ceilings for the hot paths.
# Runs the real benchmarks for three seconds to get stable averages and fails
# if either benchmark exceeds its ceiling. Go benchmarks report average ns/op;
# this target intentionally makes no p95 claim.
bench-ci:
	@output=$$(go test ./internal/adapters/ -bench='BenchmarkStatusLineUpdate|BenchmarkUserPromptSubmitNoWarning' -benchtime=3s -count=1 -timeout 120s 2>&1); \
	echo "$$output"; \
	if ! echo "$$output" | grep -q '^Benchmark'; then \
		echo "error: bench-ci ran zero benchmarks — expected BenchmarkStatusLineUpdate and BenchmarkUserPromptSubmitNoWarning"; \
		exit 1; \
	fi; \
	status_ns=$$(echo "$$output" | grep 'BenchmarkStatusLineUpdate' | head -1 | sed -E 's/.* ([0-9]+) ns\/op.*/\1/'); \
	hook_ns=$$(echo "$$output" | grep 'BenchmarkUserPromptSubmitNoWarning' | head -1 | sed -E 's/.* ([0-9]+) ns\/op.*/\1/'); \
	echo "status average = $${status_ns}ns, hook average = $${hook_ns}ns"; \
	if [ "$$status_ns" -gt 10000000 ]; then \
		echo "FAIL: status line average $${status_ns}ns exceeds 10ms ceiling"; exit 1; \
	fi; \
	if [ "$$hook_ns" -gt 10000000 ]; then \
		echo "FAIL: hook average $${hook_ns}ns exceeds 10ms ceiling"; exit 1; \
	fi; \
	echo "latency gate passed"

tidy:
	go mod tidy

run:
	go run ./cmd/fi/ $(ARGS)

# smoke runs the CLI against an isolated, throwaway environment so it never
# reads or mutates the operator's real cache, and never inherits real
# credentials. Each command is asserted to exit cleanly.
smoke: build
	tmp="$$(mktemp -d "$${TMPDIR:-/tmp}/freeinference-smoke.XXXXXX")"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	HOME="$$tmp/home" \
	FI_CACHE_DIR="$$tmp/cache" \
	FI_NO_BACKGROUND=1 \
	FREEINFERENCE_API_KEY="" \
	FREEINFERENCE_BASE_URL="" \
	./$(BUILD_DIR)/$(BINARY) help || { echo "FAIL: freeinference help"; exit 1; }; \
	HOME="$$tmp/home" \
	FI_CACHE_DIR="$$tmp/cache" \
	FI_NO_BACKGROUND=1 \
	FREEINFERENCE_API_KEY="" \
	FREEINFERENCE_BASE_URL="" \
	./$(BUILD_DIR)/$(BINARY) sessions || { echo "FAIL: freeinference sessions"; exit 1; }; \
	echo "Smoke test complete"
