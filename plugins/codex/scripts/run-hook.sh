#!/usr/bin/env bash
# FreeInference Companion — hook runner for Codex.
# Resolves the freeinference binary from PATH or the plugin-bundled bin/.
# Always exits 0 — hooks must never block Codex.
set -u

if [[ "${FI_DISABLED:-}" == "1" ]]; then
    exit 0
fi

event="${1:-}"

if type -P freeinference >/dev/null 2>&1; then
    freeinference hook codex "$event" || true
    exit 0
fi

# Codex provides PLUGIN_ROOT. CLAUDE_PLUGIN_ROOT is accepted for compatibility
# with shared test/install tooling and older plugin runners.
plugin_root="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"

if [[ -n "$plugin_root" ]]; then
    os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch_name="$(uname -m)"
    case "$arch_name" in
        x86_64) arch_name="amd64" ;;
        aarch64) arch_name="arm64" ;;
    esac
    plat="$os_name-$arch_name"
    for candidate in "$plugin_root/bin/$plat/freeinference" "$plugin_root/bin/freeinference" "$plugin_root/freeinference"; do
        if [[ -n "$candidate" && -x "$candidate" ]]; then
            "$candidate" hook codex "$event" || true
            exit 0
        fi
    done
fi

exit 0
