package engine

import (
	"fmt"
	"time"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ============================================================
// Token budget projection
// ============================================================

// BudgetProjection projects when the account quota will be exhausted based
// on current usage and observed burn rate.
type BudgetProjection struct {
	// Available is for display: "Quota healthy", "Quota low", etc.
	Status BudgetStatus `json:"status"`
	// Detail is a human-readable explanation.
	Detail string `json:"detail"`
	// RequestsRemaining is null when unknown.
	RequestsRemaining *int64 `json:"requests_remaining,omitempty"`
	// TokensRemaining is null when unknown.
	TokensRemaining *int64 `json:"tokens_remaining,omitempty"`
	// EstimatedExhaustion is the projected time the quota runs out, or nil
	// when it cannot be computed.
	EstimatedExhaustion *time.Time `json:"estimated_exhaustion,omitempty"`
}

// BudgetStatus classifies the quota headroom.
type BudgetStatus string

const (
	BudgetUnknown  BudgetStatus = "unknown"
	BudgetHealthy  BudgetStatus = "healthy"
	BudgetWatch    BudgetStatus = "watch"
	BudgetLow      BudgetStatus = "low"
	BudgetCritical BudgetStatus = "critical"
)

// ProjectBudget estimates quota exhaustion from account usage and the
// session's observed consumption rate. The burn rate is computed from
// usage observations: we sum the total tokens consumed across the
// observation window and divide by the elapsed time since the first
// observation.
//
// This is a conservative projection — it assumes the current burn rate
// continues unchanged. Real usage fluctuates, so the estimate is labeled
// as approximate.
//
// If the account-usage circuit breaker is open, the account usage data
// may be stale (not refreshed due to endpoint failures), and we return
// BudgetUnknown with a degraded confidence indication.
func ProjectBudget(au *schema.AccountUsage, snap *schema.Snapshot, now time.Time, circuitBreakers []schema.CircuitBreaker) BudgetProjection {
	// Check if account-usage circuit breaker is open — data may be stale.
	if isCircuitBreakerOpen(circuitBreakers, "account-usage", now) {
		return BudgetProjection{
			Status: BudgetUnknown,
			Detail: "Account usage data may be stale (circuit breaker open for account-usage endpoint). Run `freeinference refresh` to fetch fresh data.",
		}
	}

	if au == nil {
		return BudgetProjection{
			Status: BudgetUnknown,
			Detail: "Account usage data not available. Run `freeinference refresh` to fetch.",
		}
	}
	if err := schema.ValidateAccountUsage(au); err != nil {
		return BudgetProjection{
			Status: BudgetUnknown,
			Detail: "Account usage data is invalid and will not be used for budgeting: " + err.Error(),
		}
	}
	if age := now.Sub(au.FetchedAt); age < 0 || age > schema.DefaultAccountUsageMaxAge {
		return BudgetProjection{
			Status: BudgetUnknown,
			Detail: "Account usage data is stale; run `freeinference refresh` to fetch fresh data.",
		}
	}

	proj := BudgetProjection{}

	// Compute remaining quotas.
	var reqRemaining, tokRemaining *int64
	if au.RequestsLimit != nil && au.RequestsUsed != nil {
		r := *au.RequestsLimit - *au.RequestsUsed
		reqRemaining = &r
		proj.RequestsRemaining = reqRemaining
	}
	if au.TokensLimit != nil && au.TokensUsed != nil {
		t := *au.TokensLimit - *au.TokensUsed
		tokRemaining = &t
		proj.TokensRemaining = tokRemaining
	}

	// Compute burn rate from observations.
	burnRate, burnWindow := computeBurnRate(snap, now)

	// If we have a burn rate and token quota, project exhaustion.
	if burnRate > 0 && tokRemaining != nil && *tokRemaining > 0 {
		hoursRemaining := float64(*tokRemaining) / burnRate
		exhaustion := now.Add(time.Duration(hoursRemaining * float64(time.Hour)))
		proj.EstimatedExhaustion = &exhaustion
	}

	// Classify status based on remaining quota percentages.
	proj.Status = classifyBudgetStatus(reqRemaining, tokRemaining, au, burnWindow)

	// Build detail message.
	proj.Detail = buildBudgetDetail(proj.Status, reqRemaining, tokRemaining,
		au, burnRate, proj.EstimatedExhaustion, burnWindow)

	return proj
}

// computeBurnRate calculates tokens-per-hour consumption from the session's
// usage observations. Returns (tokensPerHour, observationWindowHours).
// Returns (0, 0) when there isn't enough data.
func computeBurnRate(snap *schema.Snapshot, now time.Time) (float64, float64) {
	if snap == nil || len(snap.UsageObservations) < 2 {
		return 0, 0
	}

	obs := snap.UsageObservations
	earliest := obs[0].ObservedAt
	if earliest.IsZero() {
		return 0, 0
	}

	elapsed := now.Sub(earliest)
	if elapsed < 5*time.Minute {
		// Not enough elapsed time for a reliable rate.
		return 0, 0
	}
	hoursElapsed := elapsed.Hours()
	if hoursElapsed <= 0 {
		return 0, 0
	}

	// Sum total tokens across all unique observations. Each observation
	// represents one request, so we sum fresh + cache_read + cache_creation
	// + output for each.
	var totalTokens int64
	for _, o := range obs {
		if o.FreshInputTokens != nil {
			totalTokens += *o.FreshInputTokens
		}
		if o.CacheReadInputTokens != nil {
			totalTokens += *o.CacheReadInputTokens
		}
		if o.CacheCreationInputTokens != nil {
			totalTokens += *o.CacheCreationInputTokens
		}
		if o.OutputTokens != nil {
			totalTokens += *o.OutputTokens
		}
	}

	return float64(totalTokens) / hoursElapsed, hoursElapsed
}

func classifyBudgetStatus(reqRem, tokRem *int64, au *schema.AccountUsage, burnWindow float64) BudgetStatus {
	// Token-based classification takes priority.
	if tokRem != nil && au.TokensLimit != nil && *au.TokensLimit > 0 {
		pct := float64(*tokRem) / float64(*au.TokensLimit)
		switch {
		case pct <= 0.05:
			return BudgetCritical
		case pct <= 0.15:
			return BudgetLow
		case pct <= 0.30:
			return BudgetWatch
		default:
			return BudgetHealthy
		}
	}
	// Fall back to request-based classification.
	if reqRem != nil && au.RequestsLimit != nil && *au.RequestsLimit > 0 {
		pct := float64(*reqRem) / float64(*au.RequestsLimit)
		switch {
		case pct <= 0.05:
			return BudgetCritical
		case pct <= 0.15:
			return BudgetLow
		case pct <= 0.30:
			return BudgetWatch
		default:
			return BudgetHealthy
		}
	}
	return BudgetUnknown
}

func buildBudgetDetail(status BudgetStatus, reqRem, tokRem *int64,
	au *schema.AccountUsage, burnRate float64,
	exhaustion *time.Time, burnWindow float64) string {

	switch status {
	case BudgetUnknown:
		return "Account quota data incomplete — limits not reported."
	case BudgetHealthy:
		detail := "Quota headroom is healthy."
		if exhaustion != nil {
			detail += fmt.Sprintf(" At current rate (~%.0fK tok/hr over %.1fh), quota lasts until %s.",
				burnRate/1000, burnWindow, exhaustion.Format("Jan 2 15:04"))
		}
		return detail
	case BudgetWatch:
		detail := "Quota usage is moderate — monitor consumption."
		if exhaustion != nil {
			detail += fmt.Sprintf(" At current rate (~%.0fK tok/hr), quota projects to exhaust around %s.",
				burnRate/1000, exhaustion.Format("Jan 2 15:04"))
		}
		return detail
	case BudgetLow:
		detail := "Quota running low — consider reducing usage."
		if exhaustion != nil {
			detail += fmt.Sprintf(" At current rate (~%.0fK tok/hr), quota projects to exhaust around %s.",
				burnRate/1000, exhaustion.Format("Jan 2 15:04"))
		}
		return detail
	case BudgetCritical:
		detail := "Quota critically low — near exhaustion."
		if exhaustion != nil {
			detail += fmt.Sprintf(" At current rate, quota projects to exhaust around %s.",
				exhaustion.Format("Jan 2 15:04"))
		}
		return detail
	}
	return ""
}

// isCircuitBreakerOpen checks if a specific endpoint's circuit breaker is open.
func isCircuitBreakerOpen(cbs []schema.CircuitBreaker, endpoint string, now time.Time) bool {
	for _, cb := range cbs {
		if cb.Endpoint == endpoint && cb.State == schema.CircuitOpen {
			if cb.NextRetryAt != nil && now.Before(*cb.NextRetryAt) {
				return true
			}
		}
	}
	return false
}
