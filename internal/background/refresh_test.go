package background

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/api"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func modelsServer(t *testing.T, calls *atomic.Int32, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		handler(w, r)
	}))
}

func writeModelsJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(api.ModelsResponse{
		Object: "list",
		Data:   []api.Model{{ID: "glm-5.1", ContextLength: 200000, MaxOutputLength: 8192}},
	})
}

func testRefresher(t *testing.T, server *httptest.Server, healthURL string) *Refresher {
	t.Helper()
	paths := state.NewPathsWithDir(t.TempDir())
	client := api.NewClientForTest(server.URL, "", 10*time.Second)
	return NewRefresher(client, paths, healthURL)
}

func TestStaleCacheRefreshesOnce(t *testing.T) {
	var calls atomic.Int32
	server := modelsServer(t, &calls, func(w http.ResponseWriter, r *http.Request) { writeModelsJSON(w) })
	defer server.Close()

	r := testRefresher(t, server, "")
	res := r.WorkerRefresh(WorkerModels)
	if !res.ModelsRefreshed {
		t.Fatalf("expected refresh, got %+v", res)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d", calls.Load())
	}

	// Second run: cache is fresh → skipped, no new request.
	res = r.WorkerRefresh(WorkerModels)
	if !res.Skipped || res.SkipReason != "cache fresh" {
		t.Errorf("expected fresh-cache skip, got %+v", res)
	}
	if calls.Load() != 1 {
		t.Errorf("fresh cache must not trigger a request: %d calls", calls.Load())
	}
}

func TestConcurrentWorkersCoalesce(t *testing.T) {
	var calls atomic.Int32
	server := modelsServer(t, &calls, func(w http.ResponseWriter, r *http.Request) { writeModelsJSON(w) })
	defer server.Close()

	r := testRefresher(t, server, "")

	// Hold the worker lock — a second worker must skip immediately.
	if err := os.MkdirAll(r.Paths.GlobalDir(), 0700); err != nil {
		t.Fatal(err)
	}
	fl := state.NewFileLock(r.Paths.RefreshLock(WorkerModels))
	if err := fl.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer fl.Release()

	start := time.Now()
	res := r.WorkerRefresh(WorkerModels)
	if !res.Skipped || res.SkipReason != "another worker running" {
		t.Errorf("expected coalesced skip, got %+v", res)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("coalesced worker must return immediately")
	}
	if calls.Load() != 0 {
		t.Errorf("locked worker must not request: %d calls", calls.Load())
	}
}

func TestRateLimitOpensBreakerAndHonorsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := modelsServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit","message":"slow down","code":429}}`))
	})
	defer server.Close()

	r := testRefresher(t, server, "")
	res := r.WorkerRefresh(WorkerModels)
	if res.Error == "" {
		t.Fatal("expected error on 429")
	}

	gs, err := state.LoadGlobal(r.Paths)
	if err != nil {
		t.Fatal(err)
	}
	var cb *schema.CircuitBreaker
	for i := range gs.CircuitBreakers {
		if gs.CircuitBreakers[i].Endpoint == WorkerModels {
			cb = &gs.CircuitBreakers[i]
		}
	}
	if cb == nil || cb.State != schema.CircuitOpen {
		t.Fatalf("breaker should be open: %+v", cb)
	}
	if cb.NextRetryAt == nil {
		t.Fatal("NextRetryAt must be set from Retry-After")
	}
	until := time.Until(*cb.NextRetryAt)
	if until < 100*time.Second || until > 130*time.Second {
		t.Errorf("Retry-After 120 not honored: retry in %v", until)
	}

	// Breaker open → next worker skips without a request.
	res = r.WorkerRefresh(WorkerModels)
	if !res.Skipped || res.SkipReason != "circuit breaker open" {
		t.Errorf("expected breaker skip, got %+v", res)
	}
	if calls.Load() != 1 {
		t.Errorf("open breaker must block requests: %d calls", calls.Load())
	}
}

func TestServerErrorBackoffEscalates(t *testing.T) {
	server := modelsServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer server.Close()

	r := testRefresher(t, server, "")
	for range 2 {
		// Simulate breaker expiry between attempts.
		expireBreaker(t, r.Paths, WorkerModels)
		r.WorkerRefresh(WorkerModels)
	}

	gs, _ := state.LoadGlobal(r.Paths)
	var cb *schema.CircuitBreaker
	for i := range gs.CircuitBreakers {
		if gs.CircuitBreakers[i].Endpoint == WorkerModels {
			cb = &gs.CircuitBreakers[i]
		}
	}
	if cb == nil || cb.FailureCount != 2 {
		t.Fatalf("failure count = %+v", cb)
	}
	// Second failure → 5-minute backoff.
	until := time.Until(*cb.NextRetryAt)
	if until < 4*time.Minute || until > 6*time.Minute {
		t.Errorf("second failure should back off ~5m, got %v", until)
	}
}

