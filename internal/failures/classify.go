// Package failures normalizes client-reported failures into bounded,
// shareable metadata. It never returns the original error body.
package failures

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/secure"
)

const maxInputBytes = 64 << 10

const (
	RateLimit            = "rate_limit"
	AuthenticationFailed = "authentication_failed"
	PermissionDenied     = "permission_denied"
	InvalidRequest       = "invalid_request"
	RequestTimeout       = "request_timeout"
	ModelNotFound        = "model_not_found"
	Overloaded           = "overloaded"
	BadGateway           = "bad_gateway"
	GatewayTimeout       = "gateway_timeout"
	ServerError          = "server_error"
	NetworkError         = "network_error"
	TLSError             = "tls_error"
	Cancelled            = "cancelled"
	MaxOutputTokens      = "max_output_tokens"
	Unknown              = "unknown"
)

// Metadata contains only fields that are safe to persist in local support
// artifacts. All pointers are nil when the source did not provide evidence.
type Metadata struct {
	Category          string
	HTTPStatus        *int
	Retryable         *bool
	TransportClass    string
	ProviderErrorType string
	ErrorOrigin       string
	RetryAfterSeconds *int64
	RequestReference  string
}

var (
	statusMarkerPattern = regexp.MustCompile(`(?i)\b(?:https?\s+status|http\s+error|status(?:_code)?|code|error)\s*[:=]?\s*([1-5][0-9]{2})\b`)
	statusPattern       = regexp.MustCompile(`\b(400|401|403|404|408|409|429|500|501|502|503|504|520|521|522|523|524|529)\b`)
	retryAfterPattern   = regexp.MustCompile(`(?i)\bretry[- ]after\s*[:=]?\s*([0-9]{1,6})\b`)
	requestRefPattern   = regexp.MustCompile(`(?i)\b(?:request[- _]?(?:id|reference)|cf[- _]?ray)\s*[:=]\s*([A-Za-z0-9._:/-]{1,128})\b`)
)

type structuredFields struct {
	status            int
	providerErrorType string
	retryAfter        int64
	hasRetryAfter     bool
	requestReference  string
	cloudflare        bool
	found             bool
}

// Normalize converts an untrusted client error into safe diagnostic fields.
// Parsing is bounded and follows the contract: structured JSON, explicit
// status, rate-limit markers, transport markers, semantic markers, unknown.
func Normalize(raw string) Metadata {
	if len(raw) > maxInputBytes {
		raw = raw[:maxInputBytes]
	}
	text := strings.TrimSpace(raw)
	structured := parseStructured(text)
	status := structured.status
	if status == 0 {
		status = parseHTTPStatus(text)
	}

	meta := Metadata{
		HTTPStatus:        optionalStatus(status),
		ProviderErrorType: safeScalar(structured.providerErrorType, 128),
		RequestReference:  safeScalar(structured.requestReference, 128),
	}
	if structured.hasRetryAfter {
		meta.RetryAfterSeconds = optionalRetryAfter(structured.retryAfter)
	}
	if meta.RetryAfterSeconds == nil {
		if seconds, ok := parseRetryAfter(text); ok {
			meta.RetryAfterSeconds = optionalRetryAfter(seconds)
		}
	}
	if meta.RequestReference == "" {
		if matches := requestRefPattern.FindStringSubmatch(text); len(matches) == 2 {
			meta.RequestReference = safeScalar(matches[1], 128)
		}
	}

	category, transport := classify(text, status, meta.ProviderErrorType)
	meta.Category = category
	meta.TransportClass = transport
	meta.Retryable = retryableFor(category)
	switch {
	case structured.cloudflare || strings.Contains(strings.ToLower(text), "cloudflare") || strings.Contains(strings.ToLower(text), "cf-ray"):
		meta.ErrorOrigin = "cloudflare"
	case structured.found || status != 0:
		meta.ErrorOrigin = "provider"
	case transport != "":
		meta.ErrorOrigin = "transport"
	default:
		meta.ErrorOrigin = "client"
	}
	return meta
}

