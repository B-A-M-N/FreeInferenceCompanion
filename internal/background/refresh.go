package background

import (
	"math/rand"
	"sync"
	"time"

	"github.com/bamn/freeinference-companion/internal/api"
	"github.com/bamn/freeinference-companion/internal/state"
	"github.com/bamn/freeinference-companion/pkg/schema"
)

// Default backoff intervals
var backoffIntervals = []time.Duration{
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
}

const maxBackoffInterval = 30 * time.Minute
const jitterPercent = 0.15 // ±15% jitter

// Refresher manages opportunistic refreshing of provider metadata.
type Refresher struct {
	Client    *api.Client
	Paths     state.Paths
	HealthURL string

	mu       sync.Mutex
	refreshing map[string]bool
}

// NewRefresher creates a new Refresher.
func NewRefresher(client *api.Client, paths state.Paths, healthURL string) *Refresher {
	return &Refresher{
		Client:     client,
		Paths:      paths,
		HealthURL:  healthURL,
		refreshing: make(map[string]bool),
	}
}

// RefreshResult summarizes what happened during a refresh.
type RefreshResult struct {
	ModelsRefreshed      bool   `json:"models_refreshed"`
	HealthRefreshed      bool   `json:"health_refreshed"`
	CircuitBreakerOpened bool   `json:"circuit_breaker_opened"`
	Error                string `json:"error,omitempty"`
}

// RefreshIfStale checks if global caches are stale and refreshes them.
// Only refreshes if no other refresh is currently running for that resource.
func (r *Refresher) RefreshIfStale() *RefreshResult {
	result := &RefreshResult{}

	gs, err := state.LoadGlobal(r.Paths)
	if err != nil {
		result.Error = "load global state"
		return result
	}

	now := time.Now()

	// Check models cache (TTL: 6 hours)
	if r.tryStartRefresh("models") {
		defer r.endRefresh("models")
		if shouldRefresh(gs.Models, 6*time.Hour, now) {
			r.refreshModels(result, now)
		}
	}

	// Check health cache (TTL: 120 seconds)
	if r.HealthURL != "" && r.tryStartRefresh("health") {
		defer r.endRefresh("health")
		if shouldRefresh(gs.Health, 120*time.Second, now) {
			r.refreshHealth(result, now, gs)
		}
	}

	return result
}

// ForceRefresh bypasses staleness checks.
func (r *Refresher) ForceRefresh() *RefreshResult {
	result := &RefreshResult{}
	now := time.Now()

	if r.tryStartRefresh("models") {
		defer r.endRefresh("models")
		r.refreshModels(result, now)
	}
	if r.HealthURL != "" && r.tryStartRefresh("health") {
		defer r.endRefresh("health")
		gs, _ := state.LoadGlobal(r.Paths)
		r.refreshHealth(result, now, gs)
	}
	return result
}

func (r *Refresher) refreshModels(result *RefreshResult, now time.Time) {
	models, err := r.Client.ListModels()
	if err != nil {
		result.Error = "models fetch failed"
		return
	}

	catalog := make([]schema.CatalogModel, 0, len(models))
	for _, m := range models {
		access := schema.AccessUnknown
		if m.OwnedBy == "internal" || m.ID == "glm-5.2" {
			access = schema.AccessRestricted
		}

		catalog = append(catalog, schema.CatalogModel{
			ID:              m.ID,
			Name:            m.Name,
			ContextLength:   m.ContextLength,
			MaxOutputLength: m.MaxOutputLength,
			AccessState:     access,
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
}

func (r *Refresher) refreshHealth(result *RefreshResult, now time.Time, gs *schema.GlobalState) {
	// Check circuit breaker
	if gs != nil {
		for _, cb := range gs.CircuitBreakers {
			if cb.Endpoint == "health" && cb.State == schema.CircuitOpen {
				if cb.NextRetryAt != nil && now.Before(*cb.NextRetryAt) {
					return // circuit open, skip
				}
			}
		}
	}

	health, err := r.Client.GetHealth(r.HealthURL)
	if err != nil {
		r.recordFailure("health", now)
		result.Error = "health fetch failed"
		return
	}

	cache := &schema.HealthCache{
		FetchedAt:      now,
		Status:         health.Status,
		HealthyCount:   &health.Healthy,
		UnhealthyCount: &health.Unhealthy,
		Source:         r.HealthURL,
	}
	if err := state.SaveHealth(r.Paths, cache); err != nil {
		result.Error = "save health"
		return
	}
	result.HealthRefreshed = true

	// Reset circuit breaker on success
	r.resetCircuitBreaker("health")
}

func (r *Refresher) recordFailure(endpoint string, now time.Time) {
	gs, err := state.LoadGlobal(r.Paths)
	if err != nil {
		return
	}

	cb := findOrCreateCB(&gs.CircuitBreakers, endpoint)
	cb.FailureCount++
	cb.LastFailureAt = &now
	cb.State = schema.CircuitOpen

	// Calculate backoff
	idx := cb.FailureCount - 1
	if idx >= len(backoffIntervals) {
		idx = len(backoffIntervals) - 1
	}
	interval := backoffIntervals[idx]
	interval = addJitter(interval)

	nextRetry := now.Add(interval)
	cb.NextRetryAt = &nextRetry

	state.SaveCircuitBreakers(r.Paths, gs.CircuitBreakers)
}

func (r *Refresher) resetCircuitBreaker(endpoint string) {
	gs, err := state.LoadGlobal(r.Paths)
	if err != nil {
		return
	}

	for i := range gs.CircuitBreakers {
		if gs.CircuitBreakers[i].Endpoint == endpoint {
			gs.CircuitBreakers[i].State = schema.CircuitClosed
			gs.CircuitBreakers[i].FailureCount = 0
			gs.CircuitBreakers[i].LastFailureAt = nil
			gs.CircuitBreakers[i].NextRetryAt = nil
		}
	}
	state.SaveCircuitBreakers(r.Paths, gs.CircuitBreakers)
}

func (r *Refresher) tryStartRefresh(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refreshing[name] {
		return false
	}
	r.refreshing[name] = true
	return true
}

func (r *Refresher) endRefresh(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.refreshing, name)
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

func shouldRefresh(cache interface{}, ttl time.Duration, now time.Time) bool {
	switch v := cache.(type) {
	case *schema.ModelsCache:
		return v == nil || now.Sub(v.FetchedAt) > ttl
	case *schema.HealthCache:
		return v == nil || now.Sub(v.FetchedAt) > ttl
	default:
		return true
	}
}

func addJitter(d time.Duration) time.Duration {
	if jitterPercent <= 0 {
		return d
	}
	jitter := time.Duration(float64(d) * jitterPercent)
	offset := time.Duration(rand.Int63n(int64(jitter*2))) - jitter
	return d + offset
}