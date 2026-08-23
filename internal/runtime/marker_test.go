package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// The companion-disable marker written by `freeinference companion disable`
// must deactivate the runtime in every new process — this is the persistent
// kill switch (audit 2026-08-22 P1-1).
func TestEvaluateDisabledByMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FI_CONFIG_DIR", dir)
	t.Setenv("FI_DISABLED", "")
	t.Setenv("FI_RUNTIME_INACTIVE", "")

	if DisabledByMarker() {
		t.Fatal("marker reported present before it was created")
	}

	// No endpoint/key either way, but Disabled must be true only with marker
	// semantics intact. Create an active-looking env so we prove the marker
	// wins over a valid configuration.
	t.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	t.Setenv("FREEINFERENCE_API_KEY", "test-key-not-real-000000")

	a := Evaluate()
	if !a.Active {
		t.Fatalf("expected active without marker, got reason=%q", a.InactiveReason)
	}

	// Create the marker exactly as cli.createCompanionMarker does.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".companion-disabled"), []byte("disabled"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !DisabledByMarker() {
		t.Fatal("marker not detected after creation")
	}
	a = Evaluate()
	if a.Active {
		t.Fatal("activation succeeded despite disable marker")
	}
	if !a.Disabled || a.InactiveReason != ReasonDisabled {
		t.Fatalf("expected Disabled/ReasonDisabled, got %v/%q", a.Disabled, a.InactiveReason)
	}
}
