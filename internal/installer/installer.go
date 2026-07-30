// Package installer provides the install/upgrade/uninstall flow for the
// FreeInference Companion CLI. It downloads release ZIPs from a marketplace
// manifest, verifies checksums, extracts plugin archives, and places the
// binary on the user's PATH.
package installer

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Options configures an install or update operation.
type Options struct {
	// ManifestURL is the URL of the marketplace.json file.
	ManifestURL string
	// Platform is the platform key to download (e.g. "linux-amd64").
	Platform PlatformKey
	// ExistingVersion is the version currently installed (empty for fresh install).
	ExistingVersion string
	// DryRun reports what would happen without making changes.
	DryRun bool
	// NoBrowser skips opening a browser after installation.
	NoBrowser bool
	// NoPlugin skips plugin extraction.
	NoPlugin bool
	// NoBin skips binary installation.
	NoBin bool
	// Force replaces the binary even if the version matches.
	Force bool
}

// Result reports what was installed or updated.
type Result struct {
	Version       string
	OldVersion    string
	BinaryPath    string
	Plugins       []string // paths to extracted plugin directories
	PathMsg       string   // note about PATH if binary not yet on it
	Updated       bool
	AlreadyLatest bool
}

// Install performs a full installation. It downloads the release ZIP, verifies
// the checksum, extracts the binary and plugins, and places the binary in
// the appropriate location.
func Install(opts Options, stdout, stderr io.Writer) (*Result, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}

	manifest, err := FetchManifest(opts.ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}

	if manifest.IsNewer(opts.ExistingVersion) == false && !opts.Force {
		if opts.ExistingVersion != "" {
			fmt.Fprintf(stdout, "Already at latest version: %s\n", opts.ExistingVersion)
			return &Result{
				Version:       opts.ExistingVersion,
				AlreadyLatest: true,
			}, nil
		}
		// Fresh install: always proceed.
	}

	version := manifest.Version
	fmt.Fprintf(stdout, "Installing FreeInference Companion %s\n", version)
	fmt.Fprintf(stdout, "  Manifest: %s\n", opts.ManifestURL)

	result := &Result{Version: version}

	// Download the release ZIP.
	pi, err := manifest.Platform(opts.Platform)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(stdout, "  Platform: %s\n  URL: %s\n", opts.Platform, pi.URL)
	if opts.DryRun {
		fmt.Fprintln(stdout, "  Dry run: no files will be downloaded or changed.")
		return result, nil
	}

	tmpZip := filepath.Join(os.TempDir(), "freeinference-install-"+version+".zip")
	if _, err := DownloadTo(pi.URL, tmpZip); err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	// Verify checksum.
	data, err := os.ReadFile(tmpZip)
	if err != nil {
		return nil, fmt.Errorf("read zip for checksum: %w", err)
	}
	if err := VerifyChecksum(data, pi.Hash); err != nil {
		os.Remove(tmpZip)
		return nil, fmt.Errorf("checksum: %w", err)
	}

	// Extract the ZIP.
	extractDir, err := os.MkdirTemp("", "freeinference-extract-*")
	if err != nil {
		os.Remove(tmpZip)
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	if err := extractZIP(tmpZip, extractDir); err != nil {
		os.Remove(tmpZip)
		return nil, fmt.Errorf("extract: %w", err)
	}
	os.Remove(tmpZip)

	// Install the binary.
	if !opts.NoBin {
		fmt.Fprintf(stdout, "  Installing binary to %s\n", paths.BinaryPath)
		if err := installBinary(extractDir, paths); err != nil {
			return nil, fmt.Errorf("install binary: %w", err)
		}
		result.BinaryPath = paths.BinaryPath

		inPath, pathMsg := paths.EnsureInPath()
		if !inPath {
			result.PathMsg = pathMsg
		}
	}

	// Extract plugins.
	if !opts.NoPlugin {
		fmt.Fprintf(stdout, "  Extracting plugins...\n")
		if err := extractPlugins(extractDir, paths); err != nil {
			return nil, fmt.Errorf("install plugins: %w", err)
		}
		result.Plugins = extractPluginPaths(paths)
	}

	// Open browser to the release page.
	if !opts.NoBrowser {
		releaseURL := strings.TrimSuffix(opts.ManifestURL, "/marketplace.json")
		if releaseURL == "" {
			releaseURL = "https://github.com/b-a-m-n/freeinference-companion/releases"
		}
		_ = releaseURL
		// Intentionally not opening a browser in non-interactive installs.
	}

	fmt.Fprintf(stdout, "\nInstallation complete.\n")
	if result.PathMsg != "" {
		fmt.Fprintf(stdout, "  %s\n", result.PathMsg)
	}

	return result, nil
}

