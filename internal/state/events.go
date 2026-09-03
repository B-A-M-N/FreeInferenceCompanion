package state

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// Event is one sanitized lifecycle event persisted to events.jsonl. It NEVER
// contains prompt text, model responses, transcripts, repository paths,
// environment values, API keys, headers, or raw error bodies.
// SessionID is stored as a hashed identifier — the raw session ID is never
// written to the event log. The directory path already identifies the session
// internally; the hashed ID in the event record is sufficient for correlation
// during incident response.
type Event struct {
	Type              string    `json:"type"`
	At                time.Time `json:"at"`
	Client            string    `json:"client,omitempty"`
	SessionID         string    `json:"session_id,omitempty"`
	Model             string    `json:"model,omitempty"`
	Provider          string    `json:"provider,omitempty"`
	Detail            string    `json:"detail,omitempty"`
	HTTPStatus        *int      `json:"http_status,omitempty"`
	Retryable         *bool     `json:"retryable,omitempty"`
	TransportClass    string    `json:"transport_class,omitempty"`
	ProviderErrorType string    `json:"provider_error_type,omitempty"`
	ErrorOrigin       string    `json:"error_origin,omitempty"`
	RetryAfterSeconds *int64    `json:"retry_after_seconds,omitempty"`
	RequestReference  string    `json:"request_reference,omitempty"`
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
	EventModelSwitch         = "model_switch"
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
	EventModelSwitch:         {},
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

// AppendEvent appends one sanitized event to the per-session events.jsonl and
// opportunistically enforces retention while it already owns the event lock.
// AppendEvent is best-effort: any
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
	if _, err := paths.SessionDirFor(clientType, sessionID); err != nil {
		return err
	}
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
	ev.Client = secure.SafeField(ev.Client)
	ev.SessionID = sessionKey(sessionID)
	ev.Model = secure.SafeIdentifier(ev.Model)
	ev.Provider = secure.SafeField(ev.Provider)
	ev.TransportClass = sanitizeEventField(ev.TransportClass)
	ev.ProviderErrorType = sanitizeEventField(ev.ProviderErrorType)
	ev.ErrorOrigin = sanitizeEventField(ev.ErrorOrigin)
	ev.RequestReference = sanitizeEventField(ev.RequestReference)
	if ev.HTTPStatus != nil && (*ev.HTTPStatus < 400 || *ev.HTTPStatus > 599) {
		ev.HTTPStatus = nil
	}
	if ev.RetryAfterSeconds != nil && (*ev.RetryAfterSeconds < 0 || *ev.RetryAfterSeconds > 7*24*60*60) {
		ev.RetryAfterSeconds = nil
	}
	ev.At = time.Now().UTC()
	if ev.Client == "" {
		ev.Client = secure.SafeField(clientType)
	}

	path := paths.SessionEvents(clientType, sessionID)
	dir := paths.SessionDir(clientType, sessionID)
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	// Hold the per-session lifecycle lock as well as the event lock. Stale
	// cleanup removes the whole directory under the lifecycle lock; using the
	// same lock here prevents cleanup from deleting an append in flight.
	sessionLock := NewFileLock(paths.SessionLock(clientType, sessionID))
	if err := sessionLock.Acquire(); err != nil {
		if IsLockBusy(err) {
			atomic.AddInt64(&droppedEvents, 1)
		}
		return err
	}
	defer sessionLock.Release()

	// Hold the per-session event lock so concurrent appends don't lose data
	// during rotation (which replaces the file). The lock is NONBLOCKING
	// because AppendEvent runs on the hook path with a tight latency budget. If the
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

	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to follow symlink at %s", path)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
	// Retention is advisory: a failed rotation must not turn a successful hook
	// event into a hook failure. The next event or an explicit refresh retries it.
	_ = rotateEventsLocked(dir, path)
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
	if _, err := paths.SessionDirFor(clientType, sessionID); err != nil {
		return err
	}
	path := paths.SessionEvents(clientType, sessionID)
	dir := paths.SessionDir(clientType, sessionID)
	lockPath := path + ".lock"
	sessionLock := NewFileLock(paths.SessionLock(clientType, sessionID))
	if err := sessionLock.Acquire(); err != nil {
		if IsLockBusy(err) {
			return nil
		}
		return err
	}
	defer sessionLock.Release()

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
	return rotateEventsLocked(dir, path)
}

