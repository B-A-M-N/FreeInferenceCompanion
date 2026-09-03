package incidents

import (
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func TestCollectAggregatesTypedFailuresWithoutRawIDs(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	sessionID := "session-with-sensitive-looking-id"
	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: sessionID, StartedAt: now, LastEventAt: now, Status: schema.SessionCompleted},
		Model:         schema.ModelInfo{ID: "model-a"},
		Provider:      schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true},
	}
	if err := state.UpdateSessionIndex(paths, snap); err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, sessionID); err != nil {
		t.Fatal(err)
	}
	status := 429
	retry := true
	if err := state.AppendEvent(paths, schema.ClientClaudeCode, sessionID, state.Event{
		Type:             state.EventTurnFailed,
		At:               now,
		Model:            "model-a",
		Provider:         "freeinference",
		Detail:           "rate_limit",
		HTTPStatus:       &status,
		Retryable:        &retry,
		RequestReference: "req-123",
	}); err != nil {
		t.Fatal(err)
	}

	report, err := Collect(paths, Filter{Since: now.Add(-time.Minute)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || len(report.Recent) != 1 || report.Recent[0].Category != "rate_limit" {
		t.Fatalf("report=%+v", report)
	}
	if report.Recent[0].SessionID == sessionID || report.Recent[0].HTTPStatus == nil || *report.Recent[0].HTTPStatus != 429 {
		t.Fatalf("incident leaked or lost metadata: %+v", report.Recent[0])
	}
}

func TestCollectCorrelatesNearestPublicMonitorSampleAndStatus(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sessionID := "session-correlation"
	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: sessionID, StartedAt: now, LastEventAt: now, Status: schema.SessionActive},
		Model:         schema.ModelInfo{ID: "glm-5.2"},
		Provider:      schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true},
	}
	if err := state.UpdateSessionIndex(paths, snap); err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, sessionID); err != nil {
		t.Fatal(err)
	}
	status := 502
	retryable := true
	if err := state.AppendEvent(paths, schema.ClientClaudeCode, sessionID, state.Event{
		Type:       state.EventTurnFailed,
		Model:      "glm-5.2",
		Provider:   schema.ProviderFreeInference,
		Detail:     "bad_gateway",
		HTTPStatus: &status,
		Retryable:  &retryable,
	}); err != nil {
		t.Fatal(err)
	}
	monitorAt := time.Now().UTC().Add(-time.Second)
	monitorOK := true
	latency := int64(27362)
	if err := state.SavePublicStatus(paths, &schema.PublicStatusCache{
		Source: api.PublicStatusSource,
		Models: []schema.PublicStatusModelCache{{
			ModelID: "glm-5.2",
			Latest:  &schema.PublicStatusSampleCache{OK: &monitorOK, CheckedAt: monitorAt, LatencyMs: &latency},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	report, err := Collect(paths, Filter{Since: now.Add(-time.Minute)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || len(report.ByStatus) != 1 || report.ByStatus[0].Name != "502" {
		t.Fatalf("status aggregation = %+v", report)
	}
	if report.Recent[0].PublicMonitor == nil || report.Recent[0].PublicMonitor.Status != "up" || report.Recent[0].PublicMonitor.LatencyMs == nil {
		t.Fatalf("monitor correlation = %+v", report.Recent[0].PublicMonitor)
	}
}

func TestCollectCorrelatesSecretShapedModelAfterSanitization(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sessionID := "session-secret-shaped-model"
	rawModel := "hyi-model-alpha-abcdef0123456789"
	safeModel := secure.SafeIdentifier(rawModel)
	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: sessionID, StartedAt: now, LastEventAt: now, Status: schema.SessionActive},
		Model:         schema.ModelInfo{ID: rawModel},
		Provider:      schema.ProviderInfo{Name: schema.ProviderFreeInference, Confirmed: true},
	}
	if err := state.UpdateSessionIndex(paths, snap); err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, sessionID); err != nil {
		t.Fatal(err)
	}
	status := 429
	if err := state.AppendEvent(paths, schema.ClientClaudeCode, sessionID, state.Event{
		Type:       state.EventTurnFailed,
		Model:      rawModel,
		Provider:   schema.ProviderFreeInference,
		Detail:     "rate_limit",
		HTTPStatus: &status,
	}); err != nil {
		t.Fatal(err)
	}
	ok := true
	if err := state.SavePublicStatus(paths, &schema.PublicStatusCache{
		Source: api.PublicStatusSource,
		Models: []schema.PublicStatusModelCache{{
			ModelID: safeModel,
			Latest:  &schema.PublicStatusSampleCache{OK: &ok, CheckedAt: now},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	report, err := Collect(paths, Filter{Since: now.Add(-time.Minute)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Recent) != 1 || report.Recent[0].Model != safeModel || report.Recent[0].PublicMonitor == nil {
		t.Fatalf("secret-shaped model correlation = %+v", report.Recent)
	}
}

func TestCollectModelFilterUsesEventModelAfterSwitch(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sessionID := "session-model-switch"
	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: sessionID, StartedAt: now, LastEventAt: now, Status: schema.SessionActive},
		Model:         schema.ModelInfo{ID: "old-model"},
	}
	if err := state.UpdateSessionIndex(paths, snap); err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendEvent(paths, schema.ClientClaudeCode, sessionID, state.Event{Type: state.EventTurnFailed, Model: "new-model", Detail: "unknown"}); err != nil {
		t.Fatal(err)
	}
	report, err := Collect(paths, Filter{Model: "new-model", Since: now.Add(-time.Minute)}, now)
	if err != nil || report.Total != 1 {
		t.Fatalf("model-switch filter report=%+v err=%v", report, err)
	}
}

func TestCollectDoesNotCorrelateNonFreeInferenceFailures(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sessionID := "session-other-provider"
	snap := &schema.Snapshot{
		SchemaVersion: schema.StateVersion,
		Client:        schema.ClientInfo{Type: schema.ClientClaudeCode},
		Session:       schema.SessionInfo{ID: sessionID, StartedAt: now, LastEventAt: now, Status: schema.SessionActive},
		Model:         schema.ModelInfo{ID: "shared-model"},
	}
	if err := state.UpdateSessionIndex(paths, snap); err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureSessionDir(schema.ClientClaudeCode, sessionID); err != nil {
		t.Fatal(err)
	}
	status := 502
	if err := state.AppendEvent(paths, schema.ClientClaudeCode, sessionID, state.Event{
		Type:       state.EventTurnFailed,
		Model:      "shared-model",
		Provider:   "another-provider",
		Detail:     "bad_gateway",
		HTTPStatus: &status,
	}); err != nil {
		t.Fatal(err)
	}
	ok := false
	if err := state.SavePublicStatus(paths, &schema.PublicStatusCache{
		Source: api.PublicStatusSource,
		Models: []schema.PublicStatusModelCache{{
			ModelID: "shared-model",
			Latest:  &schema.PublicStatusSampleCache{OK: &ok, CheckedAt: now},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	report, err := Collect(paths, Filter{Since: now.Add(-time.Minute)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Recent) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Recent[0].PublicMonitor != nil {
		t.Fatalf("non-FreeInference failure was correlated: %+v", report.Recent[0].PublicMonitor)
	}
}
