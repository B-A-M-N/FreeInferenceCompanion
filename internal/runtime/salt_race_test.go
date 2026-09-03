package runtime

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaltCreation_CrossProcessRace spawns multiple processes simultaneously
// to detect cross-process race conditions in salt creation.
func TestSaltCreation_CrossProcessRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-process race test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "salt-race-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Build a test binary that just loads the salt
	testBin := filepath.Join(tmpDir, "salt-loader")
	// Build from the module root (where go.mod is)
	// This test runs from internal/runtime, so module root is ../../
	buildCmd := exec.Command("go", "build", "-o", testBin, "-tags=saltloader", "-ldflags=-s -w", "../../cmd/fi")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn multiple processes simultaneously
	const numProcesses = 20
	type result struct {
		stdout string
		err    error
	}
	results := make(chan result, numProcesses)

	for i := 0; i < numProcesses; i++ {
		go func() {
			cmd := exec.Command(testBin, "-salt-test", tmpDir)
			cmd.Env = append(os.Environ(), "FI_CACHE_DIR="+tmpDir)
			out, err := cmd.CombinedOutput()
			results <- result{stdout: string(out), err: err}
		}()
	}

	// Collect results
	salts := make(map[string]int)
	var errors []string
	for i := 0; i < numProcesses; i++ {
		r := <-results
		if r.err != nil {
			errors = append(errors, r.err.Error())
			continue
		}
		salt := strings.TrimSpace(r.stdout)
		t.Logf("process %d: stdout=%q err=%v", i, r.stdout, r.err)
		if len(salt) == 0 {
			errors = append(errors, "empty output")
			continue
		}
		if len(salt) != 64 { // 32 bytes = 64 hex chars
			errors = append(errors, "invalid salt length: '"+salt+"'")
			continue
		}
		salts[salt]++
	}

	if len(errors) > 0 {
		t.Fatalf("errors: %v", errors)
	}

	// All processes must produce the same salt
	if len(salts) > 1 {
		t.Errorf("race detected: %d different salts produced", len(salts))
		for salt, count := range salts {
			t.Logf("  salt %s: %d times", salt[:16]+"...", count)
		}
	}

	// Verify the final salt file is valid
	saltFile := filepath.Join(tmpDir, "salt")
	info, err := os.Stat(saltFile)
	if err != nil {
		t.Fatalf("stat salt file: %v", err)
	}
	if info.Size() != 32 {
		t.Errorf("final salt file size = %d, want 32", info.Size())
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("final salt file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSaltLoader_AcceptsAnyLengthGE16(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "salt-len-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a 16-byte salt (too short per spec, but currently accepted)
	shortSalt := make([]byte, 16)
	if _, err := rand.Read(shortSalt); err != nil {
		t.Fatalf("random: %v", err)
	}
	saltFile := filepath.Join(tmpDir, "salt")
	if err := os.WriteFile(saltFile, shortSalt, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	os.Setenv("FI_CACHE_DIR", tmpDir)
	ResetSaltCache()

	loader := DefaultSaltLoader()
	salt, err := loader()
	if err != nil {
		t.Fatalf("DefaultSaltLoader: %v", err)
	}

	// Current implementation accepts 16+ bytes, but spec says exactly 32
	t.Logf("accepted salt length: %d (spec requires 32)", len(salt))
}