func rotateEventsLocked(dir, path string) error {
	if info, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to follow symlink at %s", path)
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("event log is not a regular file: %s", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() <= MaxEventBytesPerSession {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := splitLines(data)
		if len(lines) <= MaxEventsPerSession {
			return nil
		}
		return writeLines(dir, path, retainedEventLines(lines))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Split and keep the most recent lines.
	lines := splitLines(data)
	return writeLines(dir, path, retainedEventLines(lines))
}

func retainedEventLines(lines [][]byte) [][]byte {
	if len(lines) == 0 {
		return nil
	}
	start := len(lines)
	var bytes int
	for start > 0 && len(lines)-start < MaxEventsPerSession {
		candidate := len(lines[start-1]) + 1
		if bytes > 0 && bytes+candidate > MaxEventBytesPerSession {
			break
		}
		if bytes == 0 && candidate > MaxEventBytesPerSession {
			break
		}
		bytes += candidate
		start--
	}
	return lines[start:]
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
	if err := os.Chmod(tmpPath, 0600); err != nil {
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
	if _, err := paths.SessionDirFor(clientType, sessionID); err != nil {
		return nil, err
	}
	path := paths.SessionEvents(clientType, sessionID)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink at %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("event log is not a regular file: %s", path)
	}
	if info.Size() > MaxEventBytesPerSession {
		return nil, fmt.Errorf("event log exceeds %d-byte limit", MaxEventBytesPerSession)
	}
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
	sessionsRoot := filepath.Join(paths.CacheDir, "sessions")
	if info, statErr := os.Lstat(sessionsRoot); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("sessions root is not a directory: %s", sessionsRoot)
	}
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return err
	}
	for _, clientEntry := range entries {
		if clientEntry.Name() != schema.ClientClaudeCode && clientEntry.Name() != schema.ClientCodex {
			continue
		}
		clientDir := filepath.Join(sessionsRoot, clientEntry.Name())
		clientInfo, infoErr := os.Lstat(clientDir)
		if infoErr != nil || clientInfo.Mode()&os.ModeSymlink != 0 || !clientInfo.IsDir() {
			continue
		}
		sessions, err := os.ReadDir(clientDir)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if !s.IsDir() {
				continue
			}
			dir := filepath.Join(clientDir, s.Name())
			cleanupStaleSession(dir, now)
		}
	}
	// Prune stale entries from the index.
	return pruneStaleIndexEntries(paths, now)
}

func cleanupStaleSession(dir string, now time.Time) {
	if !isStale(dir, now) {
		return
	}
	lock := NewFileLock(filepath.Join(dir, "lock"))
	if err := lock.Acquire(); err != nil {
		return
	}
	defer lock.Release()
	if isStale(dir, now) {
		_ = os.RemoveAll(dir)
	}
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
		if validateClientType(e.Client) != nil || validateSessionID(e.SessionID) != nil {
			continue
		}
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
		if e.Name() == "lock" || strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if newest.IsZero() {
		info, err := os.Stat(dir)
		if err != nil {
			return false
		}
		return now.Sub(info.ModTime()) > MaxSessionAge
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
	s = secure.SanitizeField(s)
	if len(s) > MaxDetailLen {
		s = s[:MaxDetailLen] + "..."
	}
	return secure.Redact(s)
}

func sanitizeEventField(s string) string {
	return secure.Redact(secure.SanitizeField(s))
}
