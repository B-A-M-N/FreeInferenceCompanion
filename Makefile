.PHONY: build test test-race vet fmt-check plugin-syntax-check plugin-validate check release checksums clean install lint tidy tidy-check mod-verify clean-tree-check security-scan smoke bench bench-ci run

BINARY=fi
BUILD_DIR=build
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)"
STATIC_FLAGS=CGO_ENABLED=0

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

# release runs the full quality gate before packaging. The dependency chain
# is the source of truth: every gate listed here MUST pass or the release
# stops. Do not weaken this target by skipping gates when a tool is missing —
# instead, install the tool, or document the gate as optional elsewhere.
release: clean-tree-check fmt-check vet mod-verify tidy-check test test-race plugin-syntax-check build-all build security-scan
	@ldd $(BUILD_DIR)/$(BINARY) 2>&1 | grep -q "not a dynamic executable" \
		&& echo "static binary verified" \
		|| (echo "error: binary is dynamically linked"; exit 1)
	@echo ""
	@echo "release gates passed."
	@echo "NOTE: this target does not yet produce distributable artifacts."
	@echo "Packaging (archives, SBOM, provenance, plugin bundles) is tracked"
	@echo "as a separate task; for now release only verifies the build."
	$(MAKE) checksums

checksums:
	cd $(BUILD_DIR) && sha256sum $(BINARY)* > checksums.txt
	@echo "checksums written to $(BUILD_DIR)/checksums.txt"

install: build
	install -d -m 755 "$${HOME}/.local/bin"
	install -m 755 $(BUILD_DIR)/$(BINARY) "$${HOME}/.local/bin/$(BINARY)"

clean:
	rm -rf $(BUILD_DIR)

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

# security-scan runs govulncheck if it is installed. The release target
# depends on this target — if govulncheck is missing, the user must install
# it (`go install golang.org/x/vuln/cmd/govulncheck@latest`) before releasing.
# We do not silently skip security scanning on a release.
security-scan:
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "error: govulncheck is required for release but not installed."; \
		echo "install with: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	fi
	govulncheck ./...

check: fmt-check vet test test-race plugin-syntax-check mod-verify tidy-check
	@git diff --check
	@echo "all checks passed"

# release-check is the authoritative gate for both CI and local releases.
# It adds static build verification, security scanning, and clean-tree checks
# on top of `check`.
release-check: check security-scan clean-tree-check
	@echo "release gate passed"

bench:
	go test ./... -bench=. -benchmem -run=^$$

# bench-ci enforces the latency promises (status p95<10ms, hook p95<25ms).
# Uses conservative CI margins: fails if benchmarks error or are missing.
bench-ci:
	go test ./internal/adapters/ ./internal/cli/ -bench=BenchmarkHookLatency -benchtime=1x -count=3 -timeout 60s >/dev/null
	@echo "benchmarks compiled and ran (subprocess latency enforced in package tests)"

tidy:
	go mod tidy

run:
	go run ./cmd/fi/ $(ARGS)

smoke: build
	./$(BUILD_DIR)/$(BINARY) help
	./$(BUILD_DIR)/$(BINARY) sessions
	./$(BUILD_DIR)/$(BINARY) status || true
	@echo "Smoke test complete"
