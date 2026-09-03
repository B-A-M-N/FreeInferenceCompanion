---
name: freeinference-support
description: This skill generates diagnostic information and support bundles for FreeInference Companion. Use when the user needs troubleshooting help, wants to file a bug report, or needs to share diagnostics with maintainers. Shows diagnostic overview, privacy disclosure, and guides bundle creation. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
---

# FreeInference Support Bundle

Generate diagnostics and support bundles for FreeInference Companion. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run diagnostic and report commands in sequence:

### Step 1: Full diagnostic

Run `freeinference doctor --json` for a comprehensive diagnostic:

```bash
freeinference doctor --json
```

This checks:
- Cache directory state (writability, schema version, free space)
- freeinference binary availability for hooks
- Claude Code hook configuration (settings.json, status-line wrapper)
- Provider detection result (name, key source, endpoint)
- Health source configuration
- API endpoint reachability
- Model catalog accessibility

### Step 2: Session report

Run `freeinference report --json` for a session-focused report:

```bash
freeinference report --json
freeinference report --session <id> --json   # specific session
```

This includes:
- Plugin version and client type
- Session details (model, provider, context usage)
- Pressure state
- Last failure (if any)
- Provider health summary

### Step 3: Compile findings

Combine the outputs to create a diagnostic summary:

```
=== FreeInference Diagnostics ===
Version:    <from freeinference doctor>
Client:     <claude-code / codex / other>

--- Health ---
Cache dir:  <ok/warn/error>
freeinference binary:  <on-path / missing>
Hooks:      <configured / missing>
Provider:   <name> via <source>
Endpoint:   <reachable / unreachable>

--- Session ---
Model:      <model name>
Context:    <peak> / <limit> (<percentage>)
Pressure:   <healthy / watch / warn / critical>

--- Issues ---
<list any errors or warnings from doctor/report>
```

### Privacy disclosure

#### What IS collected

- Plugin version and build info
- Client type (claude-code, codex, terminal)
- Session ID (non-personal identifier)
- Model name and provider name
- Context usage percentages (not raw prompt data)
- Pressure state
- Sanitized diagnostic categories and bounded error details where the command
  exposes them
- File path existence checks (not file contents)
- Cache directory size and writability

#### What is explicitly EXCLUDED

- API keys or token values (ever)
- Authentication headers
- Prompt contents or model responses
- Repository file contents or paths (beyond existence checks)
- Environment variable values
- Personal files or user data
- Network request/response bodies
- System information beyond what's needed for hook diagnosis

### When to use support

Use freeinference-support when:
- The user reports an error that doctor cannot resolve
- Context metrics are not updating
- Cache is not collecting data
- Provider detection fails
- Status-line is broken after an update
- A bug report needs to be filed

### Example flow

```
User: "I'm having trouble with my context metrics"
Agent: "Let me run a full diagnostic. This will check your configuration and connectivity."
freeinference doctor --json
freeinference report --json
"Here's what I found:
- Cache directory: OK
- Hooks: configured
- Provider: detected
- Issue: context pressure not updating

This looks like a status-line configuration issue. Let me generate a support bundle."
```

### Notes

- Always review the diagnostic output before sharing with anyone.
- The report excludes sensitive fields by design, but review is still recommended.
- Run `freeinference doctor` before `freeinference report` — doctor identifies configuration issues that report does not cover.
- If both commands return errors, the companion may need to be reinstalled or reconfigured.
