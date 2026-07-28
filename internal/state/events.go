package state

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
)

// Event is one sanitized lifecycle event persisted to events.jsonl. It NEVER
// contains prompt text, model responses, transcripts, repository paths,
// environment values, API keys, headers, or raw error bodies.
type Event struct {
	Type      string    `json:"type"`
	At        time.Time `json:"at"`
	Client    string    `json:"client,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Model     string    `json:"model,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

// Allowed event types. Anything else is rejected.
const (
	EventSessionStarted      = "session_started"
	EventStatusObserved      = "status_observed"
	EventPromptSubmitted     = "prompt_submitted"
	EventTurnStopped         = "turn_stopped"
	EventTurnFailed          = "turn_failed"
	EventCompactionStarted   = "compaction_started"
	EventCompactionCompleted = "compaction_completed"
	EventSessionEnded        = "session_ended"
	EventWarningShown        = "warning_shown"
	EventWarningResolved     = "warning_resolved"
)

var allowedEvents = map[string]struct{}{
	EventSessionStarted:      {},
	EventStatusObserved:      {},
	EventPromptSubmitted:     {},
	EventTurnStopped:         {},
	EventTurnFailed:          {},
	EventCompactionStarted:   {},
	EventCompactionCompleted: {},
	EventSessionEnded:        {},
	EventWarningShown:        {},
	EventWarningResolved:     {},
}

// Event retention bounds — protect the local disk from unbounded growth.
const (
	MaxEventBytesPerSession = 256 * 1024 // 256 KiB per session
	MaxEventsPerSession     = 1000
	MaxSessionAge           = 30 * 24 * time.Hour
	MaxDetailLen            = 200
)

// droppedEvents counts events that were dropped because the per-session event
// lock was held by another process (AppendEvent runs on the hook path and
// must not block). Exposed via DroppedEvents() for observability. A rising
// count under load indicates event contention; events are best-effort.
var droppedEvents int64

// DroppedEvents returns the count of events dropped due to lock contention
// since process start. AppendEvent is nonblocking on the hook path — under
// contention it increments this counter and returns ErrLockBusy.
func DroppedEvents() int64 {
	return atomic.LoadInt64(&droppedEvents)
}

// AppendEvent appends one sanitized event to the per-session events.jsonl.
// Rotation/retention runs opportunistically. AppendEvent is best-effort: any
// I/O error returns an error but callers (hooks) should treat it as fail-open.
// The session directory must already exist (EnsureSessionDir).
//
// All upstream-sourced string fields are sanitized (terminal-control
// sequences stripped, length bounded) and the Detail field additionally
// passes through secure.Redact so a token-shaped value cannot leak via an
// over-eager caller. Model, Provider, Client, and SessionID are treated as
// untrusted display strings: a misbehaving client could surface arbitrary
// bytes there, and an event log is a long-lived artifact that may be
// inspected during incident response.
func AppendEvent(paths Paths, clientType, sessionID string, ev Event) error {
	if _, ok := allowedEvents[ev.Type]; !ok {
		return fmt.Errorf("unknown event type %q", ev.Type)
	}
	// Defensive: scrub any secret-shaped substring from Detail before it is
	// persisted. Detail is supposed to be a short category, but this guards
	// against a future caller that passes something richer.
	if ev.Detail != "" {
		ev.Detail = SanitizeForEvent(ev.Detail)
	}
	// Sanitize all upstream-derived string fields. The session ID we record
	// is the caller's (keyed by hash on disk anyway), but its raw form may
	// still appear in the JSON line — strip control sequences so it cannot
	// break terminal output when the log is later tailed.
	ev.Client = secure.SanitizeField(ev.Client)
	ev.SessionID = secure.SanitizeField(ev.SessionID)
	ev.Model = secure.SanitizeField(ev.Model)
	ev.Provider = secure.SanitizeField(ev.Provider)
	ev.At = time.Now().UTC()
	if ev.Client == "" {
		ev.Client = secure.SanitizeField(clientType)
	}
	if ev.SessionID == "" {
		ev.SessionID = secure.SanitizeField(sessionID)
	}

	path := paths.SessionEvents(clientType, sessionID)
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	// Hold the per-session event lock so concurrent appends don't lose data
	// during rotation (which replaces the file). The lock is NONBLOCKING
	// because AppendEvent runs on the hook path (25 ms p95 budget). If the
	// lock is held by another process, we drop the event and increment
	// droppedEvents rather than stall the hook.
	lockPath := path + ".lock"
	fl := NewFileLock(lockPath)
	if err := fl.Acquire(); err != nil {
		if IsLockBusy(err) {
			atomic.AddInt64(&droppedEvents, 1)
		}
		return err
	}
	defer fl.Release()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
	return nil
}

// RotateEvents truncates the events file when it exceeds bounds. It keeps the
// most recent MaxEventsPerSession lines. Enforces both the byte limit AND the
// line count limit independently — the file is rotated when either is exceeded.
//
// AppendEvent holds the per-session event lock during writes; RotateEvents
// acquires the same lock so concurrent appends cannot be lost during rotation.
// Best-effort; errors are returned but callers treat them as advisory.
func RotateEvents(paths Paths, clientType, sessionID string) error {
	path := paths.SessionEvents(clientType, sessionID)
	dir := paths.SessionDir(clientType, sessionID)
	lockPath := path + ".lock"

	// Nonblocking: rotation runs on the hook path. If the lock is held,
	// skip rotation this time — it will be retried on the next event.
	fl := NewFileLock(lockPath)
	if err := fl.Acquire(); err != nil {
		if IsLockBusy(err) {
			return nil // best-effort; don't stall the hook
		}
		return err
	}
	defer fl.Release()

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() <= MaxEventBytesPerSession {
		// Byte limit not exceeded — but check line count anyway.
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := splitLines(data)
		if len(lines) <= MaxEventsPerSession {
			return nil
		}
		// Over line count but under byte limit — still rotate.
		return writeLines(dir, path, lines[len(lines)-MaxEventsPerSession:])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Split and keep the most recent lines.
	lines := splitLines(data)
	if len(lines) > MaxEventsPerSession {
		lines = lines[len(lines)-MaxEventsPerSession:]
	}

	return writeLines(dir, path, lines)
}

// writeLines writes the given lines to path atomically via temp file + rename.
func writeLines(dir, path string, lines [][]byte) error {
	tmp, err := os.CreateTemp(dir, "events-*.jsonl")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	for _, l := range lines {
		if _, err := tmp.Write(append(l, '\n')); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// ReadEvents reads up to max events from a session event log. If max <= 0,
// all events are read in chronological order (oldest first). If max > 0,
// the most recent max events are returned (newest first) — this is the
// common case for display and incident response where the latest activity
// matters most.
func ReadEvents(paths Paths, clientType, sessionID string, max int) ([]Event, error) {
	path := paths.SessionEvents(clientType, sessionID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var all []Event
	dec := json.NewDecoder(f)
	for {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			if err == io.EOF {
				break
			}
			return all, err
		}
		all = append(all, ev)
	}
	if max > 0 {
		if len(all) > max {
			all = all[len(all)-max:]
		}
		// Reverse to newest-first.
		for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
			all[i], all[j] = all[j], all[i]
		}
	}
	return all, nil
}

// CleanupStaleSessions removes session directories whose newest file is older
// than MaxSessionAge. Best-effort. Also prunes stale entries from the session
// index so the index does not accumulate dead references.
func CleanupStaleSessions(paths Paths, now time.Time) error {
	sessionsRoot := paths.CacheDir + "/sessions"
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, clientEntry := range entries {
		if !clientEntry.IsDir() {
			continue
		}
		clientDir := sessionsRoot + "/" + clientEntry.Name()
		sessions, err := os.ReadDir(clientDir)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if !s.IsDir() {
				continue
			}
			dir := clientDir + "/" + s.Name()
			if isStale(dir, now) {
				_ = os.RemoveAll(dir)
			}
		}
	}
	// Prune stale entries from the index.
	return pruneStaleIndexEntries(paths, now)
}

// pruneStaleIndexEntries removes index entries whose session directories no
// longer exist (already GC'd) or whose last event exceeds MaxSessionAge.
// Best-effort; errors are returned but callers treat them as advisory.
func pruneStaleIndexEntries(paths Paths, now time.Time) error {
	// Nonblocking: cleanup is opportunistic. If the lock is held, skip this
	// round — a later refresh or hook will retry.
	fl := NewFileLock(paths.GlobalSessionIndexLock())
	if err := fl.Acquire(); err != nil {
		if IsLockBusy(err) {
			return nil
		}
		return err
	}
	defer fl.Release()

	idx, err := LoadSessionIndex(paths)
	if err != nil {
		return err
	}
	if len(idx.Sessions) == 0 {
		return nil
	}

	var live []SessionIndexEntry
	for _, e := range idx.Sessions {
		// Drop entries whose last event exceeds the retention window.
		if now.Sub(e.LastEventAt) > MaxSessionAge {
			continue
		}
		// Drop entries whose session directory no longer exists.
		dir := paths.SessionDir(e.Client, e.SessionID)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		live = append(live, e)
	}
	if len(live) == len(idx.Sessions) {
		return nil
	}
	idx.Sessions = live
	return WriteJSONAtomically(paths.GlobalSessionIndex(), idx)
}

func isStale(dir string, now time.Time) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	newest := time.Time{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if newest.IsZero() {
		return false
	}
	return now.Sub(newest) > MaxSessionAge
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// SanitizeForEvent removes any value that should never appear in an event
// detail field. It is a defensive last-mile cleaner; callers must already
// pass sanitized data. Applies both length-bounding and secret-shape redaction.
func SanitizeForEvent(s string) string {
	if len(s) > MaxDetailLen {
		s = s[:MaxDetailLen] + "..."
	}
	return secure.Redact(s)
}
