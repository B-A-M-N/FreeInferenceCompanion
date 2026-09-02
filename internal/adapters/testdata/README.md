# Claude telemetry fixtures

These are sanitized contract fixtures for the documented Claude Code
status-line and hook payload shapes. They contain no real session IDs,
prompts, responses, credentials, or vendor account data.

The adapter pipeline tests intentionally run these through parsing, activation,
observation identity, epoch handling, persistence, and cache rendering. When
the vendor payload contract changes, add a new fixture and update the
compatibility rule rather than silently reinterpreting an existing field.
