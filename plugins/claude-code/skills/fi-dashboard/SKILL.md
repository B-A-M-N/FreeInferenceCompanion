---
name: fi-dashboard
description: This skill opens the FreeInference service status dashboard in the default web browser. Use when the user wants to see real-time provider status, model availability, latency graphs, or the FreeInference health overview page. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "no arguments"
allowed-tools: Bash
---

# FreeInference Dashboard

Open the FreeInference model status dashboard in the default web browser. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `fi dashboard` to open:

```
https://status.freeinference.org/
```

The dashboard shows:
- Per-model status (UP/DOWN)
- Latency and TTFT (time to first token)
- Throughput (tokens/second)
- Uptime percentages
- Trend charts (30-100 data points)

### Options

No arguments. The dashboard always opens the same URL.

### Notes

- Requires a desktop environment with `xdg-open` (Linux) or `open` (macOS)
- In terminal-only environments, the URL is printed to stdout
- The status page auto-refreshes every 20 minutes