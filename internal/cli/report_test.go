package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func TestBuildReportModelMonitorOmitsUpstreamErrorBody(t *testing.T) {
	now := time.Now().UTC()
	ok := false
	monitor := buildReportModelMonitor(&schema.GlobalState{
		PublicStatus: &schema.PublicStatusCache{Models: []schema.PublicStatusModelCache{{
			ModelID: "model-a",
			Latest:  &schema.PublicStatusSampleCache{OK: &ok, CheckedAt: now, Error: "upstream internal details"},
		}}},
	}, "model-a", now)
	if monitor == nil {
		t.Fatal("expected model monitor")
	}
	encoded, err := json.Marshal(monitor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "upstream internal details") || strings.Contains(string(encoded), `"error"`) {
		t.Fatalf("upstream error body was copied into report: %s", encoded)
	}
}
