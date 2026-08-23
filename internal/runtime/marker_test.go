package runtime

import (
	"testing"
)

// The persistent companion-disable marker written by
// `freeinference companion disable` must deactivate the runtime in every new
// process even when a valid endpoint+key is configured — this is the
// on-disk kill switch (audit 2026-08-22 P1-1).
func TestEvaluateDisabledByMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	t.Setenv("FI_DISABLED", "")
	t.Setenv("FI_RUNTIME_INACTIVE", "")

	// Active-looking env so we prove the marker wins over a valid config.
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "test-key-not-real-000000")

	a := Evaluate()
	if !a.Active {
		t.Fatalf("expected active without marker, got reason=%q", a.InactiveReason)
	}

	if err := DisablePersistently(); err != nil {
		t.Fatal(err)
	}
	if disabled, err := PersistentDisableState(); err != nil || !disabled {
		t.Fatalf("PersistentDisableState = %v, %v; want true, nil", disabled, err)
	}

	a = Evaluate()
	if a.Active {
		t.Fatal("activation succeeded despite persistent disable marker")
	}
	if !a.Disabled || !a.DisabledByMarker || a.InactiveReason != ReasonDisabled {
		t.Fatalf("expected Disabled/DisabledByMarker/ReasonDisabled, got %v/%v/%q",
			a.Disabled, a.DisabledByMarker, a.InactiveReason)
	}

	if err := EnablePersistently(); err != nil {
		t.Fatal(err)
	}
	a = Evaluate()
	if !a.Active {
		t.Fatalf("expected active after EnablePersistently, got reason=%q", a.InactiveReason)
	}
}
