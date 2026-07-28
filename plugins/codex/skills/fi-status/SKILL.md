---
name: fi-status
description: This skill is triggered when the user asks about their FreeInference session status, current metrics, token usage, or context consumption. Use when the user wants to see their current session's state. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "[--compact]"
allowed-tools: Bash
---

# FreeInference Companion — Codex Status

Show current session metrics for the FreeInference Companion. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `fi status` to show:
- Current model and session ID
- Session status
- Pressure state
- Last failure info (if any)

### Options

- `--compact` — Single-line output suitable for embedding in prompts or scripts
- `--session <id>` — Show a specific session (default: current)

### Notes

- Codex does not provide live token/context snapshots
- Context pressure and cache metrics stay unknown for Codex sessions — they are never fabricated
