package engine

import (
	"fmt"

	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

// Projection is the advisory estimate of the next request's context usage.
// It is advisory only — confidence is always "low" in MVP because the companion
// does not have authoritative full-request accounting or tokenizer access.
//
// MVP rule: never claim medium or high confidence. The companion does not see the
// full request body that the client sends, so authoritative accounting is not
// possible in the current release.
type Projection struct {
	// CurrentActiveTokens is the best local estimate of the active context.
	CurrentActiveTokens int64
	// EstimatedPromptTokens is the approximate size of the incoming prompt.
	EstimatedPromptTokens int64
	// ToolOverheadTokens is the conservative per-turn tool overhead.
	ToolOverheadTokens int64
	// ReservedOutputTokens is what we withhold for the model's reply.
	ReservedOutputTokens int64
	// SafetyMarginTokens is a small additional safety margin.
	SafetyMarginTokens int64
	// ContextWindowSize, when known, bounds the model's available budget.
	ContextWindowSize *int64
	// ProjectedTotal is the sum of all input-side terms.
	ProjectedTotal int64
	// RemainingForOutput is ContextWindowSize - ProjectedTotal - SafetyMargin.
	// nil when ContextWindowSize is unknown.
	RemainingForOutput *int64
	// Confidence is "low". Never "medium" or "high" in MVP — the companion
	// does not have authoritative full-request accounting or tokenizer access.
	Confidence string
	// Overflow is true when ProjectedTotal exceeds ContextWindowSize - Reserve.
	Overflow bool
}

// ToolOverheadPerTurn is the default conservative per-turn tool overhead.
// Tools, system prompts, and metadata framing add a small constant overhead
// per request that the client does not surface as user-visible tokens.
const ToolOverheadPerTurn = 512

// SafetyMarginDefault is the additional buffer beyond the output reserve.
// It absorbs tokenizer approximation error and minor framing differences.
const SafetyMarginDefault = 2048

// DefaultOutputReserve returns the configured default output reserve from the
// config system (env → file → default precedence). Kept as a function so
// callers that previously referenced the package-level variable still compile.
func DefaultOutputReserve() int {
	return lazyThresholds().OutputReserve()
}

// ApproximatePromptTokens estimates prompt token count without contacting a
// remote tokenizer. The estimate is deliberately conservative (rounds up).
// Bytes-per-token ratios vary by language; 3.5 bytes/token covers Latin text
// and most code reasonably, while CJK and emoji are denser.
func ApproximatePromptTokens(promptBytes int) int64 {
	if promptBytes <= 0 {
		return 0
	}
	const bytesPerToken = 3.5
	n := int64(float64(promptBytes)/bytesPerToken + 0.999) // ceil
	return n
}

// ProjectNextRequest estimates the projected context for the next request
// using local data only. It never contacts a remote service or a model.
//
// currentActiveTokens is the best local estimate (see adapters.ActiveContextTokens).
// promptBytes is the byte length of the incoming prompt, if known.
// contextWindowSize is the model's context budget, if known.
// outputReserve is the tokens to withhold for the reply (defaults applied when 0).
func ProjectNextRequest(currentActiveTokens int64, promptBytes int, contextWindowSize *int64, outputReserve int) Projection {
	if outputReserve <= 0 {
		outputReserve = DefaultOutputReserve()
	}
	p := Projection{
		CurrentActiveTokens:   currentActiveTokens,
		EstimatedPromptTokens: ApproximatePromptTokens(promptBytes),
		ToolOverheadTokens:    int64(ToolOverheadPerTurn),
		ReservedOutputTokens:  int64(outputReserve),
		SafetyMarginTokens:    int64(SafetyMarginDefault),
		ContextWindowSize:     contextWindowSize,
	}
	p.ProjectedTotal = p.CurrentActiveTokens + p.EstimatedPromptTokens + p.ToolOverheadTokens

	// MVP: only "low" confidence. The companion does not have authoritative
	// full-request accounting or tokenizer access, so we never claim higher
	// confidence. Presence of local data does not imply precision.
	p.Confidence = "low"

	if contextWindowSize != nil && *contextWindowSize > 0 {
		remaining := *contextWindowSize - p.ProjectedTotal - p.SafetyMarginTokens
		p.RemainingForOutput = &remaining
		if remaining < int64(outputReserve) {
			p.Overflow = true
		}
	}

	return p
}

// AdvisoryMessage renders a user-facing, advisory-only message. It is empty
// when there is nothing worth surfacing. The message always labels the
// projection confidence so the user knows how much to trust it.
func (p Projection) AdvisoryMessage() string {
	if p.RemainingForOutput == nil {
		return ""
	}
	if !p.Overflow {
		return ""
	}
	reserve := p.ReservedOutputTokens
	if reserve <= 0 {
		reserve = int64(DefaultOutputReserve())
	}
	return formatProjectionOverflow(*p.RemainingForOutput, reserve, p.Confidence)
}

// HasCurrentContext reports whether the projection has enough local data to
// be worth surfacing. When false, callers should suppress the warning.
func (p Projection) HasCurrentContext() bool {
	return p.CurrentActiveTokens > 0
}

// formatProjectionOverflow renders the overflow message. Kept separate so the
// wording is consistent across the adapter and the CLI.
func formatProjectionOverflow(remainingForOutput, reserve int64, confidence string) string {
	return fmt.Sprintf(
		"FreeInference: projected next request leaves %d tokens for output (reserve %d). "+
			"Consider compacting before sending. Projection confidence: %s.",
		remainingForOutput, reserve, confidence)
}

// ensure schema package stays referenced for future expansion of model metadata.
var _ = schema.StateVersion
