package runtime

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
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

// Sentinel errors for validation failures.
var (
	errSaltNotRegular   = errors.New("salt is not a regular file")
	errSaltIsSymlink    = errors.New("salt is a symlink")
	errSaltBadPerms     = errors.New("salt has wrong permissions")
	errSaltBadLength    = errors.New("salt has wrong length")
	errSaltBadOwnership = errors.New("salt has wrong ownership")
)

var (
	saltOnce  sync.Once
	saltCache []byte
	saltErr   error
)

// DefaultSaltLoader returns a SaltLoader that reads (or creates) the
// installation salt at $FI_CACHE_DIR/salt or ~/.cache/freeinference-companion/salt.
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

	// Try exclusive creation first - this is the fast path for first-run.
	// If we win the race, we generate and write the salt atomically.
	// If we lose (EEXIST), we read and validate the winner's file.
	for {
		salt, err := tryCreateSalt(path)
		if err == nil {
			return salt, nil
		}
		if !os.IsExist(err) {
			// Some other error (permission denied, etc.)
			return nil, ErrSaltIO
		}

		// Another process created the file. Read and validate it.
		salt, err = readAndValidateSalt(path)
		if err == nil {
			return salt, nil
		}

		// If validation failed due to an invalid file (wrong perms, symlink, etc.),
		// remove it and retry creation. Only retry on validation errors,
		// not on genuine I/O errors. Note: errSaltBadLength is NOT a validation
		// error - it could be a partial write in progress, so we retry reading
		// with a small backoff instead of removing the file.
		if isValidationErr(err) {
			os.Remove(path)
			continue
		}

		// For errSaltBadLength, the file may be a partial write in progress.
		// Retry reading a few times with exponential backoff.
		if errors.Is(err, errSaltBadLength) {
			for i := 0; i < 10; i++ {
				time.Sleep(time.Duration(1+i*2) * time.Millisecond)
				salt, err = readAndValidateSalt(path)
				if err == nil {
					return salt, nil
				}
				if !errors.Is(err, errSaltBadLength) {
					break
				}
			}
			// Still wrong length after retries - treat as permanently invalid,
			// remove the file and retry creation.
			os.Remove(path)
			continue
		}

		// Other errors (e.g., I/O error reading) - give up.
		return nil, ErrSaltIO
	}
}

// tryCreateSalt attempts to create the salt file with O_EXCL.
// Returns the salt on success, or os.ErrExist if another process won,
// or another error for genuine failures.
func tryCreateSalt(path string) ([]byte, error) {
	// Ensure parent directory exists with 0700.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	// O_CREATE|O_EXCL is atomic - only one process succeeds.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}

	if _, err := f.Write(salt); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	// Double-check mode after creation (some filesystems don't set it atomically).
	if err := os.Chmod(path, 0600); err != nil {
		os.Remove(path)
		return nil, err
	}
	return salt, nil
}

// readAndValidateSalt reads an existing salt file and validates it.
// Returns the salt on success, or a validation error if the file is invalid.
// Does not retry - the caller handles retry logic for race conditions.
func readAndValidateSalt(path string) ([]byte, error) {
	return validateAndReadSalt(path)
}

// isValidationErr returns true if err is one of our sentinel validation errors
// that indicate a permanently invalid file (should be removed and recreated).
// errSaltBadLength is excluded because a short read could be a partial write
// in progress by another process - we should retry reading instead.
func isValidationErr(err error) bool {
	return errors.Is(err, errSaltNotRegular) ||
		errors.Is(err, errSaltIsSymlink) ||
		errors.Is(err, errSaltBadPerms) ||
		errors.Is(err, errSaltBadOwnership)
}

// validateAndReadSalt validates the salt file exists and is valid, returning its contents.
func validateAndReadSalt(path string) ([]byte, error) {
	// Use Lstat to avoid following symlinks.
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	// Must be a regular file, not a symlink or directory.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errSaltIsSymlink
	}
	if !info.Mode().IsRegular() {
		return nil, errSaltNotRegular
	}
	// Must have mode 0600.
	if info.Mode().Perm() != 0600 {
		return nil, errSaltBadPerms
	}
	// Must be exactly 32 bytes.
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errSaltBadLength
	}
	// Ownership check where supported (not available on Windows).
	if unixFile, ok := info.Sys().(*syscall.Stat_t); ok {
		if uint32(os.Getuid()) != unixFile.Uid {
			return nil, errSaltBadOwnership
		}
	}
	return b, nil
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
