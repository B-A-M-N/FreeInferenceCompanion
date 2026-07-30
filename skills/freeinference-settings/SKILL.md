---
name: freeinference-settings
description: This skill manages FreeInference Companion settings. Use when the user wants to view, change, or reset configuration values, including context thresholds, cache warnings, refresh intervals, provider settings, and privacy options. Use when asked about configuration, settings, preferences, or thresholds. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
---

# FreeInference Settings

Manage FreeInference Companion configuration from within the host agent. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Start by gathering current state with these JSON commands:

1. Run `freeinference version --json` to get the companion version and build info.
2. Run `freeinference status --json` to get current session, model, provider, and live metrics.

These commands return structured JSON that the agent can parse reliably.

### Viewing settings

```bash
freeinference version --json
freeinference status --json
```

The output includes:
- Current values (effective values in use)
- Default values (what would apply if no override exists)
- Source of each value (environment variable, config file, default, CLI flag)
- Context pressure thresholds currently active

### Changing settings

When the user wants to adjust a setting:

1. Confirm the setting key and desired value.
2. Validate the value is within acceptable range or choices.
3. Run `freeinference config set <key> <value>` to apply (when available).
4. Verify the change took effect by running `freeinference status --json` again.

### Supported settings

| Category | Setting Keys |
|---|---|
| Context thresholds | `context.warning`, `context.critical`, `context.watch` |
| Cache warnings | `cache.warning-threshold`, `cache.min-samples` |
| Refresh intervals | `refresh.interval`, `refresh.stale-threshold` |
| Provider detection | `provider.api-key-env`, `provider.base-url` |
| Diagnostic probes | `doctor.probe-model`, `doctor.timeout` |
| Privacy | `privacy.exclude-env-vars`, `privacy.redact-paths` |
| Status-line format | `statusline.format`, `statusline.thresholds` |
| Enable/disable | `companion.disabled` (true/false) |

### Valid ranges and choices

- Boolean settings: `true` or `false`
- Integer settings: positive integers (e.g., `300` for 5-minute interval)
- Threshold settings: values between 0 and 100 (percentage)
- Model settings: must match a name from `freeinference models` catalog
- Format strings: must contain `{model}`, `{pressure}`, `{read}` placeholders

### Example

```
User: "Lower my context warning threshold"
Agent: "Your current warning threshold is 70%. Valid range is 10-90%. What value would you like?"
User: "60%"
Agent: "Setting context.warning to 60... done. Verifying..."
freeinference status --json
```

### Notes

- `freeinference config set` and `freeinference config show` may not be fully implemented yet. If the command returns an error, explain the current value and suggest manual configuration via environment variables or `~/.config/freeinference-companion/settings.json`.
- Always verify changes with `freeinference status --json` after applying.
- Some settings only take effect on next session start.
