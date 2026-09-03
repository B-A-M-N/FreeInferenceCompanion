---
name: freeinference-enable
description: This skill manages enabling and disabling FreeInference Companion. Use when the user wants to pause, stop, or resume the companion without uninstalling. Shows current disabled state, guides toggling, and verifies integration health after the change. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
---

# FreeInference Enable / Disable

Pause or resume FreeInference Companion without uninstalling. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

### Check current state

First, check whether the companion is currently disabled:

```bash
freeinference status --json
```

Look for a `disabled` field or similar indicator in the output. If the companion is disabled, status output may be minimal or absent.

### Disable the companion

To temporarily stop the companion:

1. Confirm with the user that they want to disable it.
2. Run `freeinference companion disable`.
3. Verify the change took effect by running `freeinference status --json` — it should show disabled or return minimal output.

### Enable the companion

To resume the companion:

1. Confirm with the user that they want to enable it.
2. Run `freeinference companion enable`.
3. Verify integration health after the change:

```bash
freeinference status --json
freeinference doctor --json
```

### Post-toggle health check

After enabling, verify:

- **Status**: `freeinference status --json` shows active session
- **Provider**: provider is detected and configured correctly
- **Cache**: cache is operational (may be cold initially)
- **Context**: context metrics are being collected
- **Doctor**: `freeinference doctor --json` reports all checks as OK

### Example flow

```
User: "Pause FreeInference for a bit"
Agent: "I'll disable the companion. This stops context monitoring and cache collection. Your active session continues but metrics stop updating. Proceed?"
User: "Yes"
Agent: "Disabling... done. Verifying..."
freeinference status --json
"Companion is now disabled. To re-enable, just ask me."

User: "Resume FreeInference"
Agent: "Enabling the companion..."
freeinference companion enable
freeinference doctor --json
"Companion is running. All checks passed."
```

### What happens when disabled

When disabled:
- Context monitoring stops (no more status-line updates)
- Cache collection pauses (no new cache writes tracked)
- The active session continues normally (API calls are unaffected)
- The companion can be re-enabled at any time without losing configuration

### What happens when enabled

When enabled:
- Context monitoring resumes
- Cache collection resumes
- Status-line updates resume
- Health checks run on a schedule

### Notes

- Disabling the companion does not affect the underlying FreeInference API usage. Model calls continue normally.
- `companion disable` writes a persistent local marker; `companion enable`
  removes it. `FI_DISABLED=1` remains a process-level opt-out and takes
  precedence over the persistent marker.
- After enabling, the first few minutes may show cold cache — this is normal.
