package api

import "testing"

func TestNormalizeRouteCollapsesAllTrailingSlashes(t *testing.T) {
	for _, raw := range []string{"https://freeinference.org/v1", "https://freeinference.org/v1/", "https://freeinference.org/v1//"} {
		route, endpoint, err := NormalizeRoute(raw)
		if err != nil {
			t.Fatalf("NormalizeRoute(%q): %v", raw, err)
		}
		if route != "https://freeinference.org/v1" || !endpoint.IsFI {
			t.Fatalf("NormalizeRoute(%q) = %q, endpoint=%+v", raw, route, endpoint)
		}
	}
	route, endpoint, err := NormalizeRoute("https://freeinference.org/anthropic/")
	if err != nil || route != "https://freeinference.org/anthropic" || !endpoint.IsFI {
		t.Fatalf("anthropic normalize = %q, %+v, %v", route, endpoint, err)
	}
}

func TestClientRouteClassifierRequiresClientSpecificPath(t *testing.T) {
	if !IsFIRoute("https://freeinference.org/anthropic///", ClaudeRoutePath) {
		t.Fatal("Claude /anthropic route rejected")
	}
	if !IsFIRoute("https://freeinference.org/v1///", CodexRoutePath) {
		t.Fatal("Codex /v1 route rejected")
	}
	if IsFIRoute("https://freeinference.org/v1/", ClaudeRoutePath) {
		t.Fatal("Claude classifier accepted management /v1 route")
	}
	if IsFIRoute("https://freeinference.org/anthropic/", CodexRoutePath) {
		t.Fatal("Codex classifier accepted Claude /anthropic route")
	}
}

func TestClientRouteClassifierRejectsNearMatches(t *testing.T) {
	for _, raw := range []string{
		"https://freeinference.org/anthropic?token=secret",
		"https://freeinference.org/anthropic#fragment",
		"https://freeinference.org/anthropic-extra",
		"https://freeinference.org/%61nthropic",
		"https://freeinference.org/ANTHROPIC",
		"https://freeinference.org:8443/anthropic",
		"https://freeinference.org:/anthropic",
		"https://user:pass@freeinference.org/anthropic",
		"http://freeinference.org/anthropic",
	} {
		if IsFIRoute(raw, ClaudeRoutePath) {
			t.Errorf("near-match Claude route accepted: %q", raw)
		}
	}
}
