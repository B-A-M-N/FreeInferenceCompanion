#!/usr/bin/env bash
# Fast, fail-open attribution-only PreToolUse wrapper.
# Claude provides event JSON on stdin; every error and timeout path exits 0 so
# the underlying tool call can proceed unchanged.
set -u
attribution_override="${FI_ATTRIBUTION_COMMIT_MODE-}"
attribution_mode="$(printf '%s' "$attribution_override" | tr '[:upper:]' '[:lower:]')"
if [[ "${FI_DISABLED:-}" == "1" || "$attribution_mode" =~ ^[[:space:]]*off[[:space:]]*$ ]]; then
    exit 0
fi

# PreToolUse is blocking in Claude Code. Keep the fail-open guarantee inside
# the wrapper instead of relying on a platform-specific `timeout` utility. The
# output is buffered so a killed child cannot emit a partial JSON response.
run_attribution() {
    input_file="$(mktemp "${TMPDIR:-/tmp}/freeinference-attribution-input.XXXXXX" 2>/dev/null)" || return 0
    output_file="$(mktemp "${TMPDIR:-/tmp}/freeinference-attribution.XXXXXX" 2>/dev/null)" || {
        rm -f "$input_file"
        return 0
    }
    trap 'rm -f "$input_file" "$output_file"' EXIT HUP INT TERM
    if ! cat >"$input_file"; then
        return 0
    fi
    # Bash gives asynchronous commands /dev/null for stdin, so explicitly
    # replay the buffered Claude event into the attribution process.
    "$1" hook claude-code PreToolUse <"$input_file" >"$output_file" 2>/dev/null &
    child_pid=$!
    ticks=0
    while kill -0 "$child_pid" 2>/dev/null; do
        if [[ "$ticks" -ge 20 ]]; then
            # The executable can be a shebang script. macOS may leave that
            # script's descendant alive when only the direct shell is killed,
            # so terminate the small process tree before reaping the parent.
            kill_process_tree() {
                local pid="$1" signal="$2" descendant
                if type -P pgrep >/dev/null 2>&1; then
                    for descendant in $(pgrep -P "$pid" 2>/dev/null); do
                        kill_process_tree "$descendant" "$signal"
                    done
                fi
                kill "-$signal" "$pid" 2>/dev/null || true
            }
            # Kill immediately after the deadline. A graceful TERM plus a
            # fixed sleep makes the fail-open path exceed its budget on
            # slower macOS runners, while KILL still gets reaped below.
            kill_process_tree "$child_pid" KILL
            wait "$child_pid" 2>/dev/null || true
            return 0
        fi
        sleep 0.1
        ticks=$((ticks + 1))
    done
    wait "$child_pid" 2>/dev/null || return 0
    cat "$output_file" 2>/dev/null || true
    return 0
}

plugin_root="${CLAUDE_PLUGIN_ROOT:-${PLUGIN_ROOT:-}}"
if [[ -z "$plugin_root" ]]; then
    exit 0
fi
os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch_name="$(uname -m)"
case "$arch_name" in x86_64) arch_name="amd64";; aarch64) arch_name="arm64";; esac
for candidate in "$plugin_root/bin/$os_name-$arch_name/freeinference" "$plugin_root/bin/freeinference" "$plugin_root/freeinference"; do
    if [[ -x "$candidate" ]]; then
        run_attribution "$candidate"
        exit 0
    fi
done
if type -P freeinference >/dev/null 2>&1; then
    run_attribution "$(type -P freeinference)"
fi
exit 0
