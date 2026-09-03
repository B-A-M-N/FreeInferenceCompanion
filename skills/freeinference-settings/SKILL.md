---
name: freeinference-settings
description: This skill manages FreeInference Companion settings. Use when the user wants to view, change, or reset configuration values, including context thresholds, cache warnings, refresh intervals, provider settings, and privacy options. Use when asked about configuration, settings, preferences, or thresholds. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
---

# FreeInference Settings

Manage FreeInference Companion configuration from within the host agent. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Start with `freeinference config show --json`. It returns effective settings,
their provenance, and validity without contacting FreeInference. Use
`freeinference version --json` separately for build information.

### Viewing settings

```bash
freeinference config show --json
freeinference config path
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
3. Run `freeinference config set <key> <value>` to apply.
4. Verify the change with `freeinference config show --json`.

### Supported settings

| Category | Setting Keys |
|---|---|
| Context thresholds | `context.watch_enter`, `context.warn_enter`, `context.critical_enter`, `context.watch_leave`, `context.warn_leave`, `context.critical_leave`, `context.output_reserve` |
| Cache warnings | `cache.warn_threshold`, `cache.recovered_threshold`, `cache.cooldown_mins` |
| Refresh intervals | `refresh.interval_mins` |
| Reporting | `reporting.level` (`summary`, `standard`, or `detailed`) |
| Diagnostic probes | `privacy.diagnostic_probes` |
| Trace correlation | `tracing.enabled` |

### Valid ranges and choices

- Boolean settings: `true` or `false`
- Integer settings: positive integers (e.g., `300` for 5-minute interval)
- Threshold settings: values between 0 and 100 (percentage)
- Model settings: must match a name from `freeinference models` catalog
- Reporting level: `summary`, `standard`, or `detailed`

### Example

```
User: "Lower my context warning threshold"
Agent: "Your current warning threshold is 70%. Valid range is 10-90%. What value would you like?"
User: "60%"
Agent: "Setting context.warn_enter to 60... done. Verifying..."
freeinference config show --json
```

### Notes

- Configuration is stored at `~/.config/freeinference-companion/config.json`.
  Use `freeinference config path` to confirm the resolved location.
- Always verify changes with `freeinference config show --json` after applying.
- Some settings only take effect on next session start.
