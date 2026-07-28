package secure

import (
	"strings"
	"testing"
)

func TestRedactFreeInferenceKey(t *testing.T) {
	in := "Authorization: Bearer hyi-ABcDefGhIjKlMnOpQrStUv"
	out := Redact(in)
	if strings.Contains(out, "hyi-ABcDefGhIjKlMnOpQrStUv") {
		t.Errorf("key leaked: %s", out)
	}
	if !strings.Contains(out, RedactedPlaceholder) {
		t.Errorf("placeholder missing: %s", out)
	}
}

func TestRedactOpenAIKey(t *testing.T) {
	in := "key=sk-proj-abcdef0123456789xyz"
	out := Redact(in)
	if strings.Contains(out, "sk-proj-abcdef0123456789xyz") {
		t.Errorf("key leaked: %s", out)
	}
}

func TestRedactAuthorizationHeader(t *testing.T) {
	for _, in := range []string{
		"Authorization: Bearer somelongtokenvaluehere",
		`{"authorization":"Bearer abcdefghijklmnop1234567890"}`,
		"x-api-key: mykeyvalue123456",
	} {
		out := Redact(in)
		if out == in {
			t.Errorf("expected redaction in %q, got %q", in, out)
		}
	}
}

func TestRedactEnvVarAssignment(t *testing.T) {
	in := "FREEINFERENCE_API_KEY=hyi-test-key-1234567890"
	out := Redact(in)
	if strings.Contains(out, "hyi-test-key-1234567890") {
		t.Errorf("env value leaked: %s", out)
	}
}

func TestRedactLeavesBenignTextAlone(t *testing.T) {
	for _, in := range []string{
		"",
		"hello world",
		"context usage is 84%",
		"model: glm-5.1",
		"cache read share 0.93",
		"/v1/models returned 42 entries",
	} {
		if Redact(in) != in {
			t.Errorf("benign text altered: %q -> %q", in, Redact(in))
		}
	}
}

