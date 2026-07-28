#!/usr/bin/env bash
# FreeInference Companion — hook runner for Codex.
# Resolves the fi binary from PATH, the plugin-bundled bin/, or ~/.local/bin.
# Always exits 0 — hooks must never block Codex.
set -u

# Allow complete disable via environment variable
if [[ "${FI_DISABLED:-}" == "1" ]]; then
    exit 0
fi

event="${1:-}"

if command -v fi >/dev/null 2>&1; then
    command fi hook codex "$event" || true
    exit 0
fi

# Codex supplies PLUGIN_ROOT to plugin hooks (the documented variable). Older
# builds used CODEX_PLUGIN_ROOT; accept either so the wrapper keeps working
# across versions. Resolve a plugin-bundled binary if present.
plugin_root="${PLUGIN_ROOT:-${CODEX_PLUGIN_ROOT:-}}"
plugin_binary="${plugin_root}/bin/fi"

if [[ -n "$plugin_root" && -x "$plugin_binary" ]]; then
    "$plugin_binary" hook codex "$event" || true
    exit 0
fi

user_binary="${HOME}/.local/bin/fi"

if [[ -x "$user_binary" ]]; then
    "$user_binary" hook codex "$event" || true
    exit 0
fi

exit 0
