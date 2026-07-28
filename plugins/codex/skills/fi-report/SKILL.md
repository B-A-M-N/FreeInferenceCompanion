---
name: fi-report
description: This skill is triggered when the user asks to generate a support report, a troubleshooting bundle, or diagnostic information for sharing with FreeInference maintainers. Use when reporting issues to FreeInference support or when asked for session diagnostics. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "[--session <id>] [--format markdown|json]"
allowed-tools: Bash
---

# FreeInference Report

Generate a sanitized support report for FreeInference maintainers. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `fi report --client codex --session <id>` to generate a report with:
- Plugin version
- Client type and session ID
- Current model
- Session status
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

### Notes

- Review the report before sharing — it is designed to exclude sensitive fields
- Run `fi doctor` first to diagnose any connectivity or configuration issues separately
- Without `--session`, the most recent session is resolved from the local session index.
- Use `--format json` for a machine-readable report.
