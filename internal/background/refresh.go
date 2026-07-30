package background

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// Worker names / circuit breaker endpoint IDs.
const (
	WorkerModels       = "models"
	WorkerHealth       = "health"
	WorkerAccountUsage = "account-usage"
)

// Cache TTLs.
const (
	ModelsTTL       = 6 * time.Hour
	HealthTTL       = 120 * time.Second
	AccountUSageTTL = 60 * time.Minute
)

// Backoff intervals for consecutive failures: 2 → 5 → 15 → 30 minutes.
var backoffIntervals = []time.Duration{
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
}

// RefreshResult summarizes what a refresh pass did.
type RefreshResult struct {
	Worker                string `json:"worker,omitempty"`
	ModelsRefreshed       bool   `json:"models_refreshed"`
	HealthRefreshed       bool   `json:"health_refreshed"`
	AccountUsageRefreshed bool   `json:"account_usage_refreshed"`
	Skipped               bool   `json:"skipped"`
	SkipReason            string `json:"skip_reason,omitempty"`
	Error                 string `json:"error,omitempty"`
}

// Refresher performs provider metadata refreshes. Cross-process coalescing
// is done with non-blocking file locks; there is no in-process state.
type Refresher struct {
	Client    *api.Client
	Paths     state.Paths
	HealthURL string
}

// NewRefresher creates a new Refresher.
func NewRefresher(client *api.Client, paths state.Paths, healthURL string) *Refresher {
	return &Refresher{
		Client:    client,
		Paths:     paths,
		HealthURL: healthURL,
	}
}

// ============================================================
// Worker path (freeinference refresh --worker <name>)
// ============================================================

// WorkerRefresh runs one refresh worker under a non-blocking process lock.
// Concurrent workers coalesce: the second process to arrive skips immediately.
func (r *Refresher) WorkerRefresh(worker string) *RefreshResult {
	result := &RefreshResult{Worker: worker}

	if worker != WorkerModels && worker != WorkerHealth && worker != WorkerAccountUsage {
		result.Skipped = true
		result.SkipReason = "unknown worker"
		return result
	}

	if err := r.Paths.EnsureDirs(); err != nil {
		result.Error = "ensure dirs"
		return result
	}

	fl := state.NewFileLock(r.Paths.RefreshLock(worker))
	if err := fl.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			result.Skipped = true
			result.SkipReason = "another worker running"
			return result
		}
		result.Error = "acquire lock"
		return result
	}
	defer fl.Release()

	now := time.Now()
	gs, _ := state.LoadGlobal(r.Paths)

	// Circuit breaker gate.
	if breakerOpen(gs, worker, now) {
		result.Skipped = true
		result.SkipReason = "circuit breaker open"
		return result
	}

	switch worker {
	case WorkerModels:
		if !modelsStale(gs, now) {
			result.Skipped = true
			result.SkipReason = "cache fresh"
			return result
		}
		r.refreshModels(result, now)
	case WorkerHealth:
		if r.HealthURL == "" {
			result.Skipped = true
			result.SkipReason = "no health source configured"
			return result
		}
		if !healthStale(gs, now) {
			result.Skipped = true
			result.SkipReason = "cache fresh"
			return result
		}
		r.refreshHealth(result, now)
	case WorkerAccountUsage:
		if r.Client == nil || r.Client.APIKey() == "" {
			result.Skipped = true
			result.SkipReason = "no API key configured"
			return result
		}
		if !accountUsageStale(gs, now) {
			result.Skipped = true
			result.SkipReason = "cache fresh"
			return result
		}
		r.refreshAccountUsage(result, now)
	}
	return result
}

// ============================================================
// Synchronous paths (manual CLI)
// ============================================================