func TestSuccessfulRetryClosesBreaker(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	server := modelsServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeModelsJSON(w)
	})
	defer server.Close()

	r := testRefresher(t, server, "")
	r.WorkerRefresh(WorkerModels) // opens breaker

	expireBreaker(t, r.Paths, WorkerModels)
	fail.Store(false)
	res := r.WorkerRefresh(WorkerModels)
	if !res.ModelsRefreshed {
		t.Fatalf("expected recovery refresh, got %+v", res)
	}

	gs, _ := state.LoadGlobal(r.Paths)
	for _, cb := range gs.CircuitBreakers {
		if cb.Endpoint == WorkerModels {
			if cb.State != schema.CircuitClosed || cb.FailureCount != 0 {
				t.Errorf("breaker should be closed after success: %+v", cb)
			}
		}
	}
}

func TestHealthWorkerRespectsMissingURL(t *testing.T) {
	server := modelsServer(t, nil, func(w http.ResponseWriter, r *http.Request) { writeModelsJSON(w) })
	defer server.Close()

	r := testRefresher(t, server, "")
	res := r.WorkerRefresh(WorkerHealth)
	if !res.Skipped || res.SkipReason != "no health source configured" {
		t.Errorf("expected no-source skip, got %+v", res)
	}
}

func TestPublicStatusWorkerUsesUnauthenticatedBoundedCache(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	r := NewRefresher(nil, paths, "")
	now := time.Now().UTC().Truncate(time.Second)
	ok := true
	latency := int64(412)
	uptime := 0.998
	r.PublicStatusFetch = func(context.Context) (*api.PublicStatusResponse, error) {
		return &api.PublicStatusResponse{
			Total: 1, Healthy: 1,
			Cycle: api.PublicStatusCycle{OK: &ok, CheckedAt: now.Format(time.RFC3339)},
			Models: []api.PublicStatusModel{{
				ModelID: "glm-5.1", UptimeRatio: &uptime,
				Latest: &api.PublicStatusSample{OK: &ok, CheckedAt: now.Format(time.RFC3339), LatencyMs: &latency},
			}},
		}, nil
	}
	res := r.WorkerRefresh(WorkerPublicStatus)
	if !res.PublicStatusRefreshed || res.Error != "" {
		t.Fatalf("public status refresh = %+v", res)
	}
	gs, err := state.LoadGlobal(r.Paths)
	if err != nil || gs.PublicStatus == nil || len(gs.PublicStatus.Models) != 1 {
		t.Fatalf("public status cache = %#v, err=%v", gs.PublicStatus, err)
	}
	if gs.PublicStatus.Models[0].Latest == nil || gs.PublicStatus.Models[0].Latest.LatencyMs == nil || *gs.PublicStatus.Models[0].Latest.LatencyMs != latency {
		t.Fatalf("cached model metrics = %#v", gs.PublicStatus.Models[0])
	}
}

func TestPublicStatusCacheRetainsBoundedHistoryForCorrelation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ok := true
	history := make([]api.PublicStatusSample, 0, schema.MaxPublicStatusSamplesPerModel+10)
	for i := 0; i < schema.MaxPublicStatusSamplesPerModel+10; i++ {
		checked := now.Add(-time.Duration(i+1) * 20 * time.Minute)
		history = append(history, api.PublicStatusSample{OK: &ok, CheckedAt: checked.Format(time.RFC3339)})
	}
	latest := api.PublicStatusSample{OK: &ok, CheckedAt: now.Format(time.RFC3339)}
	cache, err := publicStatusCache(&api.PublicStatusResponse{
		Total: 1, Healthy: 1,
		Cycle:  api.PublicStatusCycle{OK: &ok, CheckedAt: now.Format(time.RFC3339)},
		Models: []api.PublicStatusModel{{ModelID: "glm-5.2", Latest: &latest, History: history}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	model := cache.Models[0]
	if len(model.History) != schema.MaxPublicStatusSamplesPerModel {
		t.Fatalf("history length=%d, want %d", len(model.History), schema.MaxPublicStatusSamplesPerModel)
	}
	if !model.History[0].CheckedAt.After(model.History[len(model.History)-1].CheckedAt) {
		t.Fatalf("history is not newest-first: first=%s last=%s", model.History[0].CheckedAt, model.History[len(model.History)-1].CheckedAt)
	}
	if model.History[0].CheckedAt.Equal(model.Latest.CheckedAt) {
		t.Fatal("latest sample was duplicated into history")
	}
}

func TestStaleWorkers(t *testing.T) {
	server := modelsServer(t, nil, func(w http.ResponseWriter, r *http.Request) { writeModelsJSON(w) })
	defer server.Close()

	r := testRefresher(t, server, "")
	stale := StaleWorkers(r.Paths, "")
	if len(stale) != 2 || stale[0] != WorkerModels || stale[1] != WorkerPublicStatus {
		t.Errorf("stale = %v", stale)
	}

	r.WorkerRefresh(WorkerModels)
	stale = StaleWorkers(r.Paths, "")
	if len(stale) != 1 || stale[0] != WorkerPublicStatus {
		t.Errorf("after model refresh, stale = %v", stale)
	}
}

// expireBreaker forces the named breaker's NextRetryAt into the past.
func expireBreaker(t *testing.T, paths state.Paths, endpoint string) {
	t.Helper()
	gs, err := state.LoadGlobal(paths)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	for i := range gs.CircuitBreakers {
		if gs.CircuitBreakers[i].Endpoint == endpoint {
			gs.CircuitBreakers[i].NextRetryAt = &past
		}
	}
	if err := state.SaveCircuitBreakers(paths, gs.CircuitBreakers); err != nil {
		t.Fatal(err)
	}
}

// TestTimeoutBackoffOpensBreaker verifies a slow server (exceeding the client
// timeout) opens the circuit breaker just like a 5xx. Uses a client with a
// 200ms timeout against a server that never responds, so the test stays fast.
func TestTimeoutBackoffOpensBreaker(t *testing.T) {
	server := modelsServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		// Hold the connection open past the client timeout.
		time.Sleep(2 * time.Second)
	})
	defer server.Close()

	paths := state.NewPathsWithDir(t.TempDir())
	client := api.NewClientForTest(server.URL, "", 200*time.Millisecond)
	r := NewRefresher(client, paths, "")
	res := r.WorkerRefresh(WorkerModels)
	if res.Error == "" {
		t.Fatal("timeout must surface as error")
	}

	gs, _ := state.LoadGlobal(paths)
	for _, cb := range gs.CircuitBreakers {
		if cb.Endpoint == WorkerModels {
			if cb.State != schema.CircuitOpen {
				t.Errorf("timeout should open the breaker, got %s", cb.State)
			}
			return
		}
	}
	t.Fatal("no models circuit breaker recorded after timeout")
}

