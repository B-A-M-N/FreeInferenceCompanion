package state

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// SessionIndex is the on-disk index of known sessions.
type SessionIndex struct {
	Sessions []SessionIndexEntry `json:"sessions"`
}

// SessionIndexEntry is one indexed session.
//
// Privacy boundary note: the raw SessionID is retained in the index because
// it is load-bearing for snapshot path resolution (the on-disk directory is
// sessionKey(ID) and the index does not store the reverse mapping). The
// index file itself is mode 0600 inside the user's own cache directory — it
// never leaves the host and is never rendered. All human-facing output paths
// (CLI sessions list, reports, status line) mask the ID at render time via
// secure.MaskSessionID. The other string fields (Client, ModelID) are
// sanitized in-place to prevent terminal-control injection.
type SessionIndexEntry struct {
	Client       string    `json:"client"`
	SessionID    string    `json:"session_id"`
	SessionKey   string    `json:"session_key"`
	ModelID      string    `json:"model_id"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"started_at"`
	LastEventAt  time.Time `json:"last_event_at"`
	ActivationID string    `json:"activation_id,omitempty"`
}

// maxIndexEntries bounds the index so it cannot grow without limit.
const maxIndexEntries = 200

// LoadSessionIndex reads the session index. Missing or corrupt index
// returns an empty index (fail open).
func LoadSessionIndex(paths Paths) (*SessionIndex, error) {
	idx := &SessionIndex{}
	if err := ReadJSON(paths.GlobalSessionIndex(), idx); err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		// Corrupt index: fail open with an empty index.
		return &SessionIndex{}, nil
	}
	return idx, nil
}

// UpdateSessionIndex records a session snapshot in the index under a
// non-blocking lock. Lock contention is not an error — the index is
// best-effort and will be updated by a later mutation.
//
// The SessionID stored in the index is raw because it is load-bearing for
// lookup and is protected by the private 0600 index file. Human-facing
// renderers must mask it with secure.MaskSessionID. Resolution from the index
// back to the real session directory is by SessionKey (the hash), never by
// matching a masked form.
func UpdateSessionIndex(paths Paths, snap *schema.Snapshot) error {
	if snap == nil || snap.Session.ID == "" {
		return nil
	}
	if err := validateClientType(snap.Client.Type); err != nil {
		return err
	}
	if err := validateSessionID(snap.Session.ID); err != nil {
		return err
	}
	// Ensure the sessions-index directory exists before acquiring the lock.
	// This is an unnamespaced directory shared across all activations.
	if err := ensureSecureDirAll(paths.SessionIndexDir()); err != nil {
		return err
	}
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

	entry := SessionIndexEntry{
		Client:       secure.SafeField(snap.Client.Type),
		SessionID:    snap.Session.ID,
		SessionKey:   sessionKey(snap.Session.ID),
		ModelID:      secure.SafeIdentifier(snap.Model.ID),
		Status:       snap.Session.Status,
		StartedAt:    snap.Session.StartedAt,
		LastEventAt:  snap.Session.LastEventAt,
		ActivationID: snap.ActivationID,
	}

	found := false
	for i := range idx.Sessions {
		if idx.Sessions[i].Client == entry.Client && idx.Sessions[i].SessionKey == entry.SessionKey {
			idx.Sessions[i] = entry
			found = true
			break
		}
	}
	if !found {
		idx.Sessions = append(idx.Sessions, entry)
	}

	// Bound the index: keep the most recently updated entries.
	sort.SliceStable(idx.Sessions, func(i, j int) bool {
		return idx.Sessions[i].LastEventAt.After(idx.Sessions[j].LastEventAt)
	})
	if len(idx.Sessions) > maxIndexEntries {
		idx.Sessions = idx.Sessions[:maxIndexEntries]
	}

	return WriteJSONAtomically(paths.GlobalSessionIndex(), idx)
}

// ErrAmbiguousSession is returned when several sessions are similarly active
// and the caller should list them rather than guess.
var ErrAmbiguousSession = errors.New("multiple active sessions")

// ResolveSession picks a session from the index.
//
// Resolution order (first match wins):
//  1. explicit sessionID (with optional client filter)
//  2. most recently updated active session for the given client
//  3. most recently updated session overall
//  4. several similarly-active sessions → ErrAmbiguousSession
func ResolveSession(paths Paths, clientType, sessionID string) (*SessionIndexEntry, error) {
	idx, err := LoadSessionIndex(paths)
	if err != nil {
		return nil, err
	}
	if len(idx.Sessions) == 0 {
		return nil, nil
	}

	if sessionID != "" {
		for i := range idx.Sessions {
			e := &idx.Sessions[i]
			if e.SessionID != sessionID {
				continue
			}
			if clientType != "" && e.Client != clientType {
				continue
			}
			return e, nil
		}
		return nil, nil
	}

	// Index is sorted by LastEventAt descending after each update, but do not
	// rely on persisted order — sort defensively.
	sort.SliceStable(idx.Sessions, func(i, j int) bool {
		return idx.Sessions[i].LastEventAt.After(idx.Sessions[j].LastEventAt)
	})

	// Most recent active session for the requested client.
	if clientType != "" {
		for i := range idx.Sessions {
			e := &idx.Sessions[i]
			if e.Client == clientType && e.Status == schema.SessionActive {
				return e, nil
			}
		}
		for i := range idx.Sessions {
			if idx.Sessions[i].Client == clientType {
				return &idx.Sessions[i], nil
			}
		}
		return nil, nil
	}

	// No client filter: most recent active session overall. If several are
	// similarly active (within 30s), report ambiguity with the candidate list.
	for i := range idx.Sessions {
		if idx.Sessions[i].Status == schema.SessionActive {
			mostRecent := idx.Sessions[i].LastEventAt
			var actives []SessionIndexEntry
			for j := range idx.Sessions {
				if idx.Sessions[j].Status == schema.SessionActive &&
					mostRecent.Sub(idx.Sessions[j].LastEventAt) < 30*time.Second {
					actives = append(actives, idx.Sessions[j])
				}
			}
			if len(actives) > 1 {
				return nil, fmt.Errorf("%w: %d candidates", ErrAmbiguousSession, len(actives))
			}
			return &idx.Sessions[i], nil
		}
	}

	return &idx.Sessions[0], nil
}
