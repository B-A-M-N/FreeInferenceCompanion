package runtime

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestSaltCreation_SingleProcess tests basic salt creation functionality.
func TestSaltCreation_SingleProcess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "salt-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	os.Setenv("FI_CACHE_DIR", tmpDir)
	ResetSaltCache()

	loader := DefaultSaltLoader()
	salt, err := loader()
	if err != nil {
		t.Fatalf("DefaultSaltLoader: %v", err)
	}

	// Salt must be 32 bytes.
	if len(salt) != 32 {
		t.Errorf("salt length = %d, want 32", len(salt))
	}

	// File must exist with correct permissions.
	saltFile := filepath.Join(tmpDir, "salt")
	info, err := os.Stat(saltFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("salt file mode = %v, want 0600", info.Mode().Perm())
	}
	if info.Size() != 32 {
		t.Errorf("salt file size = %d, want 32", info.Size())
	}
}

// TestSaltRead_ExistingFile tests that an existing valid salt file is read correctly.
func TestSaltRead_ExistingFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "salt-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Pre-create a salt file with a known value.
	originalSalt := make([]byte, 32)
	if _, err := rand.Read(originalSalt); err != nil {
		t.Fatalf("random: %v", err)
	}
	saltFile := filepath.Join(tmpDir, "salt")
	if err := os.WriteFile(saltFile, originalSalt, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	os.Setenv("FI_CACHE_DIR", tmpDir)
	ResetSaltCache()

	loader := DefaultSaltLoader()
	salt, err := loader()
	if err != nil {
		t.Fatalf("DefaultSaltLoader: %v", err)
	}

	// Should read the existing salt.
	if string(salt) != string(originalSalt) {
		t.Errorf("salt mismatch: expected %x, got %x", originalSalt, salt)
	}
}

// TestSaltInvalidFile_ReplacedWithValid tests that invalid files are replaced.
func TestSaltInvalidFile_ReplacedWithValid(t *testing.T) {
	testCases := []struct {
		name  string
		setup func(string) error
	}{
		{
			name: "wrong length",
			setup: func(path string) error {
				return os.WriteFile(path, []byte("short"), 0600)
			},
		},
		{
			name: "wrong permissions",
			setup: func(path string) error {
				salt := make([]byte, 32)
				if _, err := rand.Read(salt); err != nil {
					return err
				}
				return os.WriteFile(path, salt, 0644)
			},
		},
		{
			name: "too long",
			setup: func(path string) error {
				salt := make([]byte, 64)
				if _, err := rand.Read(salt); err != nil {
					return err
				}
				return os.WriteFile(path, salt, 0600)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "salt-"+tc.name+"-*")
			if err != nil {
				t.Fatalf("MkdirTemp: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			saltPath := filepath.Join(tmpDir, "salt")
			if err := tc.setup(saltPath); err != nil {
				t.Fatalf("setup: %v", err)
			}

			os.Setenv("FI_CACHE_DIR", tmpDir)
			ResetSaltCache()

			loader := DefaultSaltLoader()
			salt, err := loader()
			if err != nil {
				t.Fatalf("DefaultSaltLoader: %v", err)
			}

			// Salt must be 32 bytes.
			if len(salt) != 32 {
				t.Errorf("salt length = %d, want 32", len(salt))
			}

			// File must now be valid.
			info, err := os.Stat(saltPath)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Mode().Perm() != 0600 {
				t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
			}
			if info.Size() != 32 {
				t.Errorf("file size = %d, want 32", info.Size())
			}
		})
	}
}

// TestSaltFileSymlink_ReplacedWithValid tests that symlink salt files are replaced with valid ones.
func TestSaltFileSymlink_ReplacedWithValid(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "salt-symlink-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid 32-byte salt file to point at.
	realSalt := make([]byte, 32)
	if _, err := rand.Read(realSalt); err != nil {
		t.Fatalf("random: %v", err)
	}
	realFile := filepath.Join(tmpDir, "real-salt")
	if err := os.WriteFile(realFile, realSalt, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Create a symlink pointing to it.
	saltFile := filepath.Join(tmpDir, "salt")
	if err := os.Symlink(realFile, saltFile); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	os.Setenv("FI_CACHE_DIR", tmpDir)
	ResetSaltCache()

	loader := DefaultSaltLoader()
	salt, err := loader()
	if err != nil {
		t.Fatalf("expected success (symlink replaced), got error: %v", err)
	}

	// Should have created a new valid salt (different from the symlink target).
	if len(salt) != 32 {
		t.Errorf("salt length = %d, want 32", len(salt))
	}

	// The new file must be a regular file with 0600 permissions.
	info, err := os.Stat(saltFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("salt file is still a symlink")
	}
	if !info.Mode().IsRegular() {
		t.Error("salt is not a regular file")
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("salt file mode = %v, want 0600", info.Mode().Perm())
	}
}

// TestSaltFilePermissions tests that created salt files have 0600 permissions.
func TestSaltFilePermissions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "salt-perms-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	os.Setenv("FI_CACHE_DIR", tmpDir)
	ResetSaltCache()

	loader := DefaultSaltLoader()
	salt, err := loader()
	if err != nil {
		t.Fatalf("DefaultSaltLoader: %v", err)
	}

	if len(salt) != 32 {
		t.Errorf("salt length = %d, want 32", len(salt))
	}

	saltFile := filepath.Join(tmpDir, "salt")
	info, err := os.Stat(saltFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("salt file mode = %v, want 0600", info.Mode().Perm())
	}
}

// TestSaltLoader_CachesResult tests that DefaultSaltLoader caches its result.
func TestSaltLoader_CachesResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "salt-cache-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	os.Setenv("FI_CACHE_DIR", tmpDir)
	ResetSaltCache()

	loader := DefaultSaltLoader()
	salt1, err := loader()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	salt2, err := loader()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	// Same loader should return cached result.
	if string(salt1) != string(salt2) {
		t.Errorf("cached salt differs from original")
	}
}

// TestSaltMultipleInitialLoads tests that multiple calls in sequence all return the same salt.
func TestSaltMultipleInitialLoads(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "salt-multi-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	os.Setenv("FI_CACHE_DIR", tmpDir)

	// First load creates the salt.
	ResetSaltCache()
	loader1 := DefaultSaltLoader()
	salt1, err := loader1()
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	// Second load (after reset) should read the same salt.
	ResetSaltCache()
	loader2 := DefaultSaltLoader()
	salt2, err := loader2()
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if string(salt1) != string(salt2) {
		t.Errorf("salts differ: first=%x, second=%x", salt1, salt2)
	}

	// Third load should also return the same salt.
	ResetSaltCache()
	loader3 := DefaultSaltLoader()
	salt3, err := loader3()
	if err != nil {
		t.Fatalf("third load: %v", err)
	}

	if string(salt1) != string(salt3) {
		t.Errorf("salts differ: first=%x, third=%x", salt1, salt3)
	}
}