// TestBodySizeLimitEnforced verifies an oversized models response is rejected
// rather than being parsed into the cache.
func TestBodySizeLimitEnforced(t *testing.T) {
	server := modelsServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Send more than MaxCatalogBody (2 MiB).
		w.WriteHeader(http.StatusOK)
		huge := make([]byte, (api.MaxCatalogBody)+64)
		for i := range huge {
			huge[i] = 'a'
		}
		w.Write(huge)
	})
	defer server.Close()

	r := testRefresher(t, server, "")
	res := r.WorkerRefresh(WorkerModels)
	if res.Error == "" {
		t.Fatal("oversized response must error, not parse")
	}
	if res.ModelsRefreshed {
		t.Error("oversized response must not refresh the cache")
	}
}

// TestNoInferenceEndpointDuringMonitoring verifies the refresher only ever
// hits /models (or the configured health URL), never /chat/completions or any
// other inference endpoint. This is the contract that says "monitoring never
// consumes an inference slot."
func TestNoInferenceEndpointDuringMonitoring(t *testing.T) {
	var inferenceHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeModelsJSON(w)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		inferenceHits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Any other path also counts as an unexpected hit.
		inferenceHits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	paths := state.NewPathsWithDir(t.TempDir())
	client := api.NewClientForTest(server.URL+"/v1", "", 5*time.Second)
	r := NewRefresher(client, paths, "")

	// Multiple refresh passes.
	r.WorkerRefresh(WorkerModels)
	r.WorkerRefresh(WorkerModels)

	if inferenceHits.Load() != 0 {
		t.Errorf("monitoring must never hit inference or unknown endpoints: %d hits", inferenceHits.Load())
	}
}

// TestConcurrentCircuitBreakerUpdates is the P1-2 regression test: two workers
// (models and health) simultaneously recording failures and resets must not
// lose each other's updates. Before the fix, LoadGlobal → modify → SaveCB ran
// without a shared lock, so one worker's save could clobber the other's.
func TestConcurrentCircuitBreakerUpdates(t *testing.T) {
	paths := state.NewPathsWithDir(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Use a real refresher with a guaranteed-to-fail client so recordFailure
	// always fires.
	client := api.NewClientForTest("http://127.0.0.1:1", "", 100*time.Millisecond)
	r := NewRefresher(client, paths, "http://127.0.0.1:1/health")

	var wg sync.WaitGroup
	now := time.Now()

	// Goroutine A: record models failures concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			r.recordFailure(WorkerModels, errors.New("fail"), now)
		}
	}()

	// Goroutine B: record health failures concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			r.recordFailure(WorkerHealth, errors.New("fail"), now)
		}
	}()

	// Goroutine C: reset models breaker concurrently (race against A).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			r.resetCircuitBreaker(WorkerModels)
		}
	}()

	wg.Wait()

	// After all concurrent operations, both endpoints must have a circuit
	// breaker record. The exact final state depends on the race between
	// recordFailure and reset, but neither endpoint's record should be lost.
	cbs, err := state.LoadGlobal(paths)
	if err != nil {
		t.Fatal(err)
	}
	foundModels := false
	foundHealth := false
	for _, cb := range cbs.CircuitBreakers {
		if cb.Endpoint == WorkerModels {
			foundModels = true
		}
		if cb.Endpoint == WorkerHealth {
			foundHealth = true
		}
	}
	if !foundModels {
		t.Error("models circuit breaker was lost in concurrent updates")
	}
	if !foundHealth {
		t.Error("health circuit breaker was lost in concurrent updates")
	}
}
