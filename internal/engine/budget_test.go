package engine

import (
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func TestProjectBudget_NoAccountUsage(t *testing.T) {
	proj := ProjectBudget(nil, nil, time.Now())
	if proj.Status != BudgetUnknown {
		t.Errorf("expected unknown, got %s", proj.Status)
	}
}

func TestProjectBudget_HealthyQuota(t *testing.T) {
	au := &schema.AccountUsage{
		TokensUsed:  ptrI64(10000),
		TokensLimit: ptrI64(1000000),
	}
	snap := &schema.Snapshot{}
	proj := ProjectBudget(au, snap, time.Now())
	if proj.Status != BudgetHealthy {
		t.Errorf("expected healthy, got %s", proj.Status)
	}
}

func TestProjectBudget_WatchQuota(t *testing.T) {
	au := &schema.AccountUsage{
		TokensUsed:  ptrI64(750000),
		TokensLimit: ptrI64(1000000),
	}
	snap := &schema.Snapshot{}
	proj := ProjectBudget(au, snap, time.Now())
	if proj.Status != BudgetWatch {
		t.Errorf("expected watch, got %s", proj.Status)
	}
}

func TestProjectBudget_LowQuota(t *testing.T) {
	au := &schema.AccountUsage{
		TokensUsed:  ptrI64(870000),
		TokensLimit: ptrI64(1000000),
	}
	snap := &schema.Snapshot{}
	proj := ProjectBudget(au, snap, time.Now())
	if proj.Status != BudgetLow {
		t.Errorf("expected low, got %s", proj.Status)
	}
}

func TestProjectBudget_CriticalQuota(t *testing.T) {
	au := &schema.AccountUsage{
		TokensUsed:  ptrI64(960000),
		TokensLimit: ptrI64(1000000),
	}
	snap := &schema.Snapshot{}
	proj := ProjectBudget(au, snap, time.Now())
	if proj.Status != BudgetCritical {
		t.Errorf("expected critical, got %s", proj.Status)
	}
}

func TestProjectBudget_RequestBasedFallback(t *testing.T) {
	// No token limit, but request limit available.
	au := &schema.AccountUsage{
		RequestsUsed:  ptrI64(9000),
		RequestsLimit: ptrI64(10000),
	}
	snap := &schema.Snapshot{}
	proj := ProjectBudget(au, snap, time.Now())
	if proj.Status != BudgetLow {
		t.Errorf("expected low via request fallback, got %s", proj.Status)
	}
}

func TestProjectBudget_BurnRateExhaustion(t *testing.T) {
	used := int64(500000)
	limit := int64(1000000)
	au := &schema.AccountUsage{
		TokensUsed:  &used,
		TokensLimit: &limit,
	}
	// Create observations over 1 hour: 10 requests, each consuming
	// ~10K tokens total. Burn rate = 100K tokens/hour.
	// Remaining = 500K, so exhaustion = 5 hours from now.
	now := time.Now().UTC()
	fresh := int64(5000)
	read := int64(3000)
	create := int64(1000)
	output := int64(1000)
	obs := make([]schema.UsageObservation, 10)
	for i := range obs {
		obs[i] = schema.UsageObservation{
			ObservedAt:               now.Add(-time.Duration(10-i) * 6 * time.Minute),
			FreshInputTokens:         &fresh,
			CacheReadInputTokens:     &read,
			CacheCreationInputTokens: &create,
			OutputTokens:             &output,
		}
	}
	snap := &schema.Snapshot{UsageObservations: obs}

	proj := ProjectBudget(au, snap, now)
	if proj.EstimatedExhaustion == nil {
		t.Fatal("expected an exhaustion estimate")
	}
	// Should be roughly 5 hours from now (±1h tolerance for rounding).
	delta := proj.EstimatedExhaustion.Sub(now).Hours()
	if delta < 4 || delta > 6 {
		t.Errorf("expected ~5h to exhaustion, got %.1fh", delta)
	}
}

func TestProjectBudget_InsufficientBurnData(t *testing.T) {
	au := &schema.AccountUsage{
		TokensUsed:  ptrI64(500000),
		TokensLimit: ptrI64(1000000),
	}
	// Only 1 observation — not enough for burn rate.
	snap := &schema.Snapshot{
		UsageObservations: []schema.UsageObservation{
			{ObservedAt: time.Now()},
		},
	}
	proj := ProjectBudget(au, snap, time.Now())
	if proj.EstimatedExhaustion != nil {
		t.Error("expected no exhaustion estimate with insufficient data")
	}
}
