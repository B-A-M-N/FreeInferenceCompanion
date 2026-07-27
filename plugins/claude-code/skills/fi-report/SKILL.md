---
name: fi-report
description: This skill is triggered when the user asks to generate a support report, a troubleshooting bundle, or diagnostic information for sharing with FreeInference maintainers. Use when reporting issues to FreeInference support or when asked for session diagnostics.
argument-hint: [--session <id>]
allowed-tools: Bash
---

# FreeInference Report

Generate a sanitized support report for FreeInference maintainers.

## Usage

Run `fi report --session <id>` to generate a report with:
- Plugin version
- Client type and session ID
- Current model
- Live context metrics (used %, token breakdown)
- Pressure state
- Last failure (if any)
- Provider health status (if configured)
- Timestamp

### What is excluded

The report explicitly excludes:
- API keys
- Authentication headers
- Prompt contents
- Model responses
- Repository contents
- Environment variable values
- Private file paths
- Personally identifying information

### Example

```
FreeInference Companion Report
============================================================
Plugin Version: 0.1.0
Client:         claude-code
Session:        sess_abc123
Status:         active
Model:          minimax-m3

--- Live Context ---
Used:           42.5%
Fresh Input:    48K
Cache Read:     1.2M

--- Pressure ---
State:          warn
Projected:      84%

--- Provider Health ---
Status:         ok
Checked:        2026-07-27T20:30:00Z

--- Sanitized ---
No API keys, prompts, responses, or repository contents included.
```

### Notes

- Reports are safe to share with FreeInference support
- Run `fi doctor` before creating a report to include diagnostic results
- Without `--session`, shows global state summary