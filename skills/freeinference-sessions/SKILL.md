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

The command returns an array of compact session entries with client, masked
session ID, model, status, and last-event time. It is a session index, not a
full transcript or per-turn history.

### Example output

```json
[
  {
    "client": "claude-code",
    "session_id": "sess_…abc123",
    "model_id": "minimax-m3",
    "status": "active",
    "last_event_at": "2026-07-29T15:45:00Z"
  }
]
```

### Inspecting a specific session

Use `freeinference status --session <id>` or
`freeinference report --session <id>` for a selected session:

```bash
freeinference status --session sess_abc123 --level detailed
freeinference report --session sess_abc123 --format markdown
```

The session index itself remains compact; detailed context, cache, compaction,
and failure data belongs to the snapshot/report surfaces.

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
- The session index does not include prompts, responses, or full event history;
  inspect a selected session with `freeinference report --session <id>`.