// Classify is a convenience for callers that only need the bounded category.
func Classify(raw string) string {
	return Normalize(raw).Category
}

func parseStructured(raw string) structuredFields {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start || end-start+1 > maxInputBytes {
		return structuredFields{}
	}
	var root any
	if err := json.Unmarshal([]byte(raw[start:end+1]), &root); err != nil {
		return structuredFields{}
	}
	result := structuredFields{}
	walkStructured(root, 0, &result)
	return result
}

func walkStructured(value any, depth int, result *structuredFields) {
	if depth > 6 || result == nil {
		return
	}
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			normalizedKey := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
			switch normalizedKey {
			case "request_id", "request_reference", "cf_ray", "ray_id":
				if scalar, ok := child.(string); ok && result.requestReference == "" {
					result.requestReference = scalar
				}
				if normalizedKey == "cf_ray" || normalizedKey == "ray_id" {
					result.cloudflare = true
				}
			case "type", "error_type", "provider_error_type":
				if scalar, ok := child.(string); ok && result.providerErrorType == "" {
					result.providerErrorType = scalar
				}
			case "status", "status_code", "http_status":
				if parsed, ok := numberValue(child); ok && isHTTPStatus(parsed) && result.status == 0 {
					result.status = parsed
				}
			case "code":
				if parsed, ok := numberValue(child); ok && isHTTPStatus(parsed) && result.status == 0 {
					result.status = parsed
				} else if scalar, ok := child.(string); ok && result.providerErrorType == "" {
					result.providerErrorType = scalar
				}
			case "retry_after", "retryafter", "retry_after_seconds":
				if parsed, ok := numberValue(child); ok && parsed >= 0 && parsed <= 604800 {
					result.retryAfter = int64(parsed)
					result.hasRetryAfter = true
				}
			}
			if scalar, ok := child.(string); ok && strings.Contains(strings.ToLower(scalar), "cloudflare") {
				result.cloudflare = true
			}
			walkStructured(child, depth+1, result)
		}
	case []any:
		for _, child := range item {
			walkStructured(child, depth+1, result)
		}
	}
	result.found = true
}

func numberValue(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		return int(number), number == float64(int(number))
	case json.Number:
		parsed, err := strconv.Atoi(string(number))
		return parsed, err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(number))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func parseHTTPStatus(raw string) int {
	if matches := statusMarkerPattern.FindStringSubmatch(raw); len(matches) == 2 {
		if status, err := strconv.Atoi(matches[1]); err == nil && isHTTPStatus(status) {
			return status
		}
	}
	if matches := statusPattern.FindStringSubmatch(raw); len(matches) == 2 {
		status, _ := strconv.Atoi(matches[1])
		return status
	}
	return 0
}

func parseRetryAfter(raw string) (int64, bool) {
	matches := retryAfterPattern.FindStringSubmatch(raw)
	if len(matches) != 2 {
		return 0, false
	}
	seconds, err := strconv.ParseInt(matches[1], 10, 64)
	return seconds, err == nil && seconds >= 0 && seconds <= 604800
}

