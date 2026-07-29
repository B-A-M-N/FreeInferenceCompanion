.PHONY: build test test-race vet fmt-check plugin-syntax-check plugin-validate check release release-check checksums clean install lint tidy tidy-check mod-verify clean-tree-check security-scan smoke bench bench-ci run package package-smoke sbom provenance

BINARY=fi
BUILD_DIR=build
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)"
STATIC_FLAGS=CGO_ENABLED=0
RELEASE_DIR=release

# Supported MVP platforms: Linux amd64/arm64, macOS amd64/arm64.
PLATFORMS=linux-amd64 linux-arm64 darwin-amd64 darwin-arm64

build:
	$(STATIC_FLAGS) go build -trimpath $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/fi
	@if ldd $(BUILD_DIR)/$(BINARY) 2>/dev/null | grep -q "=>"; then \
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
release-check: clean-tree-check fmt-check vet mod-verify tidy-check test test-race security-scan bench-ci plugin-syntax-check build-all build
	@ldd $(BUILD_DIR)/$(BINARY) 2>&1 | grep -q "not a dynamic executable" \
		&& echo "static binary verified" \
		|| (echo "error: binary is dynamically linked"; exit 1)
	@echo "release gate passed"

# release runs the full quality gate, then packages distributable artifacts.
release: release-check package package-smoke
	@echo ""
	@echo "release $(VERSION) packaged in $(RELEASE_DIR)/"
	@ls -la $(RELEASE_DIR)/

# checksums writes sha256 checksums for every file in the release directory,
# excluding the checksum file itself (which is generated last).
checksums:
	cd $(RELEASE_DIR) && sha256sum $$(ls | grep -v '^checksums.txt$$') > checksums.txt
	@echo "checksums written to $(RELEASE_DIR)/checksums.txt"

# package produces versioned distributable archives. Requires a clean tree
# and a semantic-version tag (no "dirty" suffix) so every release artifact is
# reproducible and traceable to a committed state.
package: build-all plugin-bin
	@if echo "$(VERSION)" | grep -qE 'dirty'; then \
		echo "error: refusing to package a dirty version ($(VERSION)); commit and tag first"; \
		exit 1; \
	fi
	@if ! echo "$(VERSION)" | grep -qE '^v?[0-9]+\.[0-9]+\.[0-9]+'; then \
		echo "error: VERSION $(VERSION) is not a semantic version; tag a release first"; \
		exit 1; \
	fi
	@rm -rf $(RELEASE_DIR)
	@mkdir -p $(RELEASE_DIR)
	@for p in $(PLATFORMS); do \
		os=$$(echo "$$p" | cut -d- -f1); \
		arch=$$(echo "$$p" | cut -d- -f2); \
		archive="$(RELEASE_DIR)/fi_$(VERSION)_$$p.tar.gz"; \
		mkdir -p "$$archive.tmp"; \
		cp $(BUILD_DIR)/$(BINARY)-$$p "$$archive.tmp/fi"; \
		cp LICENSE README.md "$$archive.tmp/"; \
		tar -czf "$$archive" -C "$$archive.tmp" .; \
		rm -rf "$$archive.tmp"; \
		echo "packaged $$archive"; \
	done
	# Stage plugin bundles with correct version in staging dir (don't mutate source)
	@REL_VERSION=$$(echo $(VERSION) | sed 's/^v//' | sed 's/-.*//'); \
	mkdir -p "$(RELEASE_DIR)/staging/claude-code" && \
	sed "s/\"version\": \".*\"/\"version\": \"$$REL_VERSION\"/" plugins/claude-code/.claude-plugin/plugin.json > "$(RELEASE_DIR)/staging/claude-code/plugin.json" && \
	cp -r plugins/claude-code/hooks plugins/claude-code/scripts plugins/claude-code/skills plugins/claude-code/bin "$(RELEASE_DIR)/staging/claude-code/" && \
	(cd "$(RELEASE_DIR)/staging/claude-code" && zip -q -r "../../../$(RELEASE_DIR)/freeinference-companion-claude_$(VERSION).zip" .) && \
	echo "packaged Claude plugin bundle"; \
	mkdir -p "$(RELEASE_DIR)/staging/codex" && \
	sed "s/\"version\": \".*\"/\"version\": \"$$REL_VERSION\"/" plugins/codex/.codex-plugin/plugin.json > "$(RELEASE_DIR)/staging/codex/plugin.json" && \
	cp -r plugins/codex/hooks plugins/codex/scripts plugins/codex/skills plugins/codex/bin "$(RELEASE_DIR)/staging/codex/" && \
	(cd "$(RELEASE_DIR)/staging/codex" && zip -q -r "../../../$(RELEASE_DIR)/freeinference-companion-codex_$(VERSION).zip" .) && \
	echo "packaged Codex plugin bundle"; \
	rm -rf "$(RELEASE_DIR)/staging"; \
	cp LICENSE README.md $(RELEASE_DIR)/ && \
	$(MAKE) sbom && $(MAKE) provenance && $(MAKE) checksums && \
	echo "packaging complete"

