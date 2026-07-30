---
name: freeinference-doctor
description: This skill is used when diagnosing FreeInference connection problems, configuration issues, authentication failures, or when the user needs to verify their setup is working correctly. Trigger when the user reports errors, connection issues, or wants to verify their API configuration. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "[--probe --model <name>]"
allowed-tools: Bash
---

# FreeInference Doctor

Diagnose FreeInference API connectivity and configuration. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `freeinference doctor` to check:
- Cache directory writability and state readability
- freeinference binary resolvability for hooks
- Claude hook configuration and status-line wrapper
- Provider detection result
- Endpoint reachability and model catalog
- Health source configuration (optional)

Authentication and model access are reported as `unknown` unless verified by
an explicit probe — doctor never infers them from API key presence.

### Options

- `--probe --model <name>` — Also send a minimal synthetic inference request (marked `X-Probe: synthetic`) to verify the full request pipeline. Without `--model`, a model is selected from the cached catalog and the selection is printed.

### Example output

```
FreeInference Doctor
------------------------------------------------------------
Cache directory:       ✓
State schema:          ✓
freeinference binary:             ✓ on PATH
Claude hook config:    ✓
Status-line wrapper:   ✓
Provider detection:    ✓ freeinference via FREEINFERENCE_API_KEY
Health source:         ? not configured (optional)
API endpoint:          ✓
Model catalog:         ✓ 12 models listed
API key format:        ✓ present, format valid (not verified)
Authentication:        ? not verified without an authenticated operation
Model access:          ? catalog presence does not imply access

Doctor complete.
```

### Notes

- The `--probe` flag sends a real (but minimal) inference request. It consumes service resources.
- Synthetic probes are marked with `X-Probe: synthetic` header for server-side identification. Whether the request is excluded from FreeInference accounting is determined by the server — the companion cannot guarantee it.
- Doctor checks are non-destructive except for `--probe`: they create and remove a temporary cache write probe to verify writability.
- If the endpoint is unreachable, check your `FREEINFERENCE_BASE_URL` environment variable.