---
name: fi-doctor
description: This skill is used when diagnosing FreeInference connection problems, configuration issues, authentication failures, or when the user needs to verify their setup is working correctly. Trigger when the user reports errors, connection issues, or wants to verify their API configuration.
argument-hint: [--probe]
allowed-tools: Bash
---

# FreeInference Doctor

Diagnose FreeInference API connectivity and configuration.

## Usage

Run `fi doctor` to check:
- Endpoint reachability
- API key presence and format validity
- Authentication acceptance
- Model catalog accessibility
- Health source configuration (if set)

### Options

- `--probe` — Also send a minimal synthetic inference request (marked `X-Probe: synthetic`) to verify the full request pipeline

### Example output

```
FreeInference Doctor
------------------------------------------------------------
Endpoint reachable... ✓
API key present:     ✓ (format valid)
Authentication...... ✓
Model catalog....... ✓
Health source:       https://status.staging.freeinference.org/api/health

Doctor complete.
```

### Notes

- The `--probe` flag sends a real (but minimal) inference request. It consumes service resources.
- Synthetic probes are marked with `X-Probe: synthetic` header and do not affect FreeInference metrics.
- Doctor checks are read-only except for `--probe`.
- If the endpoint is unreachable, check your `FREEINFERENCE_BASE_URL` environment variable.