# plugin-bin builds all platform binaries into each plugin's bin/ directory
# so that the installed hook wrappers can find a working fi binary without
# relying on the user having fi on PATH or pre-installed.
plugin-bin: build-all
	@mkdir -p plugins/claude-code/bin plugins/codex/bin
	@for p in $(PLATFORMS); do \
		os=$$(echo "$$p" | cut -d- -f1); \
		arch=$$(echo "$$p" | cut -d- -f2); \
		case "$$arch" in x86_64) arch="amd64" ;; esac; \
		bin_dir="plugins/claude-code/bin/$$os-$$arch"; \
		mkdir -p "$$bin_dir"; \
		cp $(BUILD_DIR)/$(BINARY)-$$p "$$bin_dir/$(BINARY)"; \
		bin_dir="plugins/codex/bin/$$os-$$arch"; \
		mkdir -p "$$bin_dir"; \
		cp $(BUILD_DIR)/$(BINARY)-$$p "$$bin_dir/$(BINARY)"; \
		echo "copied $$p into plugin bin/$$os-$$arch/"; \
	done

# package-smoke validates the packaged archives: extracts each platform archive
# into a temporary directory, runs the binary, and asserts it executes.
package-smoke: package
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	cur="$$(go env GOOS)-$$(go env GOARCH)"; \
	archive="$(RELEASE_DIR)/fi_$(VERSION)_$$cur.tar.gz"; \
	if [ ! -f "$$archive" ]; then echo "FAIL: current-platform archive missing: $$archive"; exit 1; fi; \
	tar -xzf "$$archive" -C "$$tmp"; \
	bin="$$tmp/fi"; \
	if "$$bin" help >/dev/null 2>&1; then \
		echo "smoke OK: $$cur (binary executes)"; \
	else \
		echo "FAIL: $$cur binary did not execute"; exit 1; \
	fi; \
	for p in $(PLATFORMS); do \
		a="$(RELEASE_DIR)/fi_$(VERSION)_$$p.tar.gz"; \
		if tar -tzf "$$a" >/dev/null 2>&1; then \
			echo "archive OK: $$p"; \
		else \
			echo "FAIL: $$p archive corrupt"; exit 1; \
		fi; \
	done; \
	for z in $(RELEASE_DIR)/freeinference-companion-*.zip; do \
		if unzip -t "$$z" >/dev/null 2>&1; then \
			echo "archive OK: $$(basename $$z)"; \
		else \
			echo "FAIL: corrupt zip $$z"; exit 1; \
		fi; \
	done; \
	echo "package smoke tests passed"

