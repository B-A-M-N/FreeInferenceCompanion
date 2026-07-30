---
name: freeinference-sessions
description: This skill is used when the user wants to browse, inspect, or manage FreeInference sessions. Use when asked about session history, token usage per session, session state, or model/provider per session. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
---

# FreeInference Sessions

Browse and inspect FreeInference Companion sessions from structured data. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `freeinference sessions --json` to get a list of sessions:

```bash
freeinference sessions --json
```

### Listing sessions

The command returns an array of session objects with:

- `id`: unique session identifier
- `status`: active, completed, interrupted, errored
- `model`: model name used for the session
- `provider`: provider name and detection source
- `started`: ISO 8601 start time
- `tokens`: total tokens consumed in the session
- `contextPeak`: peak context window usage (tokens)
- `contextLimit`: context window limit for the model
- `events`: list of notable events (model changes, errors, cache misses)

### Example output

```json
{
  "sessions": [
    {
      "id": "sess_abc123",
      "status": "active",
      "model": "minimax-m3",
      "provider": "freeinference (FREEINFERENCE_API_KEY)",
      "started": "2026-07-29T14:00:00Z",
      "tokens": 1245000,
      "contextPeak": 890000,
      "contextLimit": 1000000,
      "events": [
        {"time": "2026-07-29T14:00:00Z", "type": "started", "detail": "session begins"},
        {"time": "2026-07-29T14:15:00Z", "type": "model_change", "detail": "switched to minimax-m3"},
        {"time": "2026-07-29T15:30:00Z", "type": "pressure_warn", "detail": "context 72% used"}
      ]
    }
  ],
  "total": 1,
  "oldest": "2026-07-29T14:00:00Z",
  "newest": "2026-07-29T15:45:00Z"
}
```

### Inspecting a specific session

Use `freeinference sessions --json --session <id>` to inspect a specific session:

```bash
freeinference sessions --json --session sess_abc123
```

This returns the same structure but focused on one session with additional detail:
- Token usage breakdown by turn
- Context pressure timeline
- Error details if any
- Cache metrics per turn

### Showing token usage

Token usage includes:
- Total tokens consumed
- Breakdown by: fresh, cache-read, cache-write, output
- Peak context usage as percentage of context window
- Context pressure state at peak (watch/warn/critical)

### Showing context pressure

Context pressure is tracked per session:
- **healthy**: context below warning threshold
- **watch**: context approaching warning threshold
- **warn**: context above warning threshold, consider pruning
- **critical**: context near limit, action required

### Showing events

Events capture notable state changes during a session:
- `started`: session begins
- `model_change`: model was switched
- `provider_change`: provider was switched
- `pressure_watch`: entered watch state
- `pressure_warn`: entered warn state
- `pressure_critical`: entered critical state
- `error`: an error occurred
- `cache_cold`: cache was found to be cold
- `completed`: session ended normally

### Notes

- Without `--session`, the command lists all sessions in reverse chronological order (newest first).
- The active session (if any) is marked with `"status": "active"`.
- Session data is stored locally and is not sent to FreeInference servers.
- If `freeinference sessions --json` is not yet implemented, use `freeinference status --json` for the current session and suggest filing a feature request for session history.