// RefreshIfStale synchronously refreshes any stale resource whose breaker is
// closed. Intended for interactive use (freeinference refresh).
func (r *Refresher) RefreshIfStale() *RefreshResult {
	result := &RefreshResult{}
	now := time.Now()
	gs, _ := state.LoadGlobal(r.Paths)

	if modelsStale(gs, now) && !breakerOpen(gs, WorkerModels, now) {
		res := r.WorkerRefresh(WorkerModels)
		result.ModelsRefreshed = res.ModelsRefreshed
		if res.Error != "" {
			result.Error = res.Error
		}
	}
	if r.HealthURL != "" && healthStale(gs, now) && !breakerOpen(gs, WorkerHealth, now) {
		res := r.WorkerRefresh(WorkerHealth)
		result.HealthRefreshed = res.HealthRefreshed
		if res.Error != "" && result.Error == "" {
			result.Error = res.Error
		}
	}
	if r.Client != nil && r.Client.APIKey() != "" &&
		!breakerOpen(gs, WorkerAccountUsage, now) {
		if accountUsageStale(gs, now) {
			res := r.WorkerRefresh(WorkerAccountUsage)
			result.AccountUsageRefreshed = res.AccountUsageRefreshed
			if res.Error != "" && result.Error == "" {
				result.Error = res.Error
			}
		}
	}
	return result
}

// ForceRefresh synchronously refreshes regardless of staleness or breaker
// state (explicit user request). Still routes through the worker locking path
// so concurrent refreshes coalesce properly (no lost-update on circuit breakers).
func (r *Refresher) ForceRefresh() *RefreshResult {
	result := &RefreshResult{}
	now := time.Now()
	r.forceRefreshModels(result, now)
	if r.HealthURL != "" {
		r.forceRefreshHealth(result, now)
	}
	if r.Client != nil && r.Client.APIKey() != "" {
		r.forceRefreshAccountUsage(result, now)
	}
	return result
}

// ============================================================
// Detached coalescing (freeinference refresh --if-stale --detach, and hooks)
// ============================================================

// StaleWorkers returns the workers whose caches are stale and whose breakers
// are closed. Cheap and read-only — safe for hooks to call.
func StaleWorkers(paths state.Paths, healthURL string) []string {
	return StaleWorkersWithClient(paths, healthURL, "")
}

// StaleWorkersWithClient returns the workers whose caches are stale and whose
// breakers are closed. When apiKey is non-empty, account-usage is included.
func StaleWorkersWithClient(paths state.Paths, healthURL string, apiKey string) []string {
	now := time.Now()
	gs, _ := state.LoadGlobal(paths)
	var stale []string
	if modelsStale(gs, now) && !breakerOpen(gs, WorkerModels, now) {
		stale = append(stale, WorkerModels)
	}
	if healthURL != "" && healthStale(gs, now) && !breakerOpen(gs, WorkerHealth, now) {
		stale = append(stale, WorkerHealth)
	}
	if apiKey != "" && !breakerOpen(gs, WorkerAccountUsage, now) {
		if accountUsageStale(gs, now) {
			stale = append(stale, WorkerAccountUsage)
		}
	}
	return stale
}

// SpawnDetachedWorkers launches one detached `freeinference refresh --worker <name>`
// process per worker and returns immediately. Worker-side file locks make
// duplicate spawns harmless.
func SpawnDetachedWorkers(executable string, workers []string) error {
	if executable == "" {
		return errors.New("no executable path")
	}
	for _, worker := range workers {
		if err := spawnDetached(executable, "refresh", "--worker", worker); err != nil {
			return err
		}
	}
	return nil
}

// spawnDetached starts a fully detached child process (new session, stdio to
// /dev/null) so it outlives the short-lived hook that spawned it.
func spawnDetached(executable string, args ...string) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()

	cmd := exec.Command(executable, args...)
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn detached: %w", err)
	}
	// Do not Wait — the child is reparented and reaped by init.
	return nil
}

// ============================================================
// Internals
// ============================================================

func modelsStale(gs *schema.GlobalState, now time.Time) bool {
	if gs.Models == nil {
		return true
	}
	fetched := schema.SanitizeTimestamp(gs.Models.FetchedAt, now)
	return now.Sub(fetched) > ModelsTTL
}

func healthStale(gs *schema.GlobalState, now time.Time) bool {
	if gs.Health == nil {
		return true
	}
	fetched := schema.SanitizeTimestamp(gs.Health.FetchedAt, now)
	return now.Sub(fetched) > HealthTTL
}

