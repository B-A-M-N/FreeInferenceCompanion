---
name: fi-doctor
description: This skill is used when diagnosing FreeInference connection problems, configuration issues, authentication failures, or when the user needs to verify their setup is working correctly. Trigger when the user reports errors, connection issues, or wants to verify their API configuration. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "[--probe --model <name>]"
allowed-tools: Bash
---

# FreeInference Doctor

Diagnose FreeInference API connectivity and configuration. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `fi doctor` to check:
- Cache directory writability and state readability
- fi binary resolvability for hooks
- Claude hook configuration and status-line wrapper
- Provider detection result
- Endpoint reachability and model catalog
- Health source configuration (optional)

Authentication and model access are reported as `unknown` unless verified by
an explicit probe — doctor never infers them from API key presence.

### Options

- `--probe --model <name>` — Also send a minimal synthetic inference request (marked `X-Probe: synthetic`) to verify the full request pipeline. Without `--model`, a model is selected from the cached catalog and the selection is printed.

### Notes

- The `--probe` flag sends a real (but minimal) inference request. It consumes service resources.
- Synthetic probes are marked with `X-Probe: synthetic` header and do not affect FreeInference metrics.
- Doctor checks are non-destructive except for `--probe`: they create and remove a temporary cache write probe to verify writability.
- If the endpoint is unreachable, check your `FREEINFERENCE_BASE_URL` environment variable.
