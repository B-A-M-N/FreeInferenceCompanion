package cli

import (
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
	if monitor.Error != "" {
		t.Fatalf("upstream error body was copied into report: %q", monitor.Error)
	}
}
