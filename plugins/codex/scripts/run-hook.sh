#!/usr/bin/env bash
# FreeInference Companion — hook runner for Codex.
# Resolves the freeinference binary from PATH, the plugin-bundled bin/, or ~/.local/bin.
# Always exits 0 — hooks must never block Codex.
set -u

# Allow complete disable via environment variable
if [[ "${FI_DISABLED:-}" == "1" ]]; then
    exit 0
fi

event="${1:-}"

if type -P freeinference >/dev/null 2>&1; then
    freeinference hook codex "$event" || true
    exit 0
fi

# Codex supplies PLUGIN_ROOT to plugin hooks (the documented variable). Older
# builds used CODEX_PLUGIN_ROOT; accept both so the wrapper keeps working
# across versions. Resolve a platform-specific bundled binary if present.
# Codex sets PLUGIN_ROOT; older builds used CODEX_PLUGIN_ROOT.
# Claude Code sets CLAUDE_PLUGIN_ROOT; accept all variants so the wrapper
# keeps working across vendors and versions.
plugin_root="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-${CODEX_PLUGIN_ROOT:-}}}"

if [[ -n "$plugin_root" ]]; then
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
            "$candidate" hook codex "$event" || true
            exit 0
        fi
    done
fi

user_binary="${HOME}/.local/bin/freeinference"

if [[ -x "$user_binary" ]]; then
    "$user_binary" hook codex "$event" || true
    exit 0
fi

exit 0