// Update checks the manifest for a newer version, backs up the current binary,
// and replaces it. Returns a Result describing what changed.
func Update(opts Options, stdout, stderr io.Writer) (*Result, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}

	manifest, err := FetchManifest(opts.ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}

	if !manifest.IsNewer(opts.ExistingVersion) {
		fmt.Fprintf(stdout, "Already at latest version: %s\n", opts.ExistingVersion)
		return &Result{
			Version:       opts.ExistingVersion,
			AlreadyLatest: true,
		}, nil
	}

	fmt.Fprintf(stdout, "Updating FreeInference Companion %s -> %s\n", opts.ExistingVersion, manifest.Version)

	result := &Result{
		Version:    manifest.Version,
		OldVersion: opts.ExistingVersion,
	}
	pi, err := manifest.Platform(opts.Platform)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(stdout, "  Platform: %s\n  URL: %s\n", opts.Platform, pi.URL)
	if opts.DryRun {
		fmt.Fprintln(stdout, "  Dry run: no files will be downloaded or changed.")
		return result, nil
	}

	// Backup current binary.
	if opts.ExistingVersion != "" {
		backupPath := paths.BinaryPath + ".backup-" + opts.ExistingVersion
		if _, err := os.Stat(paths.BinaryPath); err == nil {
			if err := copyFile(paths.BinaryPath, backupPath); err != nil {
				return nil, fmt.Errorf("backup binary: %w", err)
			}
			fmt.Fprintf(stdout, "  Backed up %s -> %s\n", paths.BinaryPath, backupPath)
		}
		result.Updated = true
	}

	// Download and install the new version.
	tmpZip := filepath.Join(os.TempDir(), "freeinference-update-"+manifest.Version+".zip")
	if _, err := DownloadTo(pi.URL, tmpZip); err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	data, err := os.ReadFile(tmpZip)
	if err != nil {
		return nil, fmt.Errorf("read zip for checksum: %w", err)
	}
	if err := VerifyChecksum(data, pi.Hash); err != nil {
		os.Remove(tmpZip)
		return nil, fmt.Errorf("checksum: %w", err)
	}

	extractDir, err := os.MkdirTemp("", "freeinference-extract-*")
	if err != nil {
		os.Remove(tmpZip)
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	if err := extractZIP(tmpZip, extractDir); err != nil {
		os.Remove(tmpZip)
		return nil, fmt.Errorf("extract: %w", err)
	}
	os.Remove(tmpZip)

	if !opts.NoBin {
		if err := installBinary(extractDir, paths); err != nil {
			return nil, fmt.Errorf("install binary: %w", err)
		}
		result.BinaryPath = paths.BinaryPath
	}

	// Also update plugins.
	if !opts.NoPlugin {
		if err := extractPlugins(extractDir, paths); err != nil {
			return nil, fmt.Errorf("update plugins: %w", err)
		}
		result.Plugins = extractPluginPaths(paths)
	}

	fmt.Fprintf(stdout, "Update complete: %s -> %s\n", opts.ExistingVersion, manifest.Version)
	return result, nil
}

// Uninstall removes the installed binary and plugins.
func Uninstall(paths Paths, stdout, stderr io.Writer) error {
	fmt.Fprintf(stdout, "Uninstalling FreeInference Companion...\n")

	// Remove binary.
	if paths.BinaryPath != "" {
		if _, err := os.Stat(paths.BinaryPath); err == nil {
			if err := os.Remove(paths.BinaryPath); err != nil {
				return fmt.Errorf("remove binary: %w", err)
			}
			fmt.Fprintf(stdout, "  Removed %s\n", paths.BinaryPath)
		}
	}

	// Remove plugin directories.
	if paths.ClaudePluginDir != "" {
		pluginPath := filepath.Join(paths.ClaudePluginDir, "freeinference-companion")
		if _, err := os.Stat(pluginPath); err == nil {
			if err := os.RemoveAll(pluginPath); err != nil {
				return fmt.Errorf("remove claude plugin: %w", err)
			}
			fmt.Fprintf(stdout, "  Removed Claude Code plugin: %s\n", pluginPath)
		}
	}
	if paths.CodexPluginDir != "" {
		pluginPath := filepath.Join(paths.CodexPluginDir, "freeinference-companion")
		if _, err := os.Stat(pluginPath); err == nil {
			if err := os.RemoveAll(pluginPath); err != nil {
				return fmt.Errorf("remove codex plugin: %w", err)
			}
			fmt.Fprintf(stdout, "  Removed Codex plugin: %s\n", pluginPath)
		}
	}

	// Remove install dir.
	if paths.InstallDir != "" {
		if _, err := os.Stat(paths.InstallDir); err == nil {
			if err := os.RemoveAll(paths.InstallDir); err != nil {
				return fmt.Errorf("remove install dir: %w", err)
			}
			fmt.Fprintf(stdout, "  Removed %s\n", paths.InstallDir)
		}
	}

	fmt.Fprintf(stdout, "Uninstall complete.\n")
	return nil
}

