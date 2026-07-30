package engine

import "testing"

func TestProjectionMissingContextIsLowConfidence(t *testing.T) {
	p := ProjectNextRequest(0, 100, nil, 0)
	if p.Confidence != "low" {
		t.Errorf("missing current context / window: confidence = %s, want low", p.Confidence)
	}
	if p.RemainingForOutput != nil {
		t.Error("RemainingForOutput must be nil without a context window")
	}
	if p.Overflow {
		t.Error("Overflow must be false without a context window")
	}
}

func TestProjectionMissingModelLimitIsLowConfidence(t *testing.T) {
	// Active context known but model context window unknown.
	p := ProjectNextRequest(100_000, 100, nil, 0)
	if p.Confidence != "low" {
		t.Errorf("missing window: confidence = %s, want low", p.Confidence)
	}
}

func TestProjectionNeverClaimsMediumOrHighConfidence(t *testing.T) {
	// Even with authoritative current totals AND a known context window,
	// confidence must remain "low" because the companion lacks authoritative
	// full-request accounting and tokenizer access.
	window := int64(200_000)
	p := ProjectNextRequest(100_000, 100, &window, 0)
	if p.Confidence != "low" {
		t.Errorf("confidence = %s, want low (MVP never claims medium/high)", p.Confidence)
	}
}

func TestProjectionConservativePromptEstimate(t *testing.T) {
	// 350 bytes ≈ 100 tokens at 3.5 bytes/token.
	got := ApproximatePromptTokens(350)
	if got < 95 || got > 110 {
		t.Errorf("ApproximatePromptTokens(350) = %d, want ~100", got)
	}
	// Empty input → zero.
	if ApproximatePromptTokens(0) != 0 {
		t.Error("empty prompt must estimate zero tokens")
	}
}

func TestProjectionOutputReserveApplied(t *testing.T) {
	window := int64(200_000)
	p := ProjectNextRequest(180_000, 350, &window, 16_000)
	if p.ReservedOutputTokens != 16_000 {
		t.Errorf("reserve = %d, want 16000", p.ReservedOutputTokens)
	}
	if p.RemainingForOutput == nil {
		t.Fatal("RemainingForOutput should be populated")
	}
	want := window - p.ProjectedTotal - int64(SafetyMarginDefault)
	if *p.RemainingForOutput != want {
		t.Errorf("remaining = %d, want %d", *p.RemainingForOutput, want)
	}
}

func TestProjectionSafetyMarginApplied(t *testing.T) {
	window := int64(200_000)
	p := ProjectNextRequest(100_000, 0, &window, 16_000)
	if p.SafetyMarginTokens != int64(SafetyMarginDefault) {
		t.Errorf("safety margin = %d, want %d", p.SafetyMarginTokens, SafetyMarginDefault)
	}
}

func TestProjectionOverflowTriggersAdvisory(t *testing.T) {
	// 195K active + 5K prompt + tools > window - reserve.
	window := int64(200_000)
	p := ProjectNextRequest(195_000, 5000, &window, 16_000)
	if !p.Overflow {
		t.Errorf("expected overflow, projection = %+v", p)
	}
	msg := p.AdvisoryMessage()
	if msg == "" {
		t.Fatal("overflow must produce an advisory message")
	}
	// Message must label confidence.
	if !contains(msg, "medium") && !contains(msg, "low") {
		t.Errorf("advisory must label confidence: %q", msg)
	}
}

func TestProjectionNoOverflowProducesNoMessage(t *testing.T) {
	window := int64(200_000)
	p := ProjectNextRequest(50_000, 1000, &window, 16_000)
	if p.Overflow {
		t.Error("should not overflow")
	}
	if p.AdvisoryMessage() != "" {
		t.Errorf("no overflow → no message, got %q", p.AdvisoryMessage())
	}
}

func TestProjectionNeverBlocks(t *testing.T) {
	// Even at extreme overflow, the projection returns a message — it does
	// not panic, does not return an error, and never tells the caller to
	// block the prompt. The hook layer always returns continue=true.
	window := int64(200_000)
	p := ProjectNextRequest(1_000_000, 100_000, &window, 16_000)
	if !p.Overflow {
		t.Error("expected overflow")
	}
	// AdvisoryMessage is a string, never an error/block signal.
	_ = p.AdvisoryMessage()
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
