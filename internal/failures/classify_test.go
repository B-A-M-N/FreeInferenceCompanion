package failures

import (
	"strings"
	"testing"
)

func TestNormalizeHTTPAndTransportFailures(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		category string
		status   int
		class    string
		retry    bool
	}{
		{"plain 429", "429", RateLimit, 429, "", true},
		{"http 429", "HTTP 429 Too Many Requests", RateLimit, 429, "", true},
		{"500", "HTTP 500", ServerError, 500, "", true},
		{"502 cloudflare", `{"success":false,"errors":[{"code":502,"message":"bad gateway"}],"cf-ray":"abc123"}`, BadGateway, 502, "", true},
		{"503 overloaded", "503 service unavailable: overloaded", Overloaded, 503, "", true},
		{"504", "status=504 gateway timeout", GatewayTimeout, 504, "timeout", true},
		{"520", "HTTP 520", ServerError, 520, "", true},
		{"521", "HTTP 521", ServerError, 521, "", true},
		{"522", "HTTP 522", GatewayTimeout, 522, "connect", true},
		{"523", "HTTP 523", ServerError, 523, "", true},
		{"524", "HTTP 524", GatewayTimeout, 524, "timeout", true},
		{"connect reset", "read: connection reset by peer", NetworkError, 0, "connection_reset", true},
		{"dns", "dial tcp: lookup api: no such host", NetworkError, 0, "dns", true},
		{"tls", "tls handshake failed: x509 certificate", TLSError, 0, "tls", false},
		{"timeout", "connect timeout", RequestTimeout, 0, "connect", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.input)
			if got.Category != tc.category || got.TransportClass != tc.class {
				t.Fatalf("metadata=%+v, want category=%s class=%s", got, tc.category, tc.class)
			}
			if tc.status == 0 && got.HTTPStatus != nil || tc.status != 0 && (got.HTTPStatus == nil || *got.HTTPStatus != tc.status) {
				t.Fatalf("status=%v, want %d", got.HTTPStatus, tc.status)
			}
			if got.Retryable == nil || *got.Retryable != tc.retry {
				t.Fatalf("retryable=%v, want %t", got.Retryable, tc.retry)
			}
		})
	}
}

func TestNormalizeStructuredFieldsAndPrivacy(t *testing.T) {
	input := `prefix {"error":{"type":"rate_limit_error","message":"API key sk-secret-value-1234567890"},"status":429,"retry_after":7,"request_id":"req-123"} suffix`
	got := Normalize(input)
	if got.Category != RateLimit || got.HTTPStatus == nil || *got.HTTPStatus != 429 || got.RetryAfterSeconds == nil || *got.RetryAfterSeconds != 7 {
		t.Fatalf("structured metadata=%+v", got)
	}
	if got.ProviderErrorType != "rate_limit_error" || got.RequestReference != "req-123" || got.ErrorOrigin != "provider" {
		t.Fatalf("structured metadata lost safe fields=%+v", got)
	}
	if strings.Contains(got.ProviderErrorType, "sk-secret") || strings.Contains(got.RequestReference, "sk-secret") {
		t.Fatal("secret-shaped input reached normalized metadata")
	}
}

func TestNormalizeBoundsInputAndDoesNotPersistBody(t *testing.T) {
	got := Normalize(strings.Repeat(`{"error":{"message":"502 bad gateway"}}`, 10000))
	if got.Category != BadGateway {
		t.Fatalf("bounded body category=%q", got.Category)
	}
	if strings.Contains(got.ProviderErrorType, "message") {
		t.Fatal("raw body was returned as metadata")
	}
}

func FuzzNormalizeBoundedMetadata(f *testing.F) {
	f.Add(`{"error":{"type":"rate_limit_error","status":429}}`)
	f.Add("tls handshake failed")
	f.Fuzz(func(t *testing.T, input string) {
		got := Normalize(input)
		if len(got.ProviderErrorType) > 128 || len(got.ErrorOrigin) > 64 || len(got.TransportClass) > 64 || len(got.RequestReference) > 128 {
			t.Fatalf("metadata exceeded bounds: %+v", got)
		}
	})
}