func breakerOpen(gs *schema.GlobalState, endpoint string, now time.Time) bool {
	for i, cb := range gs.CircuitBreakers {
		if cb.Endpoint == endpoint && cb.State == schema.CircuitOpen {
			if cb.NextRetryAt != nil && now.Before(*cb.NextRetryAt) {
				return true
			}
			// Retry window has opened — transition to half-open.
			// The next refresh acts as a probe; success closes, failure re-opens.
			gs.CircuitBreakers[i].State = schema.CircuitHalfOpen
		}
	}
	return false
}

func (r *Refresher) refreshModels(result *RefreshResult, now time.Time) {
	models, err := r.Client.ListModels()
	if err != nil {
		r.recordFailure(WorkerModels, err, now)
		result.Error = "models fetch failed"
		return
	}

	catalog := make([]schema.CatalogModel, 0, len(models))
	for _, m := range models {
		catalog = append(catalog, schema.CatalogModel{
			ID:              m.ID,
			Name:            m.Name,
			ContextLength:   m.ContextLength,
			MaxOutputLength: m.MaxOutputLength,
			AccessState:     schema.AccessUnknown,
			Pricing:         m.Pricing,
			Features:        m.SupportedFeatures,
		})
	}

	cache := &schema.ModelsCache{
		FetchedAt: now,
		Models:    catalog,
	}
	if err := state.SaveModels(r.Paths, cache); err != nil {
		result.Error = "save models"
		return
	}
	result.ModelsRefreshed = true
	r.resetCircuitBreaker(WorkerModels)
}

func (r *Refresher) refreshHealth(result *RefreshResult, now time.Time) {
	// FI_HEALTH_URL comes straight from the environment, so treat it as
	// untrusted: enforce the canonical FreeInference-origin allowlist and
	// strip the API key from the request. Only the sanitized origin is
	// persisted to disk.
	health, err := r.Client.GetHealthFromTrusted(r.HealthURL, api.HealthURLOrigins)
	if err != nil {
		r.recordFailure(WorkerHealth, err, now)
		result.Error = "health fetch failed"
		return
	}

	// Persist only the sanitized origin so userinfo, query-string secrets, or
	// fragments in a misconfigured FI_HEALTH_URL never land on disk.
	sanitized, sanitizeErr := api.NormalizeHealthURL(r.HealthURL)
	source := ""
	if sanitizeErr == nil {
		source = sanitized.Origin
	} else {
		// NormalizeHealthURL rejected the URL — keep the field empty rather
		// than persisting a value that may carry a credential. The fetch
		// itself would have failed above in that case, so this branch is
		// defensive only.
		source = ""
	}

	cache := &schema.HealthCache{
		FetchedAt:      now,
		Status:         health.Status,
		HealthyCount:   &health.Healthy,
		UnhealthyCount: &health.Unhealthy,
		Source:         source,
	}
	if err := state.SaveHealth(r.Paths, cache); err != nil {
		result.Error = "save health"
		return
	}
	result.HealthRefreshed = true
	r.resetCircuitBreaker(WorkerHealth)
}

// recordFailure opens the circuit breaker with escalating backoff.
// A server-supplied Retry-After overrides the computed backoff.
//
// This uses UpdateCircuitBreaker so concurrent models/health workers cannot
// lose each other's breaker updates (lost-update race).
func (r *Refresher) recordFailure(endpoint string, cause error, now time.Time) {
	interval := time.Duration(0)
	var he *api.HTTPError
	if errors.As(cause, &he) && he.RetryAfter > 0 {
		interval = he.RetryAfter
	}

	err := state.UpdateCircuitBreakers(r.Paths, func(cbs []schema.CircuitBreaker) ([]schema.CircuitBreaker, error) {
		cb := findOrCreateCB(&cbs, endpoint)
		cb.FailureCount++
		cb.LastFailureAt = &now
		cb.State = schema.CircuitOpen

		if interval == 0 {
			idx := cb.FailureCount - 1
			if idx >= len(backoffIntervals) {
				idx = len(backoffIntervals) - 1
			}
			if idx < 0 {
				idx = 0
			}
			interval = backoffIntervals[idx]
		}

		nextRetry := now.Add(interval)
		cb.NextRetryAt = &nextRetry
		return cbs, nil
	})
	if err != nil && !state.IsLockBusy(err) {
		// Lock contention is fail-open; only log unexpected errors.
		// (In production this would use structured logging.)
		_ = err
	}
}

