package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/b-a-m-n/freeinference-companion/internal/config"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// ============================================================
// Rolling cache analysis
// ============================================================

const (
	// CacheHistoryRetention bounds the per-session observation window.
	// This is the maximum number of unique observations persisted to the
	// session snapshot. Separate from the analysis window and viewport size.
	CacheHistoryRetention = 20

	// CacheAnalysisWindow is how many recent unique observations feed rolling
	// shares in AnalyzeCache. Kept distinct from retention so analysis can
	// use a smaller window than the full history.
	CacheAnalysisWindow = 5

	// CacheHistoryViewportRows is the recommended number of rows to display
	// at one time in an interactive history viewer. Used by TUI/panel code;
	// the engine itself does not enforce this.
	CacheHistoryViewportRows = 8

	// MinObservationsForWarning is the minimum sample count before a
	// cache-low warning may qualify.
	MinObservationsForWarning = 3
	// MinContextTokensForWarning gates warnings on meaningful active context.
	MinContextTokensForWarning = 50000
	// CacheReadLowThreshold is the read share below which reuse counts as low.
	CacheReadLowThreshold = 0.20
	// CacheReadRecoveredThreshold is the read share above which reuse counts recovered.
	CacheReadRecoveredThreshold = 0.40
	// ConsecutiveToWarn is how many sequential low observations activate a warning.
	ConsecutiveToWarn = 3
	// ConsecutiveToResolve is how many sequential good observations resolve it.
	ConsecutiveToResolve = 3
	// CacheWarningCooldown suppresses repeat cache-low warnings.
	CacheWarningCooldown = 30 * time.Minute
	// TrendThreshold is the read-share delta (in share points) that marks a trend.
	TrendThreshold = 0.05
)

// MaxUsageObservations is retained as an alias for CacheHistoryRetention
// for backwards compatibility with code that references it directly.
const MaxUsageObservations = CacheHistoryRetention

// AnalysisWindow is retained as an alias for CacheAnalysisWindow.
const AnalysisWindow = CacheAnalysisWindow

// cacheConfig holds the resolved cache warning thresholds.
type cacheConfig struct {
	lowThreshold       float64
	recoveredThreshold float64
	warningCooldown    time.Duration
}

var (
	cacheConfOnce sync.Once
	cacheConf     *cacheConfig
)

func initCacheConfig() {
	mgr, err := config.NewManager()
	if err != nil {
		return
	}
	eff, err := mgr.Resolve()
	if err != nil {
		return
	}

	c := &cacheConfig{
		lowThreshold:       CacheReadLowThreshold,
		recoveredThreshold: CacheReadRecoveredThreshold,
		warningCooldown:    CacheWarningCooldown,
	}
	if eff.Cache.WarnThreshold.Valid {
		c.lowThreshold = eff.Cache.WarnThreshold.Value
	}
	if eff.Cache.RecoveredThreshold.Valid {
		c.recoveredThreshold = eff.Cache.RecoveredThreshold.Value
	}
	if eff.Cache.CooldownMins.Valid && eff.Cache.CooldownMins.Value > 0 {
		c.warningCooldown = time.Duration(eff.Cache.CooldownMins.Value) * time.Minute
	}
	if c.lowThreshold < 0 || c.recoveredThreshold > 1 || c.lowThreshold >= c.recoveredThreshold {
		c.lowThreshold = CacheReadLowThreshold
		c.recoveredThreshold = CacheReadRecoveredThreshold
	}
	cacheConf = c
}

func cacheConfigGet() *cacheConfig {
	cacheConfOnce.Do(initCacheConfig)
	if cacheConf == nil {
		return &cacheConfig{
			lowThreshold:       CacheReadLowThreshold,
			recoveredThreshold: CacheReadRecoveredThreshold,
			warningCooldown:    CacheWarningCooldown,
		}
	}
	return cacheConf
}

