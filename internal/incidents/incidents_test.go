package incidents

import (
	"testing"
	"time"

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
