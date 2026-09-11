#!/usr/bin/env bash
# FreeInference Companion — Codex lifecycle hook runner.
# Hook failures are always swallowed so Companion state cannot block Codex.
set -u

if [[ "${FI_DISABLED:-}" == "1" ]]; then
    exit 0
fi

event="${1:-}"
if [[ -z "$event" ]]; then
    exit 0
fi

if type -P freeinference >/dev/null 2>&1; then
    freeinference hook codex "$event" >/dev/null 2>&1 || true
    exit 0
fi

plugin_root="${PLUGIN_ROOT:-}"
if [[ -z "$plugin_root" ]]; then
    exit 0
fi

os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch_name="$(uname -m)"
case "$arch_name" in
    x86_64) arch_name="amd64" ;;
    aarch64) arch_name="arm64" ;;
esac
platform="${os_name}-${arch_name}"

for candidate in \
    "$plugin_root/bin/$platform/freeinference" \
    "$plugin_root/bin/freeinference" \
    "$plugin_root/freeinference"; do
    if [[ -x "$candidate" ]]; then
        "$candidate" hook codex "$event" >/dev/null 2>&1 || true
        exit 0
    fi
done

exit 0