// ObservationFingerprint builds a stable fingerprint for a usage sample so
// re-renders of the same status-line data are not double-counted.
// When promptID is non-empty it is the primary discriminator (each request
// gets a unique ID). The token-based fallback handles older clients.
//
// Finding 9: returns the fingerprint AND its source so callers can record
// the derivation confidence alongside the fingerprint value.
func ObservationFingerprint(modelID, promptID string, totalInput, totalOutput int64, fresh, cacheRead, cacheCreation, output *int64) (string, schema.FingerprintSource) {
	var buf []byte
	if promptID != "" {
		buf = fmt.Appendf(buf, "pid:%s", promptID)
		return fingerprintID(buf), schema.FingerprintClientTurnID
	}
	buf = fmt.Appendf(buf, "tok|%s|%d|%d|%d|%d|%d|%d", modelID, totalInput, totalOutput,
		derefI64(fresh), derefI64(cacheRead), derefI64(cacheCreation), derefI64(output))
	return fingerprintID(buf), schema.FingerprintFallback
}

func fingerprintID(buf []byte) string {
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:16])
}

func derefI64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// AddObservation appends a unique observation to the snapshot.
// Returns true if the observation was new (fingerprint not seen before).
func AddObservation(snap *schema.Snapshot, obs schema.UsageObservation) bool {
	for i := range snap.UsageObservations {
		if snap.UsageObservations[i].Fingerprint == obs.Fingerprint {
			return false
		}
	}
	snap.UsageObservations = append(snap.UsageObservations, obs)
	if len(snap.UsageObservations) > MaxUsageObservations {
		snap.UsageObservations = snap.UsageObservations[len(snap.UsageObservations)-MaxUsageObservations:]
	}
	return true
}

// observationInput sums the observed input components of one observation.
func observationInput(o schema.UsageObservation) (fresh, read, creation int64, ok bool) {
	if o.FreshInputTokens == nil || o.CacheReadInputTokens == nil || o.CacheCreationInputTokens == nil {
		return 0, 0, 0, false
	}
	return *o.FreshInputTokens, *o.CacheReadInputTokens, *o.CacheCreationInputTokens, true
}

// AnalyzeCache recomputes the rolling cache analysis on the snapshot from its
// unique usage observations. currentContextTokens is the best available
// estimate of the active context size (used for warning qualification).
//
// This function is idempotent: consecutive counters are recomputed from the
// observation sequence on every call, so re-running it on unchanged state
// never inflates them. Duplicate status-line renders of the same response
// cannot manufacture a cache-low warning.
func AnalyzeCache(snap *schema.Snapshot, currentContextTokens int64, now time.Time) {
	if snap.CacheAnalysis == nil {
		snap.CacheAnalysis = &schema.CacheAnalysis{}
	}
	analysis := snap.CacheAnalysis

	obs := snap.UsageObservations
	analysis.RequestSamples = len(obs)

	window := obs
	if len(window) > AnalysisWindow {
		window = window[len(window)-AnalysisWindow:]
	}

	var totalFresh, totalRead, totalCreation int64
	var counted int
	for _, o := range window {
		fresh, read, creation, ok := observationInput(o)
		if !ok {
			continue
		}
		totalFresh += fresh
		totalRead += read
		totalCreation += creation
		counted++
	}

	if counted == 0 {
		analysis.CacheReadShare = nil
		analysis.CacheCreationShare = nil
		analysis.FreshInputShare = nil
		analysis.Trend = schema.TrendInsufficientData
		analysis.ConsecutiveLow = 0
		analysis.ConsecutiveRecovered = 0
		return
	}

	totalInput := totalFresh + totalRead + totalCreation
	if totalInput <= 0 {
		analysis.CacheReadShare = nil
		analysis.CacheCreationShare = nil
		analysis.FreshInputShare = nil
		analysis.Trend = schema.TrendInsufficientData
		analysis.ConsecutiveLow = 0
		analysis.ConsecutiveRecovered = 0
		return
	}

	readShare := float64(totalRead) / float64(totalInput)
	creationShare := float64(totalCreation) / float64(totalInput)
	freshShare := float64(totalFresh) / float64(totalInput)

	// Trend vs. previous share (±5 percentage points).
	if analysis.CacheReadShare != nil {
		analysis.PreviousReadShare = analysis.CacheReadShare
		delta := readShare - *analysis.PreviousReadShare
		switch {
		case delta >= TrendThreshold:
			analysis.Trend = schema.TrendRising
		case delta <= -TrendThreshold:
			analysis.Trend = schema.TrendDeclining
		default:
			analysis.Trend = schema.TrendStable
		}
	} else {
		analysis.PreviousReadShare = nil
		analysis.Trend = schema.TrendInsufficientData
	}

	analysis.CacheReadShare = &readShare
	analysis.CacheCreationShare = &creationShare
	analysis.FreshInputShare = &freshShare

	// Idempotent consecutive counters: walk the observation sequence from
	// most recent backward, counting how many consecutive observations had
	// read share below / above the thresholds. Recomputing from scratch
	// (rather than incrementing) guarantees that re-running analysis on
	// unchanged state never inflates the counters — duplicate status-line
	// renders cannot manufacture a warning.
	analysis.ConsecutiveLow = 0
	analysis.ConsecutiveRecovered = 0
	var streak int // 0 = unset, -1 = low, +1 = recovered
	for i := len(obs) - 1; i >= 0; i-- {
		share := observationReadShare(obs[i])
		if share == nil {
			break // incomplete observation breaks the streak
		}
		var bucket int // -1 low, 0 neutral, +1 recovered
		cfg := cacheConfigGet()
		switch {
		case *share < cfg.lowThreshold:
			bucket = -1
		case *share > cfg.recoveredThreshold:
			bucket = 1
		default:
			bucket = 0
		}
		if bucket == 0 {
			break // neutral zone breaks any streak
		}
		if streak == 0 {
			streak = bucket
		}
		if bucket != streak {
			break // direction changed — stop counting
		}
		if bucket < 0 {
			analysis.ConsecutiveLow++
		} else {
			analysis.ConsecutiveRecovered++
		}
	}
}

