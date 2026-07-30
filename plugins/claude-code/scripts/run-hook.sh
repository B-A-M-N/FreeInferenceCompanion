#!/usr/bin/env bash
# FreeInference Companion — hook runner for Claude Code.
# Resolves the freeinference binary from PATH or the plugin-bundled bin/.
# Always exits 0 — hooks must never block Claude Code.
set -u

# Allow complete disable via environment variable
if [[ "${FI_DISABLED:-}" == "1" ]]; then
    exit 0
fi

event="${1:-}"

if type -P freeinference >/dev/null 2>&1; then
    freeinference hook claude-code "$event" || true
    exit 0
fi

# Claude Code sets CLAUDE_PLUGIN_ROOT; some builds also export PLUGIN_ROOT.
# Accept both so the wrapper keeps working across versions.
plugin_root="${CLAUDE_PLUGIN_ROOT:-${PLUGIN_ROOT:-}}"

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
    for candidate in "$plugin_root/bin/$plat/freeinference" "$plugin_root/bin/freeinference" "$plugin_root/freeinference"; do
        if [[ -n "$candidate" && -x "$candidate" ]]; then
            "$candidate" hook claude-code "$event" || true
            exit 0
        fi
    done
fi

exit 0
