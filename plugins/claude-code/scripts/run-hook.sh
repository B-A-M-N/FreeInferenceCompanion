#!/usr/bin/env bash
# FreeInference Companion — hook runner for Claude Code.
# Resolves the fi binary from PATH or the plugin-bundled bin/.
# Always exits 0 — hooks must never block Claude Code.
set -u

# Allow complete disable via environment variable
if [[ "${FI_DISABLED:-}" == "1" ]]; then
    exit 0
fi

event="${1:-}"

if command -v fi >/dev/null 2>&1; then
    command fi hook claude-code "$event" || true
    exit 0
fi

plugin_root="${CLAUDE_PLUGIN_ROOT:-}"

if [[ -n "$plugin_root" ]]; then
    # Try platform-specific binary first, then fall back to a generic one.
    os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch_name="$(uname -m)"
    # Normalize architecture names to match Makefile output (amd64/x86_64 -> amd64, arm64/aarch64 -> arm64)
    case "$arch_name" in
        x86_64) arch_name="amd64" ;;
        aarch64) arch_name="arm64" ;;
    esac
    plat="$os_name-$arch_name"
    for candidate in "$plugin_root/bin/$plat/fi" "$plugin_root/bin/fi"; do
        if [[ -n "$candidate" && -x "$candidate" ]]; then
            "$candidate" hook claude-code "$event" || true
            exit 0
        fi
    done
fi

exit 0
