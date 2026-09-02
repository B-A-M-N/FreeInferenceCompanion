package background

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/failures"
	"github.com/b-a-m-n/freeinference-companion/internal/secure"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// Worker names / circuit breaker endpoint IDs.
const (
	WorkerModels       = "models"
	WorkerHealth       = "health"
	WorkerAccountUsage = "account-usage"
	WorkerPublicStatus = "public-status"
)

// Cache TTLs.
const (
	ModelsTTL       = 6 * time.Hour
	HealthTTL       = 120 * time.Second
	AccountUSageTTL = 60 * time.Minute
	PublicStatusTTL = 20 * time.Minute

	// AutomaticRefreshMinInterval is shared by all authenticated metadata
	// workers. It prevents several stale workers spawned by one session from
	// creating a burst against the provider API.
	AutomaticRefreshMinInterval = time.Minute
	// AutomaticRateLimitCooldown is the conservative fallback when a provider
	// returns 429 without a usable Retry-After value.
	AutomaticRateLimitCooldown = 15 * time.Minute
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
	Worker                 string `json:"worker,omitempty"`
	ModelsRefreshed        bool   `json:"models_refreshed"`
	HealthRefreshed        bool   `json:"health_refreshed"`
	AccountUsageRefreshed  bool   `json:"account_usage_refreshed"`
	PublicStatusRefreshed  bool   `json:"public_status_refreshed"`
	AccountUsageCapability string `json:"account_usage_capability,omitempty"`
	Skipped                bool   `json:"skipped"`
	SkipReason             string `json:"skip_reason,omitempty"`
	Error                  string `json:"error,omitempty"`
}

