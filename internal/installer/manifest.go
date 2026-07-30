package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// PlatformKey identifies a target platform (e.g. "linux-amd64").
type PlatformKey string

// PlatformInfo holds the download URL and expected SHA-256 checksum for a
// single platform's release ZIP archive.
type PlatformInfo struct {
	URL  string `json:"url"`
	Hash string `json:"sha256"`
}

// MarketplaceManifest describes the latest release available from a
// marketplace endpoint (GitHub Releases or a custom JSON host).
type MarketplaceManifest struct {
	// Version is the semver string for the latest release.
	Version string `json:"version"`
	// Platforms maps platform keys to download URLs and checksums.
	Platforms map[string]PlatformInfo `json:"platforms"`
	// PluginURLs maps plugin names to their release asset URLs.
	PluginURLs map[string]string `json:"plugin_urls"`
}

// platformManifest is the wire-format version (PlatformInfo re-defined to match the JSON schema exactly).
type platformManifest struct {
	URL  string `json:"url"`
	Hash string `json:"sha256"`
}

// manifestWire is the full wire format for the marketplace JSON.
type manifestWire struct {
	Version    string                      `json:"version"`
	Platforms  map[string]platformManifest `json:"platforms"`
	PluginURLs map[string]string           `json:"plugin_urls"`
}

// FetchManifest downloads and parses a marketplace manifest from the given URL.
func FetchManifest(manifestURL string) (*MarketplaceManifest, error) {
	if err := validateRemoteURL(manifestURL); err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	resp, err := http.Get(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch manifest: HTTP %d", resp.StatusCode)
	}

	var wire manifestWire
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&wire); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	out := &MarketplaceManifest{
		Version:    wire.Version,
		Platforms:  make(map[string]PlatformInfo, len(wire.Platforms)),
		PluginURLs: wire.PluginURLs,
	}
	for k, v := range wire.Platforms {
		out.Platforms[k] = PlatformInfo{URL: v.URL, Hash: v.Hash}
	}
	if err := out.Validate(); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return out, nil
}

var semverPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

// Validate rejects malformed manifests before any platform asset is selected.
// This catches publication mistakes and prevents ambiguous version comparisons.
func (m *MarketplaceManifest) Validate() error {
	if !semverPattern.MatchString(m.Version) {
		return fmt.Errorf("version must be a semantic version")
	}
	if len(m.Platforms) == 0 {
		return fmt.Errorf("platforms must not be empty")
	}
	for platform, info := range m.Platforms {
		if strings.TrimSpace(platform) == "" {
			return fmt.Errorf("platform key must not be empty")
		}
		if err := validateRemoteURL(info.URL); err != nil {
			return fmt.Errorf("platform %q URL: %w", platform, err)
		}
		if len(info.Hash) != sha256.Size*2 {
			return fmt.Errorf("platform %q checksum must be a %d-character SHA-256 hex digest", platform, sha256.Size*2)
		}
		if _, err := hex.DecodeString(info.Hash); err != nil {
			return fmt.Errorf("platform %q checksum: %w", platform, err)
		}
	}
	return nil
}

// VerifyChecksum computes the SHA-256 hex digest of data and compares it
// with the expected hash from the manifest.
func VerifyChecksum(data []byte, expectedHash string) error {
	if len(expectedHash) != sha256.Size*2 {
		return fmt.Errorf("checksum must be a %d-character SHA-256 hex digest", sha256.Size*2)
	}
	if _, err := hex.DecodeString(expectedHash); err != nil {
		return fmt.Errorf("invalid checksum digest: %w", err)
	}
	h := sha256.Sum256(data)
	actual := hex.EncodeToString(h[:])
	if actual != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actual)
	}
	return nil
}

// Platform returns the PlatformInfo for the given platform key, or an error
// if the platform is not in the manifest.
func (m *MarketplaceManifest) Platform(key PlatformKey) (PlatformInfo, error) {
	info, ok := m.Platforms[string(key)]
	if !ok {
		return PlatformInfo{}, fmt.Errorf("platform %q not found in manifest", key)
	}
	return info, nil
}

// IsNewer reports whether m.Version is semantically newer than existingVersion
// using a simple numeric comparison of the major.minor.patch components.
// Returns false when m.Version is not newer, or when the versions share the
// same numeric components.
func (m *MarketplaceManifest) IsNewer(existingVersion string) bool {
	existing := parseVersion(existingVersion)
	latest := parseVersion(m.Version)
	if latest.major > existing.major {
		return true
	}
	if latest.major != existing.major {
		return false
	}
	if latest.minor > existing.minor {
		return true
	}
	if latest.minor != existing.minor {
		return false
	}
	return latest.patch > existing.patch
}

type versionParts struct {
	major, minor, patch int
}

func parseVersion(v string) versionParts {
	var parts versionParts
	fmt.Sscanf(v, "v%d.%d.%d", &parts.major, &parts.minor, &parts.patch)
	if parts.major == 0 && parts.minor == 0 && parts.patch == 0 {
		fmt.Sscanf(v, "%d.%d.%d", &parts.major, &parts.minor, &parts.patch)
	}
	return parts
}

// DownloadTo downloads the resource at url to path, returning the number of
// bytes written. On success the caller must verify the checksum against the
// expected hash in the manifest.
func DownloadTo(downloadURL, destPath string) (int64, error) {
	if err := validateRemoteURL(downloadURL); err != nil {
		return 0, fmt.Errorf("download: %w", err)
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %s: HTTP %d", downloadURL, resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "freeinference-download-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	n, err := io.Copy(tmpFile, resp.Body)
	if err != nil {
		tmpFile.Close()
		return 0, fmt.Errorf("write download: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("rename download: %w", err)
	}
	return n, nil
}

// validateRemoteURL limits installer inputs to HTTPS. Local HTTP is allowed
// only for explicitly opted-in development and test endpoints.
func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return fmt.Errorf("invalid URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && os.Getenv("FI_ALLOW_INSECURE_LOCALHOST") == "1" {
		host := strings.ToLower(u.Hostname())
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
	}
	return fmt.Errorf("URL must use HTTPS (or opted-in loopback HTTP for development)")
}
