// Package secure contains the credential-redaction and storage-hardening
// utilities used by every output path (reports, events, snapshots, errors,
// logs). The contract is simple: any string that may leave the process
// through state, a report, or a user-facing message must pass through
// Redact. The companion persists nothing that the redactor would have to
// catch — but the redactor exists so that a future field (an unexpected
// server response, an error body, a debug string) cannot leak a key.
package secure

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// RedactedPlaceholder replaces any detected secret in output.
const RedactedPlaceholder = "[REDACTED]"

// secretPatterns are the canonical secret shapes the companion knows how to
// detect and scrub. Order matters: more specific patterns (full Bearer
// headers, full env assignments) run before generic bare-token patterns so a
// single pass produces a clean, non-cascading result. The list is
// deliberately conservative — favor false positives over false negatives
// when the cost of a leak is asymmetric.
var secretPatterns = []*regexp.Regexp{
	// 1. Full "Bearer <token>" Authorization header values — capture the
	//    prefix and replace only the token half.
	regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._\-/+=]+`),
	// 2. Authorization / x-api-key headers in either header or JSON shape.
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*["']?)[A-Za-z0-9._\-/+=]+`),
	regexp.MustCompile(`(?i)(x-api-key\s*[:=]\s*["']?)[A-Za-z0-9._\-/+=]+`),
	// 3. Specific env-var assignment shapes for known key names. Capture the
	//    name= prefix; replace only the value.
	regexp.MustCompile(`(?i)((?:FREEINFERENCE|ANTHROPIC|OPENAI)_API_KEY\s*=\s*)[A-Za-z0-9._\-/+=]+`),
	// 4. Generic labeled JSON/header secret under common key names. The body
	//    must be long enough (16+) to avoid eating short config values.
	regexp.MustCompile(`(?i)("?(?:api[_-]?key|secret|token|password|access[_-]?token|refresh[_-]?token)"?\s*[:=]\s*["']?)[A-Za-z0-9._\-/+=]{16,}`),
	// 5. Bare key-shaped tokens. These run last so they catch any tokens the
	//    header-shaped patterns missed. Require a prefix so we don't scrub
	//    arbitrary identifiers: hyi-*, sk-*, key-*, tok-*.
	regexp.MustCompile(`(?i)\b(?:hyi|sk|key|tok)-[A-Za-z0-9._\-/+=]{6,}`),
}

// Redact returns s with any detected secret-shaped substring replaced by
// RedactedPlaceholder. It is safe to call on any string. It is intentionally
// not a no-op on empty input so callers can uniformly pipe output through it.
func Redact(s string) string {
	if s == "" {
		return s
	}
	for _, p := range secretPatterns {
		// Patterns with a capture group preserve the label; the bare-token
		// pattern (no group) replaces the whole match.
		if p.NumSubexp() > 0 {
			s = p.ReplaceAllString(s, "${1}"+RedactedPlaceholder)
		} else {
			s = p.ReplaceAllString(s, RedactedPlaceholder)
		}
	}
	return s
}

// LooksLikeSecret reports whether s has the shape of a known secret. Use it
// as a defensive gate before persisting a value — when true, do not persist.
func LooksLikeSecret(s string) bool {
	if s == "" {
		return false
	}
	for _, p := range secretPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

// ScrubMap returns a copy of m with any string value that looks like a secret
// replaced by RedactedPlaceholder. Keys are not redacted (key names are not
// secrets). This is the workhorse for sanitizing untyped JSON before it lands
// in a report or error message.
func ScrubMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = scrubValue(v)
	}
	return out
}

func scrubValue(v any) any {
	switch x := v.(type) {
	case string:
		return Redact(x)
	case map[string]any:
		return ScrubMap(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = scrubValue(item)
		}
		return out
	default:
		return v
	}
}