// observationReadShare returns the cache read share for a single observation,
// or nil if the observation has no valid input breakdown.
func observationReadShare(o schema.UsageObservation) *float64 {
	fresh, read, creation, ok := observationInput(o)
	if !ok {
		return nil
	}
	total := fresh + read + creation
	if total <= 0 {
		return nil
	}
	share := float64(read) / float64(total)
	return &share
}

// CacheWarningDecision is the outcome of cache-low warning qualification.
type CacheWarningDecision struct {
	Warn     bool
	Resolved bool
	Share    *float64
}

// QualifyCacheWarning decides whether a cache-low warning should fire,
// resolve, or stay quiet. All gates must pass:
//   - enough unique observations
//   - active context above the minimum size
//   - read share below threshold for enough sequential observations
//   - provider confirmed as FreeInference
//   - cooldown elapsed since the last shown cache warning
func QualifyCacheWarning(snap *schema.Snapshot, currentContextTokens int64, providerConfirmed bool, now time.Time) CacheWarningDecision {
	decision := CacheWarningDecision{}
	analysis := snap.CacheAnalysis
	if analysis == nil {
		return decision
	}
	decision.Share = analysis.CacheReadShare

	if !providerConfirmed {
		return decision
	}
	if analysis.RequestSamples < MinObservationsForWarning {
		return decision
	}
	if currentContextTokens < MinContextTokensForWarning {
		return decision
	}

	// Resolution: share recovered above threshold for enough observations.
	if snap.Warnings.CacheLowActive &&
		analysis.ConsecutiveRecovered >= ConsecutiveToResolve {
		decision.Resolved = true
		return decision
	}

	// Activation: share below threshold for enough sequential observations.
	if analysis.ConsecutiveLow >= ConsecutiveToWarn {
		// Cooldown since last shown warning.
		if snap.Warnings.LastCacheShownAt != nil &&
			now.Sub(*snap.Warnings.LastCacheShownAt) < cacheConfigGet().warningCooldown {
			return decision
		}
		decision.Warn = true
		return decision
	}

	return decision
}