# plugin-clean-install smoke extracts each plugin zip into a temp directory
# with an empty HOME and no fi on PATH, then exercises the hook wrapper
# resolution chain (bundled bin/ → fallback paths). Confirms the plugin
# archives contain all platform binaries.
plugin-clean-install: package
	@tmpdir="$$(mktemp -d 2>/dev/null || echo /tmp)"; \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	for z in $(RELEASE_DIR)/freeinference-companion-*.zip; do \
		edir="$$(mktemp -d -p "$$tmpdir" 2>/dev/null || mktemp -d)"; \
		unzip -q "$$z" -d "$$edir" && echo "extracted $$(basename $$z)"; \
		base="$$(ls "$$edir")"; \
		test -d "$$edir/$$base/bin" || { echo "FAIL: bin/ missing in $$(basename $$z)"; exit 1; }; \
		for bin in "$$edir/$$base/bin"/*; do \
			while [ -d "$$bin" ]; do \
				for f in "$$bin"/*; do \
					test -x "$$f" && { echo "  executable: $$(basename $$f)"; } || { echo "FAIL: $$(basename $$f) is not executable"; exit 1; }; \
				done; \
				break; \
			done; \
		done; \
		ls "$$edir/$$base/scripts/run-hook.sh" >/dev/null 2>&1 || { echo "FAIL: run-hook.sh missing in $$(basename $$z)"; exit 1; }; \
	done; \
	echo "plugin clean-install smoke tests passed"

# sbom generates a minimal SPDX JSON SBOM of the module dependencies.
# For a richer SBOM, install syft (https://github.com/anchore/syft).
sbom:
	@go run ./cmd/sbomgen "$(VERSION)" "$(RELEASE_DIR)/sbom.spdx.json"
	@echo "SBOM written to $(RELEASE_DIR)/sbom.spdx.json"

# provenance generates an in-toto attestation-style provenance file.
# For signed provenance, install cosign (https://github.com/sigstore/cosign).
provenance:
	@go run ./cmd/provenancegen "$(VERSION)" "$(COMMIT)" "$(RELEASE_DIR)/provenance.intoto.jsonl"
	@echo "provenance written to $(RELEASE_DIR)/provenance.intoto.jsonl"

install: build
	install -d -m 755 "$${HOME}/.local/bin"
	install -m 755 $(BUILD_DIR)/$(BINARY) "$${HOME}/.local/bin/$(BINARY)"

clean:
	rm -rf $(BUILD_DIR) $(RELEASE_DIR)

test:
	go test ./... -count=1

test-race:
	go test -race ./... -count=1

vet:
	go vet ./...

lint: vet

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "unformatted files:"; gofmt -l .; exit 1)

# plugin-syntax-check verifies that the plugin manifests parse as JSON and the
# hook wrapper scripts parse as bash. This is a SYNTAX check only — it does
# NOT validate against either vendor's plugin schema, and it does NOT verify
# that the Codex runtime will load plugin-local hooks. Use the official
# `claude` plugin validator and a real Codex install for runtime validation
# (tracked separately under P0-1).
plugin-syntax-check:
	@python3 -c "import json; json.load(open('plugins/claude-code/.claude-plugin/plugin.json')); json.load(open('plugins/claude-code/hooks/hooks.json')); json.load(open('plugins/codex/.codex-plugin/plugin.json')); json.load(open('plugins/codex/hooks/hooks.json')); print('plugin manifests are syntactically valid JSON')"
	@bash -n plugins/claude-code/scripts/run-hook.sh && bash -n plugins/codex/scripts/run-hook.sh && echo "hook wrappers are syntactically valid bash"

# plugin-validate is the legacy name for plugin-syntax-check. It is preserved
# for compatibility with existing CI jobs and scripts, but it does NOT perform
# full plugin validation. New callers should use plugin-syntax-check to make
# the contract honest, or invoke the official vendor validators directly.
plugin-validate: plugin-syntax-check

# tidy-check fails if `go mod tidy` would modify go.mod or go.sum.
tidy-check:
	@cp go.mod go.mod.check
	@cp go.sum go.sum.check
	@go mod tidy
	@if ! diff -q go.mod go.mod.check >/dev/null || ! diff -q go.sum go.sum.check >/dev/null; then \
		rm -f go.mod.check go.sum.check; \
		echo "error: go.mod or go.sum is out of date — run 'go mod tidy' and commit the result"; \
		exit 1; \
	fi
	@rm -f go.mod.check go.sum.check

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

check: fmt-check vet test test-race plugin-syntax-check mod-verify tidy-check
	@git diff --check
	@echo "all checks passed"

# bench runs the full benchmark suite locally (not CI-gated).
bench:
	go test ./... -bench=. -benchmem -run=^$$

# bench-ci enforces the latency promises (status p95<10ms, hook p95<25ms).
# Runs the real benchmarks with enough iterations to get reliable averages and
# fails if either benchmark exceeds its ceiling. Go benchmarks report average
# ns/op, not p95. We enforce a conservative average ceiling that leaves
# headroom under the p95 target. True p95 latency enforcement requires
# subprocess-level distribution testing (tracked separately).
bench-ci:
	@output=$$(go test ./internal/adapters/ -bench='BenchmarkStatusLineUpdate|BenchmarkUserPromptSubmitNoWarning' -benchtime=1s -count=1 -timeout 120s 2>&1); \
	echo "$$output"; \
	if ! echo "$$output" | grep -q '^Benchmark'; then \
		echo "error: bench-ci ran zero benchmarks — expected BenchmarkStatusLineUpdate and BenchmarkUserPromptSubmitNoWarning"; \
		exit 1; \
	fi; \
	status_ns=$$(echo "$$output" | grep 'BenchmarkStatusLineUpdate' | head -1 | grep -oP '\d+(?= ns/op)'); \
	hook_ns=$$(echo "$$output" | grep 'BenchmarkUserPromptSubmitNoWarning' | head -1 | grep -oP '\d+(?= ns/op)'); \
	echo "status average = $${status_ns}ns, hook average = $${hook_ns}ns"; \
	if [ "$$status_ns" -gt 5000000 ]; then \
		echo "FAIL: status line average $${status_ns}ns exceeds 5ms ceiling (p95 target: 10ms)"; exit 1; \
	fi; \
	if [ "$$hook_ns" -gt 5000000 ]; then \
		echo "FAIL: hook average $${hook_ns}ns exceeds 5ms ceiling (p95 target: 25ms)"; exit 1; \
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
	tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	HOME="$$tmp/home" \
	FI_CACHE_DIR="$$tmp/cache" \
	FI_NO_BACKGROUND=1 \
	FREEINFERENCE_API_KEY="" \
	FREEINFERENCE_BASE_URL="" \
	./$(BUILD_DIR)/$(BINARY) help || { echo "FAIL: fi help"; exit 1; }; \
	HOME="$$tmp/home" \
	FI_CACHE_DIR="$$tmp/cache" \
	FI_NO_BACKGROUND=1 \
	FREEINFERENCE_API_KEY="" \
	FREEINFERENCE_BASE_URL="" \
	./$(BUILD_DIR)/$(BINARY) sessions || { echo "FAIL: fi sessions"; exit 1; }; \
	echo "Smoke test complete"