func classify(raw string, status int, providerType string) (string, string) {
	lower := strings.ToLower(raw + " " + providerType)
	switch status {
	case 401:
		return AuthenticationFailed, ""
	case 403:
		return PermissionDenied, ""
	case 400:
		return InvalidRequest, ""
	case 404:
		return ModelNotFound, ""
	case 408:
		return RequestTimeout, "timeout"
	case 429:
		return RateLimit, ""
	case 502:
		return BadGateway, ""
	case 504, 524:
		return GatewayTimeout, "timeout"
	case 520, 521, 522, 523:
		if status == 522 {
			return GatewayTimeout, "connect"
		}
		return ServerError, ""
	case 529:
		return Overloaded, ""
	case 500, 501:
		return ServerError, ""
	case 503:
		if strings.Contains(lower, "overload") || strings.Contains(lower, "capacity") || strings.Contains(lower, "busy") || strings.Contains(lower, "unavailable") {
			return Overloaded, ""
		}
		return ServerError, ""
	}

	if strings.Contains(lower, "cancelled") || strings.Contains(lower, "canceled") || strings.Contains(lower, "context canceled") {
		return Cancelled, ""
	}
	if strings.Contains(lower, "tls") || strings.Contains(lower, "x509") || strings.Contains(lower, "certificate") {
		return TLSError, "tls"
	}
	if strings.Contains(lower, "connection reset") || strings.Contains(lower, "reset by peer") {
		return NetworkError, "connection_reset"
	}
	if strings.Contains(lower, "no such host") || strings.Contains(lower, "dns") || strings.Contains(lower, "name resolution") {
		return NetworkError, "dns"
	}
	if strings.Contains(lower, "connect timeout") {
		return RequestTimeout, "connect"
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "dial tcp") {
		return NetworkError, "connect"
	}
	if strings.Contains(lower, "read timeout") || strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout") {
		return RequestTimeout, "timeout"
	}
	if strings.Contains(lower, "network") || strings.Contains(lower, "transport") || strings.Contains(lower, "unexpected eof") {
		return NetworkError, "network"
	}
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "throttl") || strings.Contains(lower, "quota exceeded") || strings.Contains(lower, "error 1015") {
		return RateLimit, ""
	}
	if strings.Contains(lower, "overload") || strings.Contains(lower, "capacity") {
		return Overloaded, ""
	}
	if strings.Contains(lower, "auth") || strings.Contains(lower, "unauthor") || strings.Contains(lower, "api key") {
		return AuthenticationFailed, ""
	}
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "forbidden") {
		return PermissionDenied, ""
	}
	if strings.Contains(lower, "model not found") || strings.Contains(lower, "model_not_found") {
		return ModelNotFound, ""
	}
	if strings.Contains(lower, "max_output") || strings.Contains(lower, "max tokens") || strings.Contains(lower, "maximum output") {
		return MaxOutputTokens, ""
	}
	if strings.Contains(lower, "invalid request") || strings.Contains(lower, "invalid_request") {
		return InvalidRequest, ""
	}
	if strings.Contains(lower, "bad gateway") {
		return BadGateway, ""
	}
	if strings.Contains(lower, "gateway timeout") {
		return GatewayTimeout, "timeout"
	}
	if strings.Contains(lower, "server error") || strings.Contains(lower, "internal server") {
		return ServerError, ""
	}
	return Unknown, ""
}

func isHTTPStatus(status int) bool {
	return status >= 400 && status <= 599
}

func optionalStatus(status int) *int {
	if !isHTTPStatus(status) {
		return nil
	}
	return &status
}

func optionalRetryAfter(seconds int64) *int64 {
	if seconds < 0 || seconds > 604800 {
		return nil
	}
	return &seconds
}

func retryableFor(category string) *bool {
	value := false
	switch category {
	case RateLimit, RequestTimeout, Overloaded, BadGateway, GatewayTimeout, ServerError, NetworkError:
		value = true
	case TLSError, AuthenticationFailed, PermissionDenied, InvalidRequest, ModelNotFound, Cancelled, MaxOutputTokens:
		// These categories are evidence that retrying the same request is
		// unlikely to help. Keep the explicit false value so consumers can
		// distinguish that conclusion from an unavailable signal.
		value = false
	case Unknown:
		// An unrecognized error does not provide enough evidence to infer a
		// retry policy. Keep this optional rather than inventing "false".
		return nil
	default:
		return nil
	}
	return &value
}

func safeScalar(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" || secure.LooksLikeSecret(value) {
		return ""
	}
	value = secure.Redact(secure.SanitizeField(value))
	if value == secure.RedactedPlaceholder {
		return ""
	}
	if len(value) > max {
		value = value[:max]
	}
	return value
}