// AllowedAccountFields is the exhaustive allowlist of fields permitted in an
// account-usage render. Anything not in this set is dropped on output. This
// is the primary defense for account-usage data: rather than redacting known
// bad shapes from arbitrary upstream JSON, we only ever emit fields we
// declared safe. A future endpoint that adds a "billing_email" or
// "api_key_hint" field will be silently dropped rather than leaked.
var AllowedAccountFields = map[string]struct{}{
	"requests_used":  {},
	"requests_limit": {},
	"tokens_used":    {},
	"tokens_limit":   {},
	"reset_at":       {},
	"fetched_at":     {},
}

// AllowlistAccountMap returns a copy of m containing only the fields named in
// AllowedAccountFields. Values are passed through Redact as a belt-and-
// suspenders measure, even though allowlisting should already be safe.
func AllowlistAccountMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(AllowedAccountFields))
	for k := range AllowedAccountFields {
		if v, ok := m[k]; ok {
			out[k] = scrubValue(v)
		}
	}
	return out
}

// ShortHash returns the first 8 hex characters of a SHA-256 over s. It is the
// obfuscation utility for identifiers that must appear in output (logs,
// events with session_id, doctor) but where the full value is not required
// for the consumer to act. The full session ID is still persisted in the
// snapshot (needed for resolution); ShortHash is only for *display* contexts.
//
// Why this exists: a snapshot file dumped into a bug report or pasted into a
// chat could carry a sensitive session ID. ShortHash ensures log lines and
// advisory output show only a non-reversible prefix. Resolution from a
// short hash back to the full ID is not supported by design.
func ShortHash(s string) string {
	if s == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}

// MaskSessionID returns a masked form of a session ID for display:
// keeps the first 4 and last 4 characters, replaces the middle with dots.
// Empty input returns empty. Short input (≤8 chars) returns ShortHash.
// This is lighter-weight than ShortHash when the consumer is a human who
// benefits from seeing the prefix/suffix.
func MaskSessionID(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return ShortHash(s)
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// controlSeq matches terminal-control and other unprintable sequences that
// must never reach a rendered field: ANSI CSI/OSC sequences, common VT100
// controls, DEL, and any non-printable control char. The companion does not
// emit these itself, but upstream model/provider/session strings could in
// principle carry them and would otherwise be capable of terminal-injection
// when piped back through a shell or rendered by a TUI.
var controlSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

// SanitizeField strips terminal-control sequences and other non-printable
// characters from a string, then bounds it to a reasonable display length.
// Use this for any field sourced from upstream data (model ID, provider
// name, session ID, client-reported version, error category) before it lands
// in state, an event, a report, or the status-line view model. SanitizeField
// does NOT apply Redact — callers should compose SanitizeField and Redact
// when both threats apply (Redact catches secrets, SanitizeField catches
// terminal injection).
func SanitizeField(s string) string {
	if s == "" {
		return s
	}
	s = controlSeq.ReplaceAllString(s, "")
	// Collapse any whitespace runs introduced by removing escape sequences.
	s = strings.TrimSpace(s)
	// Replace internal newlines/tabs so a multiline upstream value cannot
	// break the layout of a single-line status or a report field.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	// Bound the length so an upstream value cannot DOS the renderer. 256 is
	// generous for any plausible model ID or provider name.
	if len(s) > 256 {
		s = s[:256]
	}
	return s
}

// SafeField applies both output-safe character filtering and secret-shape
// redaction. Use it for untrusted client/provider strings that may be written
// to persistent state as well as displayed later.
func SafeField(s string) string {
	return Redact(SanitizeField(s))
}

// SafeIdentifier sanitizes an identifier for persistence while preserving the
// distinction between different secret-shaped values. Identifiers such as
// model IDs are not credentials, but an upstream value can still resemble a
// credential; replacing every such value with the same placeholder would
// collapse distinct records and make validation reject an otherwise valid
// response.
func SafeIdentifier(s string) string {
	clean := SanitizeField(s)
	if !LooksLikeSecret(clean) {
		return clean
	}
	h := sha256.Sum256([]byte(clean))
	return RedactedPlaceholder + "-" + hex.EncodeToString(h[:])[:16]
}
