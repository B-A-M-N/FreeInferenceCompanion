package api

import "time"

// NewClientForTest creates an API client without credential-safety validation.
// It is intended for test scaffolding only and must never be used in production.
//
// Deprecated: Use NewClient with approved endpoints in production code.
func NewClientForTest(baseURL, apiKey string, timeout time.Duration) *Client {
	return newClientLegacyWithMode(baseURL, apiKey, timeout, true)
}