func (r *Refresher) resetCircuitBreaker(endpoint string) {
	err := state.UpdateCircuitBreakers(r.Paths, func(cbs []schema.CircuitBreaker) ([]schema.CircuitBreaker, error) {
		for i := range cbs {
			if cbs[i].Endpoint == endpoint {
				cbs[i].State = schema.CircuitClosed
				cbs[i].FailureCount = 0
				cbs[i].LastFailureAt = nil
				cbs[i].NextRetryAt = nil
			}
		}
		return cbs, nil
	})
	if err != nil && !state.IsLockBusy(err) {
		_ = err
	}
}

func findOrCreateCB(cbs *[]schema.CircuitBreaker, endpoint string) *schema.CircuitBreaker {
	for i := range *cbs {
		if (*cbs)[i].Endpoint == endpoint {
			return &(*cbs)[i]
		}
	}
	*cbs = append(*cbs, schema.CircuitBreaker{
		Endpoint: endpoint,
		State:    schema.CircuitClosed,
	})
	return &(*cbs)[len(*cbs)-1]
}

// forceRefreshModels refreshes models under the worker lock but without the
// staleness / circuit-breaker gates. Used by ForceRefresh.
func (r *Refresher) forceRefreshModels(result *RefreshResult, now time.Time) {
	fl := state.NewFileLock(r.Paths.RefreshLock(WorkerModels))
	if err := fl.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			result.Skipped = true
			result.SkipReason = "another worker running"
		} else {
			result.Error = "acquire lock"
		}
		return
	}
	defer fl.Release()
	r.refreshModels(result, now)
}

// forceRefreshHealth refreshes health under the worker lock but without the
// staleness / circuit-breaker gates. Used by ForceRefresh.
func (r *Refresher) forceRefreshHealth(result *RefreshResult, now time.Time) {
	fl := state.NewFileLock(r.Paths.RefreshLock(WorkerHealth))
	if err := fl.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			result.Skipped = true
			result.SkipReason = "another worker running"
		} else {
			result.Error = "acquire lock"
		}
		return
	}
	defer fl.Release()
	r.refreshHealth(result, now)
}

// ============================================================
// Account usage
// ============================================================

func accountUsageStale(gs *schema.GlobalState, now time.Time) bool {
	if gs.AccountUsage == nil {
		return true
	}
	fetched := schema.SanitizeTimestamp(gs.AccountUsage.FetchedAt, now)
	return now.Sub(fetched) > AccountUSageTTL
}

func (r *Refresher) refreshAccountUsage(result *RefreshResult, now time.Time) {
	usage, err := r.Client.GetAccountUsage()
	if err != nil {
		r.recordFailure(WorkerAccountUsage, err, now)
		result.Error = "account usage fetch failed"
		return
	}
	if err := state.SaveAccountUsage(r.Paths, usage); err != nil {
		result.Error = "save account usage"
		return
	}
	result.AccountUsageRefreshed = true
	r.resetCircuitBreaker(WorkerAccountUsage)
}

// forceRefreshAccountUsage refreshes account usage under the worker lock but
// without the staleness / circuit-breaker gates. Used by ForceRefresh.
func (r *Refresher) forceRefreshAccountUsage(result *RefreshResult, now time.Time) {
	fl := state.NewFileLock(r.Paths.RefreshLock(WorkerAccountUsage))
	if err := fl.Acquire(); err != nil {
		if state.IsLockBusy(err) {
			result.Skipped = true
			result.SkipReason = "another worker running"
		} else {
			result.Error = "acquire lock"
		}
		return
	}
	defer fl.Release()
	r.refreshAccountUsage(result, now)
}