// installBinary copies the freeinference binary from the extracted ZIP into
// the install directory, then symlinks it into LocalBin.
func installBinary(extractDir string, paths Paths) error {
	// Find the binary in the extracted ZIP. It should be at bin/freeinference
	// or freeinference at the root.
	binarySrc := findBinary(extractDir)
	if binarySrc == "" {
		return fmt.Errorf("binary not found in extracted archive")
	}

	// Create install dir.
	if err := os.MkdirAll(filepath.Dir(paths.BinaryPath), 0755); err != nil {
		return fmt.Errorf("mkdir install dir: %w", err)
	}

	// Copy binary to install dir.
	if err := copyFile(binarySrc, paths.BinaryPath); err != nil {
		return fmt.Errorf("copy binary to install dir: %w", err)
	}
	if err := os.Chmod(paths.BinaryPath, 0755); err != nil {
		return fmt.Errorf("chmod binary: %w", err)
	}

	// Symlink into LocalBin (or copy if symlink fails).
	if err := os.MkdirAll(paths.LocalBin, 0755); err != nil {
		return fmt.Errorf("mkdir local bin: %w", err)
	}
	linkPath := filepath.Join(paths.LocalBin, "freeinference")
	// Remove old symlink/file.
	os.Remove(linkPath)
	// Try symlink first.
	if err := os.Symlink(paths.BinaryPath, linkPath); err != nil {
		// Fall back to copy.
		if err := copyFile(paths.BinaryPath, linkPath); err != nil {
			return fmt.Errorf("create link in %s: %w", paths.LocalBin, err)
		}
	}

	return nil
}

// findBinary looks for the freeinference binary in the extracted directory.
func findBinary(root string) string {
	// Check common locations inside the ZIP.
	candidates := []string{
		filepath.Join("bin", "freeinference"),
		filepath.Join("freeinference"),
		filepath.Join("dist", "freeinference"),
	}
	// Also check platform-specific paths.
	candidates = append(candidates,
		filepath.Join(runtime.GOOS+"-"+runtime.GOARCH, "freeinference"),
		filepath.Join("bin", runtime.GOOS+"-"+runtime.GOARCH, "freeinference"),
	)
	for _, c := range candidates {
		p := filepath.Join(root, c)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// extractPlugins extracts plugin ZIPs from the release archive into the
// appropriate plugin directories for each coding agent.
func extractPlugins(extractDir string, paths Paths) error {
	// Extract Claude Code plugin.
	pluginSrc := filepath.Join(extractDir, "plugins", "claude-code")
	if _, err := os.Stat(pluginSrc); err == nil {
		pluginDest := filepath.Join(paths.ClaudePluginDir, "freeinference-companion")
		if err := os.MkdirAll(filepath.Dir(pluginDest), 0755); err != nil {
			return fmt.Errorf("mkdir claude plugin dir: %w", err)
		}
		if err := removeAllThenCopy(pluginDest, pluginSrc); err != nil {
			return fmt.Errorf("copy claude plugin: %w", err)
		}
	}

	// Extract Codex plugin.
	codexSrc := filepath.Join(extractDir, "plugins", "codex")
	if _, err := os.Stat(codexSrc); err == nil {
		pluginDest := filepath.Join(paths.CodexPluginDir, "freeinference-companion")
		if err := os.MkdirAll(filepath.Dir(pluginDest), 0755); err != nil {
			return fmt.Errorf("mkdir codex plugin dir: %w", err)
		}
		if err := removeAllThenCopy(pluginDest, codexSrc); err != nil {
			return fmt.Errorf("copy codex plugin: %w", err)
		}
	}

	return nil
}

// extractPluginPaths returns the paths of extracted plugin directories.
func extractPluginPaths(paths Paths) []string {
	var plugins []string
	claudePlugin := filepath.Join(paths.ClaudePluginDir, "freeinference-companion")
	codexPlugin := filepath.Join(paths.CodexPluginDir, "freeinference-companion")
	if _, err := os.Stat(claudePlugin); err == nil {
		plugins = append(plugins, claudePlugin)
	}
	if _, err := os.Stat(codexPlugin); err == nil {
		plugins = append(plugins, codexPlugin)
	}
	return plugins
}

// extractZIP extracts a ZIP archive to destDir.
func extractZIP(archive, destDir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		// Prevent path traversal.
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(filepath.Separator)) &&
			target != filepath.Clean(destDir) {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("mkdir parent: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open file in zip: %w", err)
		}

		outFile, err := os.Create(target)
		if err != nil {
			rc.Close()
			return fmt.Errorf("create file: %w", err)
		}

		if _, err := io.Copy(outFile, rc); err != nil {
			outFile.Close()
			rc.Close()
			return fmt.Errorf("write file: %w", err)
		}
		outFile.Close()
		rc.Close()
	}
	return nil
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0755)
}

// removeAllThenCopy removes dst (if it exists) and copies src into it.
func removeAllThenCopy(dst, src string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return copyDir(dst, src)
}

// copyDir recursively copies src directory into dst directory.
func copyDir(dst, src string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0755)
	})
}