// Refresher performs provider metadata refreshes. Cross-process coalescing
// is done with non-blocking file locks; there is no in-process state.
type Refresher struct {
	Client    *api.Client
	Paths     state.Paths
	HealthURL string
	// PublicStatusFetch is injectable for tests. Production callers leave it
	// nil, which uses the unauthenticated public monitor endpoint.
	PublicStatusFetch func(context.Context) (*api.PublicStatusResponse, error)
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

	if worker != WorkerModels && worker != WorkerHealth && worker != WorkerAccountUsage && worker != WorkerPublicStatus {
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
		if !reserveAutomaticRefresh(r.Paths, now) {
			result.Skipped = true
			result.SkipReason = "automatic refresh cooldown"
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
		if !reserveAutomaticRefresh(r.Paths, now) {
			result.Skipped = true
			result.SkipReason = "automatic refresh cooldown"
			return result
		}
		r.refreshHealth(result, now)
	case WorkerAccountUsage:
		if r.Client == nil || r.Client.APIKey() == "" {
			result.Skipped = true
			result.SkipReason = "no API key configured"
			return result
		}
		if !accountUsageCapabilityRefreshable(gs) {
			result.Skipped = true
			result.SkipReason = "account usage capability unavailable"
			result.AccountUsageCapability = string(gs.AccountUsageCapability.State)
			return result
		}
		if !accountUsageStale(gs, now) {
			result.Skipped = true
			result.SkipReason = "cache fresh"
			return result
		}
		if !reserveAutomaticRefresh(r.Paths, now) {
			result.Skipped = true
			result.SkipReason = "automatic refresh cooldown"
			return result
		}
		r.refreshAccountUsage(result, now)
	case WorkerPublicStatus:
		if !publicStatusStale(gs, now) {
			result.Skipped = true
			result.SkipReason = "cache fresh"
			return result
		}
		r.refreshPublicStatus(result, now)
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
	if r.Client != nil && r.Client.APIKey() != "" && accountUsageCapabilityRefreshable(gs) &&
		!breakerOpen(gs, WorkerAccountUsage, now) {
		if accountUsageStale(gs, now) {
			res := r.WorkerRefresh(WorkerAccountUsage)
			result.AccountUsageRefreshed = res.AccountUsageRefreshed
			result.AccountUsageCapability = res.AccountUsageCapability
			if res.Error != "" && result.Error == "" {
				result.Error = res.Error
			}
		}
	}
	if publicStatusStale(gs, now) && !breakerOpen(gs, WorkerPublicStatus, now) {
		res := r.WorkerRefresh(WorkerPublicStatus)
		result.PublicStatusRefreshed = res.PublicStatusRefreshed
		if res.Error != "" && result.Error == "" {
			result.Error = res.Error
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
	gs, _ := state.LoadGlobal(r.Paths)
	r.forceRefreshModels(result, now)
	if r.HealthURL != "" {
		r.forceRefreshHealth(result, now)
	}
	if r.Client != nil && r.Client.APIKey() != "" && accountUsageCapabilityRefreshable(gs) {
		r.forceRefreshAccountUsage(result, now)
	}
	r.forceRefreshPublicStatus(result, now)
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
	if apiKey != "" && accountUsageCapabilityRefreshable(gs) && !breakerOpen(gs, WorkerAccountUsage, now) {
		if accountUsageStale(gs, now) {
			stale = append(stale, WorkerAccountUsage)
		}
	}
	if publicStatusStale(gs, now) && !breakerOpen(gs, WorkerPublicStatus, now) {
		stale = append(stale, WorkerPublicStatus)
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
	if gs == nil || gs.Models == nil {
		return true
	}
	fetched := schema.SanitizeTimestamp(gs.Models.FetchedAt, now)
	return now.Sub(fetched) > ModelsTTL
}

func healthStale(gs *schema.GlobalState, now time.Time) bool {
	if gs == nil || gs.Health == nil {
		return true
	}
	fetched := schema.SanitizeTimestamp(gs.Health.FetchedAt, now)
	return now.Sub(fetched) > HealthTTL
}

func publicStatusStale(gs *schema.GlobalState, now time.Time) bool {
	if gs == nil || gs.PublicStatus == nil || gs.PublicStatus.FetchedAt.IsZero() {
		return true
	}
	fetched := schema.SanitizeTimestamp(gs.PublicStatus.FetchedAt, now)
	if now.Sub(fetched) > PublicStatusTTL {
		return true
	}
	if !gs.PublicStatus.CheckedAt.IsZero() && now.Sub(schema.SanitizeTimestamp(gs.PublicStatus.CheckedAt, now)) > api.PublicStatusStaleAfter {
		return true
	}
	return false
}

func reserveAutomaticRefresh(paths state.Paths, now time.Time) bool {
	allowed, err := state.ReserveRefreshSlot(paths, now, AutomaticRefreshMinInterval)
	return err == nil && allowed
}

func breakerOpen(gs *schema.GlobalState, endpoint string, now time.Time) bool {
	if gs == nil {
		return false
	}
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

func (r *Refresher) refreshPublicStatus(result *RefreshResult, now time.Time) {
	fetch := r.PublicStatusFetch
	if fetch == nil {
		fetch = api.FetchPublicStatus
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	status, err := fetch(ctx)
	if err != nil {
		r.recordFailure(WorkerPublicStatus, err, now)
		r.recordPublicStatusFailure(now, failures.Normalize(err.Error()).Category)
		result.Error = "public status fetch failed"
		return
	}
	cache, err := publicStatusCache(status, now)
	if err != nil {
		r.recordFailure(WorkerPublicStatus, err, now)
		r.recordPublicStatusFailure(now, failures.Normalize(err.Error()).Category)
		result.Error = "public status validation failed"
		return
	}
	if err := state.SavePublicStatus(r.Paths, cache); err != nil {
		result.Error = "save public status"
		return
	}
	result.PublicStatusRefreshed = true
	r.resetCircuitBreaker(WorkerPublicStatus)
}

func publicStatusCache(status *api.PublicStatusResponse, now time.Time) (*schema.PublicStatusCache, error) {
	if status == nil {
		return nil, errors.New("public status response is nil")
	}
	if err := status.Validate(); err != nil {
		return nil, err
	}
	if status.Cycle.ValidationError != "" {
		return nil, errors.New("public status cycle is invalid")
	}
	checkedAt, err := time.Parse(time.RFC3339Nano, status.Cycle.CheckedAt)
	if err != nil {
		return nil, errors.New("public status cycle timestamp is invalid")
	}
	cache := &schema.PublicStatusCache{
		FetchedAt:  now.UTC(),
		CheckedAt:  checkedAt.UTC(),
		Source:     api.PublicStatusSource,
		Total:      status.Total,
		Healthy:    status.Healthy,
		Unhealthy:  status.Unhealthy,
		CycleOK:    status.Cycle.OK,
		CycleError: publicStatusText(status.Cycle.Error),
	}
	for i, model := range status.Models {
		if i >= schema.MaxPublicStatusModels {
			break
		}
		if model.ValidationError != "" {
			continue
		}
		entry := schema.PublicStatusModelCache{
			ModelID:     secure.SanitizeField(model.ModelID),
			UptimeRatio: model.UptimeRatio,
		}
		if model.Latest != nil {
			latest, ok := cachedPublicStatusSample(*model.Latest)
			if !ok {
				continue
			}
			entry.Latest = &latest
		}
		entry.History = cachedPublicStatusHistory(model, entry.Latest)
		if entry.Latest == nil && len(entry.History) == 0 {
			continue
		}
		cache.Models = append(cache.Models, entry)
	}
	if len(cache.Models) == 0 {
		return nil, errors.New("public status contains no valid model metrics")
	}
	return cache, nil
}

// cachedPublicStatusSample copies one already-validated public sample into
// the durable schema and normalizes its timestamp. Pointers are copied so a
// caller cannot mutate the cache through the response object after refresh.
func cachedPublicStatusSample(sample api.PublicStatusSample) (schema.PublicStatusSampleCache, bool) {
	checkedAt, err := time.Parse(time.RFC3339Nano, sample.CheckedAt)
	if err != nil {
		return schema.PublicStatusSampleCache{}, false
	}
	return schema.PublicStatusSampleCache{
		OK:               copyBool(sample.OK),
		CheckedAt:        checkedAt.UTC(),
		LatencyMs:        copyInt64(sample.LatencyMs),
		TTFTMs:           copyInt64(sample.TTFTMs),
		CompletionTokens: copyInt64(sample.CompletionTokens),
		ThroughputTps:    copyFloat64(sample.ThroughputTps),
		Error:            publicStatusText(sample.Error),
	}, true
}

// cachedPublicStatusHistory retains the newest distinct synthetic samples
// from both history and spark. The endpoint may expose either collection, and
// deduplication prevents the same check from consuming the whole bound twice.
func cachedPublicStatusHistory(model api.PublicStatusModel, latest *schema.PublicStatusSampleCache) []schema.PublicStatusSampleCache {
	type candidate struct {
		sample api.PublicStatusSample
		at     time.Time
	}
	candidates := make([]candidate, 0, len(model.History)+len(model.Spark))
	for _, sample := range append(append([]api.PublicStatusSample{}, model.History...), model.Spark...) {
		cached, ok := cachedPublicStatusSample(sample)
		if !ok {
			continue
		}
		if latest != nil && cached.CheckedAt.Equal(latest.CheckedAt) {
			continue
		}
		candidates = append(candidates, candidate{sample: sample, at: cached.CheckedAt})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].at.After(candidates[j].at) })
	result := make([]schema.PublicStatusSampleCache, 0, minInt(len(candidates), schema.MaxPublicStatusSamplesPerModel))
	seen := make(map[int64]struct{}, len(candidates))
	for _, candidate := range candidates {
		key := candidate.at.UnixNano()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		cached, ok := cachedPublicStatusSample(candidate.sample)
		if !ok {
			continue
		}
		result = append(result, cached)
		if len(result) == schema.MaxPublicStatusSamplesPerModel {
			break
		}
	}
	return result
}

func copyBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func publicStatusText(value string) string {
	value = secure.Redact(secure.SanitizeField(value))
	if len(value) > 200 {
		return value[:200] + "..."
	}
	return value
}

func (r *Refresher) recordPublicStatusFailure(now time.Time, category string) {
	gs, _ := state.LoadGlobal(r.Paths)
	if gs == nil {
		gs = &schema.GlobalState{}
	}
	cache := gs.PublicStatus
	if cache == nil {
		cache = &schema.PublicStatusCache{Source: api.PublicStatusSource}
	}
	cache.ConsecutiveFailure++
	cache.LastError = publicStatusText(category)
	for _, cb := range gs.CircuitBreakers {
		if cb.Endpoint == WorkerPublicStatus {
			cache.NextRetryAt = cb.NextRetryAt
			break
		}
	}
	if cache.NextRetryAt == nil {
		index := cache.ConsecutiveFailure - 1
		if index < 0 {
			index = 0
		}
		if index >= len(backoffIntervals) {
			index = len(backoffIntervals) - 1
		}
		next := now.Add(backoffIntervals[index])
		cache.NextRetryAt = &next
	}
	_ = state.SavePublicStatus(r.Paths, cache)
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
	if isRateLimitFailure(cause) {
		cooldown := AutomaticRateLimitCooldown
		if errors.As(cause, &he) && he.RetryAfter > cooldown {
			cooldown = he.RetryAfter
		}
		_ = state.ExtendRefreshCooldown(r.Paths, now.Add(cooldown))
	}
}

func isRateLimitFailure(cause error) bool {
	if cause == nil {
		return false
	}
	var he *api.HTTPError
	if errors.As(cause, &he) {
		return he.StatusCode == 429
	}
	return failures.Normalize(cause.Error()).Category == failures.RateLimit
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
	if gs == nil || gs.AccountUsage == nil {
		return true
	}
	fetched := schema.SanitizeTimestamp(gs.AccountUsage.FetchedAt, now)
	return now.Sub(fetched) > AccountUSageTTL
}

func accountUsageCapabilityRefreshable(gs *schema.GlobalState) bool {
	if gs == nil || gs.AccountUsageCapability == nil {
		return true
	}
	return gs.AccountUsageCapability.State != schema.CapabilityUnsupported &&
		gs.AccountUsageCapability.State != schema.CapabilityForbidden
}

func (r *Refresher) refreshAccountUsage(result *RefreshResult, now time.Time) {
	usage, capability, err := r.Client.GetAccountUsage()
	result.AccountUsageCapability = string(capability)
	capabilityRecord := &schema.AccountUsageCapability{State: capability, CheckedAt: now.UTC()}
	if err := state.SaveAccountUsageCapability(r.Paths, capabilityRecord); err != nil {
		result.Error = "save account usage capability"
		return
	}
	if err != nil {
		r.recordFailure(WorkerAccountUsage, err, now)
		result.Error = "account usage fetch failed"
		return
	}
	if capability != schema.CapabilitySupported || usage == nil {
		r.resetCircuitBreaker(WorkerAccountUsage)
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

func (r *Refresher) forceRefreshPublicStatus(result *RefreshResult, now time.Time) {
	fl := state.NewFileLock(r.Paths.RefreshLock(WorkerPublicStatus))
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
	r.refreshPublicStatus(result, now)
}
