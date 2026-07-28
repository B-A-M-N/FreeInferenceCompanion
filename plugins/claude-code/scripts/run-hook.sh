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

plugin_binary="${CLAUDE_PLUGIN_ROOT:-}/bin/fi"

if [[ -n "${CLAUDE_PLUGIN_ROOT:-}" && -x "$plugin_binary" ]]; then
    "$plugin_binary" hook claude-code "$event" || true
    exit 0
fi

exit 0