func TestLooksLikeSecret(t *testing.T) {
	cases := map[string]bool{
		"":                     false,
		"hello":                false,
		"hyi-abcdef":           true,
		"sk-abcdef0123456789":  true,
		"Bearer abc1234567890": true,
		"normal text":          false,
		"context usage 80%":    false,
	}
	for in, want := range cases {
		if got := LooksLikeSecret(in); got != want {
			t.Errorf("LooksLikeSecret(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestScrubMapNested(t *testing.T) {
	in := map[string]any{
		"model": "glm-5.1",
		"nested": map[string]any{
			"key": "Bearer abcdefghijklmnopqrst",
		},
		"list": []any{"sk-abcdefghijklmnopqrstuvwxyz", "safe"},
	}
	out := ScrubMap(in)
	if got := out["model"]; got != "glm-5.1" {
		t.Errorf("benign value altered: %v", got)
	}
	nested, _ := out["nested"].(map[string]any)
	if v, _ := nested["key"].(string); !strings.Contains(v, RedactedPlaceholder) {
		t.Errorf("nested secret not redacted: %v", nested)
	}
	list, _ := out["list"].([]any)
	if v, _ := list[0].(string); !strings.Contains(v, RedactedPlaceholder) {
		t.Errorf("list secret not redacted: %v", list)
	}
}

func TestAllowlistAccountMapDropsUnknownFields(t *testing.T) {
	// Simulate a future upstream response that adds sensitive fields the
	// companion never asked for.
	in := map[string]any{
		"requests_used":  int64(1234),
		"requests_limit": int64(5000),
		"tokens_used":    int64(1_000_000),
		"tokens_limit":   int64(5_000_000),
		"reset_at":       "2026-08-01T00:00:00Z",
		// These MUST be dropped:
		"billing_email":  "user@example.com",
		"api_key_hint":   "hyi-abc...wxyz",
		"customer_id":    "cust_abc123",
		"payment_method": "visa-4242",
		"ip":             "1.2.3.4",
	}
	out := AllowlistAccountMap(in)
	for _, banned := range []string{"billing_email", "api_key_hint", "customer_id", "payment_method", "ip"} {
		if _, present := out[banned]; present {
			t.Errorf("allowlist leaked banned field %q: %+v", banned, out)
		}
	}
	if len(out) != 5 {
		t.Errorf("expected exactly 5 allowed fields, got %d: %+v", len(out), out)
	}
	// Allowed fields survive.
	if out["requests_used"] != int64(1234) {
		t.Errorf("allowed field mutated: %+v", out)
	}
}

func TestAllowlistAccountMapRedactsSuspiciousAllowedValues(t *testing.T) {
	// If an allowed field somehow carries a secret shape (defensive:
	// upstream bug), it must still be redacted.
	in := map[string]any{
		"requests_used": "Bearer hyi-should-not-be-here-1234567890",
	}
	out := AllowlistAccountMap(in)
	v, _ := out["requests_used"].(string)
	if !strings.Contains(v, RedactedPlaceholder) {
		t.Errorf("allowed-field secret not redacted: %v", v)
	}
}

func TestShortHashIsStableAndNonReversible(t *testing.T) {
	a := ShortHash("sess_abc123")
	b := ShortHash("sess_abc123")
	if a != b {
		t.Errorf("ShortHash not stable: %s != %s", a, b)
	}
	if len(a) != 8 {
		t.Errorf("ShortHash len = %d, want 8", len(a))
	}
	// Different inputs produce different hashes.
	if ShortHash("sess_other") == a {
		t.Error("ShortHash collided on different inputs")
	}
	// The original session ID is not present in the hash output.
	if strings.Contains(a, "sess_abc123") {
		t.Errorf("ShortHash leaked input: %s", a)
	}
}

func TestShortHashEmpty(t *testing.T) {
	if ShortHash("") != "" {
		t.Errorf("ShortHash(\"\") = %q, want \"\"", ShortHash(""))
	}
}

func TestMaskSessionID(t *testing.T) {
	got := MaskSessionID("sess_abcDEF1234567890")
	if !strings.HasPrefix(got, "sess") || !strings.HasSuffix(got, "7890") {
		t.Errorf("MaskSessionID lost prefix/suffix: %s", got)
	}
	if !strings.Contains(got, "...") {
		t.Errorf("MaskSessionID missing mask dots: %s", got)
	}
	// Empty stays empty.
	if MaskSessionID("") != "" {
		t.Errorf("MaskSessionID(\"\") = %q, want \"\"", MaskSessionID(""))
	}
	// Short IDs fall back to ShortHash so they don't reveal the full value.
	short := MaskSessionID("abc")
	if strings.Contains(short, "abc") {
		t.Errorf("short ID should hash, not mask: %s", short)
	}
}

// TestSanitizeFieldStripsTerminalControl is the regression test for the
// P0-6 finding that upstream model/provider/session strings could inject
// ANSI/VT100 escape sequences into rendered output. The companion itself
// never emits these, but a misbehaving client (or a hostile upstream) could
// surface them — once they are in a status line, terminal, or report they
// can cause real harm (cursor movement, OSC writes, title spoofing).
func TestSanitizeFieldStripsTerminalControl(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // exact sanitized output
	}{
		{name: "empty", input: "", want: ""},
		{name: "plain", input: "glm-5.1", want: "glm-5.1"},
		{name: "ANSI color", input: "\x1b[31mglm-5.1\x1b[0m", want: "glm-5.1"},
		{name: "OSC title", input: "\x1b]0;evil\x07glm-5.1", want: "glm-5.1"}, // whole OSC payload stripped
		{name: "carriage return newline", input: "glm-5.1\r\nevil", want: "glm-5.1  evil"},
		{name: "tab", input: "a\tb", want: "a b"},
		{name: "DEL", input: "a\x7fb", want: "ab"},
		{name: "null byte", input: "a\x00b", want: "ab"},
		{name: "trims surrounding whitespace", input: "  glm-5.1  ", want: "glm-5.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeField(tc.input)
			if got != tc.want {
				t.Errorf("SanitizeField(%q) = %q, want %q", tc.input, got, tc.want)
			}
			// No escape sequence may remain in the output.
			if strings.ContainsAny(got, "\x00\x07\x08\x0b\x0c\x1b\x7f") {
				t.Errorf("SanitizeField(%q) left control bytes: %q", tc.input, got)
			}
		})
	}
}

// TestSanitizeFieldBoundsLength ensures an attacker cannot DOS the renderer
// with a multi-megabyte model ID.
func TestSanitizeFieldBoundsLength(t *testing.T) {
	long := strings.Repeat("a", 100_000)
	got := SanitizeField(long)
	if len(got) > 256 {
		t.Errorf("SanitizeField did not bound length: got %d bytes", len(got))
	}
}
