package runtime

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// SaltLoader returns the installation salt, creating it on first call. The
// salt is a 32-byte random value persisted with 0600 permissions. It is NOT
// a secret in its own right — its job is to ensure that a leaked credential
// fingerprint cannot be reversed via a rainbow table built from a known
// unsalted hash. Per-installation randomness plus the HMAC construction make
// fingerprints unique even across installs that happen to share a key.
type SaltLoader func() ([]byte, error)

// ErrSaltIO is returned for salt-file I/O failures other than "not found".
var ErrSaltIO = errors.New("salt I/O error")

var (
	saltOnce  sync.Once
	saltCache []byte
	saltErr   error
)

// DefaultSaltLoader returns a SaltLoader that reads (or creates) the
// installation salt at $FI_CACHE_DIR/../salt or ~/.cache/freeinference-companion/salt.
// The result is cached for the process lifetime so repeated calls are cheap.
// Tests clear the cache with ResetSaltCache.
func DefaultSaltLoader() SaltLoader {
	return func() ([]byte, error) {
		saltOnce.Do(func() {
			saltCache, saltErr = loadOrCreateSalt()
		})
		return saltCache, saltErr
	}
}

// ResetSaltCache clears the cached salt. For tests that mutate FI_CACHE_DIR.
func ResetSaltCache() {
	saltOnce = sync.Once{}
	saltCache = nil
	saltErr = nil
}

func loadOrCreateSalt() ([]byte, error) {
	path, err := saltPath()
	if err != nil {
		return nil, err
	}
	// Try to read first.
	if b, err := os.ReadFile(path); err == nil {
		if len(b) >= 16 {
			return b, nil
		}
		// Too short — treat as missing and recreate.
	}
	// Create the directory with 0700 so the salt file is owned only by the user.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	// Write atomically with 0600 so a concurrent reader never sees a partial
	// file and no other user can read the salt.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".salt-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(salt); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	return salt, nil
}

func saltPath() (string, error) {
	if dir := os.Getenv("FI_CACHE_DIR"); dir != "" {
		return filepath.Join(dir, "salt"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "freeinference-companion", "salt"), nil
}

// credentialFingerprint returns a non-reversible HMAC-SHA256 of cred keyed by
// salt, hex-encoded and truncated to 16 bytes (32 hex chars). Truncation is
// safe because the fingerprint is a matching key, not a pre-image-attack target.
func credentialFingerprint(cred string, salt []byte) string {
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(cred))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

// MarshalSalt For diagnostic tooling only — never write this to logs.
func MarshalSalt(salt []byte) []byte {
	return []byte(hex.EncodeToString(salt))
}

// SaltSummary is a JSON-serializable diagnostic view of the salt state.
type SaltSummary struct {
	Path      string `json:"path,omitempty"`
	ByteLen   int    `json:"byte_len"`
	Available bool   `json:"available"`
}

// _ sentinel keeps encoding/json imported for future diagnostic marshaling.
var _ = json.Marshal
