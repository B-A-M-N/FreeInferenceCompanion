// Package codexusage reads the bounded, local Codex rollout records that the
// Codex TUI itself uses for token accounting. The reader intentionally extracts
// only numeric usage, model, session, and working-directory metadata; prompts,
// tool inputs, instructions, and responses never leave the rollout parser.
package codexusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MaxRolloutTailBytes bounds the amount of a large rollout that is read.
	// Recent token_count and turn_context events are appended at the end.
	MaxRolloutTailBytes = 8 << 20
	maxRolloutLineBytes = 16 << 20
)

var ErrNoRollout = errors.New("no Codex rollout found")
var ErrNoUsage = errors.New("codex rollout contains no token usage")

// TokenUsage is one Codex token usage counter. A nil field means that Codex
// did not report that counter; zero is preserved as an explicit measurement.
type TokenUsage struct {
	InputTokens           *int64 `json:"input_tokens,omitempty"`
	CachedInputTokens     *int64 `json:"cached_input_tokens,omitempty"`
	CacheWriteInputTokens *int64 `json:"cache_write_input_tokens,omitempty"`
	OutputTokens          *int64 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens           *int64 `json:"total_tokens,omitempty"`
}

// Usage is the latest safe-to-display usage state observed in one rollout.
type Usage struct {
	RolloutPath   string
	SessionID     string
	Model         string
	CWD           string
	ObservedAt    time.Time
	ContextWindow int64
	Last          TokenUsage
	Total         TokenUsage
	latestTokenAt time.Time
}

// FreshInputTokens derives the uncached input counter from Codex's reported
// input, cache-read, and cache-write counters. It returns nil when the
// complete breakdown was not reported or is internally inconsistent.
func (u Usage) FreshInputTokens() *int64 {
	if u.Last.InputTokens == nil || u.Last.CachedInputTokens == nil || u.Last.CacheWriteInputTokens == nil {
		return nil
	}
	fresh := *u.Last.InputTokens - *u.Last.CachedInputTokens - *u.Last.CacheWriteInputTokens
	if fresh < 0 {
		return nil
	}
	return &fresh
}

// FindLatest returns the most recently modified regular rollout file under
// CODEX_HOME/sessions. Date directories are traversed explicitly so a
// malformed file elsewhere in CODEX_HOME cannot become a data source.
func FindLatest(codexHome string) (string, error) {
	root := filepath.Join(codexHome, "sessions")
	years, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoRollout
		}
		return "", err
	}
	var latest string
	var latestMod time.Time
	for _, year := range years {
		if !year.IsDir() || strings.HasPrefix(year.Name(), ".") {
			continue
		}
		months, readErr := os.ReadDir(filepath.Join(root, year.Name()))
		if readErr != nil {
			continue
		}
		for _, month := range months {
			if !month.IsDir() || strings.HasPrefix(month.Name(), ".") {
				continue
			}
			days, readErr := os.ReadDir(filepath.Join(root, year.Name(), month.Name()))
			if readErr != nil {
				continue
			}
			for _, day := range days {
				if !day.IsDir() || strings.HasPrefix(day.Name(), ".") {
					continue
				}
				entries, readErr := os.ReadDir(filepath.Join(root, year.Name(), month.Name(), day.Name()))
				if readErr != nil {
					continue
				}
				for _, entry := range entries {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") || strings.HasPrefix(entry.Name(), ".") {
						continue
					}
					path := filepath.Join(root, year.Name(), month.Name(), day.Name(), entry.Name())
					info, statErr := os.Lstat(path)
					if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
						continue
					}
					if latest == "" || info.ModTime().After(latestMod) {
						latest, latestMod = path, info.ModTime()
					}
				}
			}
		}
	}
	if latest == "" {
		return "", ErrNoRollout
	}
	return latest, nil
}

// Latest reads the newest rollout below codexHome.
func Latest(codexHome string) (*Usage, error) {
	path, err := FindLatest(codexHome)
	if err != nil {
		return nil, err
	}
	return ReadFile(path)
}

// ReadFile reads only the tail of one rollout and returns its latest
// token_count state. It never logs or returns rollout content.
func ReadFile(path string) (*Usage, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("codex rollout is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	start := int64(0)
	if info.Size() > MaxRolloutTailBytes {
		start = info.Size() - MaxRolloutTailBytes
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
	}
	reader := bufio.NewScanner(f)
	reader.Buffer(make([]byte, 64*1024), maxRolloutLineBytes)
	if start > 0 && reader.Scan() {
		// The first line begins in the middle of a JSONL record. Discard it.
	}
	usage := &Usage{RolloutPath: path, ObservedAt: info.ModTime()}
	for reader.Scan() {
		line := reader.Bytes()
		if !bytes.Contains(line, []byte(`"type":"token_count"`)) &&
			!bytes.Contains(line, []byte(`"type":"session_meta"`)) &&
			!bytes.Contains(line, []byte(`"type":"turn_context"`)) {
			continue
		}
		parseLine(line, usage)
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	if usage.Last.InputTokens == nil {
		return nil, ErrNoUsage
	}
	return usage, nil
}

type envelope struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type eventPayload struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage    TokenUsage `json:"total_token_usage"`
		LastTokenUsage     TokenUsage `json:"last_token_usage"`
		ModelContextWindow int64      `json:"model_context_window"`
	} `json:"info"`
}

type sessionPayload struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

type turnPayload struct {
	Model string `json:"model"`
	CWD   string `json:"cwd"`
}

func parseLine(line []byte, usage *Usage) {
	var event envelope
	if json.Unmarshal(line, &event) != nil {
		return
	}
	switch event.Type {
	case "session_meta":
		var payload sessionPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			if payload.SessionID != "" {
				usage.SessionID = payload.SessionID
			}
			if payload.CWD != "" {
				usage.CWD = strings.TrimPrefix(payload.CWD, "file://")
			}
		}
	case "turn_context":
		var payload turnPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			if payload.Model != "" {
				usage.Model = payload.Model
			}
			if payload.CWD != "" {
				usage.CWD = strings.TrimPrefix(payload.CWD, "file://")
			}
		}
	case "event_msg":
		var payload eventPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.Type != "token_count" {
			return
		}
		if event.Timestamp.IsZero() || usage.Last.InputTokens == nil || event.Timestamp.After(usage.latestTokenAt) {
			usage.Last = payload.Info.LastTokenUsage
			usage.Total = payload.Info.TotalTokenUsage
			usage.latestTokenAt = event.Timestamp
			if !event.Timestamp.IsZero() {
				usage.ObservedAt = event.Timestamp
			}
			if payload.Info.ModelContextWindow > 0 {
				usage.ContextWindow = payload.Info.ModelContextWindow
			}
		}
	}
}
