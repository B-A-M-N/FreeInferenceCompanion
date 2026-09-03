// Package installer provides the install/upgrade/uninstall flow for the
// FreeInference Companion CLI. It downloads release ZIPs from a marketplace
// manifest, verifies checksums, extracts plugin archives, and places the
// binary on the user's PATH.
package installer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

const (
	maxArchiveEntries    = 4096
	maxArchiveTotalBytes = 128 << 20
	maxArchiveFileBytes  = 32 << 20
	maxArchiveNameBytes  = 4096
)

// Options configures an install or update operation.
type Options struct {
	// ManifestURL is the URL of the marketplace.json file.
	ManifestURL string
	// Platform is the platform key to download (e.g. "linux-amd64").
	Platform PlatformKey
	// ExistingVersion is deprecated. Install/update determine the installed
	// version from persistent metadata, never from the invoking executable.
	ExistingVersion string
	// DryRun reports what would happen without making changes.
	DryRun bool
	// NoPlugin skips plugin extraction.
	NoPlugin bool
	// NoBin skips binary installation.
	NoBin bool
	// Force replaces the binary even if the version matches.
	Force bool
}

// Result reports what was installed or updated.
type Result struct {
	Version            string
	OldVersion         string
	BinaryPath         string
	Plugins            []string // paths to extracted plugin directories
	PathMsg            string   // note about PATH if binary not yet on it
	Updated            bool
	AlreadyLatest      bool
	Installed          bool
	PartiallyInstalled bool
	Warnings           []string
	ClaudePluginReady  bool
	CodexFilesReady    bool
	CodexRegistered    bool
	CodexTrusted       bool
}

// Install performs a full installation. It downloads the release ZIP, verifies
// the checksum, extracts the binary and plugins, and places the binary in
// the appropriate location.
//
//lint:ignore U1000 retained as a source-compatibility marker for older callers
func legacyInstall(opts Options, stdout, stderr io.Writer) (*Result, error) {
	return Install(opts, stdout, stderr)
	/*
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
			if err := extractPlugins(extractDir, paths, stdout); err != nil {
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
	*/
}

// Update checks the manifest for a newer version, backs up the current binary,
// and replaces it. Returns a Result describing what changed.
//
//lint:ignore U1000 retained as a source-compatibility marker for older callers
func legacyUpdate(opts Options, stdout, stderr io.Writer) (*Result, error) {
	return Update(opts, stdout, stderr)
	/*
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
			if err := extractPlugins(extractDir, paths, stdout); err != nil {
				return nil, fmt.Errorf("update plugins: %w", err)
			}
			result.Plugins = extractPluginPaths(paths)
		}

		fmt.Fprintf(stdout, "Update complete: %s -> %s\n", opts.ExistingVersion, manifest.Version)
		return result, nil
	*/
}

// Uninstall removes the installed binary and plugins.
//
//lint:ignore U1000 retained as a source-compatibility marker for older callers
func legacyUninstall(paths Paths, stdout, stderr io.Writer) error {
	return Uninstall(paths, stdout, stderr)
	/*
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
		if paths.CodexMarketplaceDir != "" {
			if _, err := os.Stat(paths.CodexMarketplaceDir); err == nil {
				unregisterCodexMarketplace()
				if err := os.RemoveAll(paths.CodexMarketplaceDir); err != nil {
					return fmt.Errorf("remove Codex marketplace: %w", err)
				}
				fmt.Fprintf(stdout, "  Removed Codex marketplace: %s\n", paths.CodexMarketplaceDir)
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
	*/
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
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func validateReleaseLayout(root string, needBinary, needPlugins bool) error {
	if needBinary && findBinary(root) == "" {
		return errors.New("release archive does not contain a regular freeinference binary")
	}
	if !needPlugins {
		return nil
	}
	for _, plugin := range []struct {
		name     string
		rel      string
		manifest string
	}{
		{name: "Claude", rel: "plugins/claude-code", manifest: ".claude-plugin/plugin.json"},
		{name: "Codex", rel: "plugins/codex", manifest: ".codex-plugin/plugin.json"},
	} {
		base := filepath.Join(root, plugin.rel)
		for _, required := range []string{plugin.manifest, "hooks/hooks.json", "scripts/run-hook.sh"} {
			path := filepath.Join(base, required)
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("release archive is missing %s plugin file %s", plugin.name, required)
			}
			if required == "scripts/run-hook.sh" && info.Mode()&0111 == 0 {
				return fmt.Errorf("release archive %s plugin runner is not executable", plugin.name)
			}
		}
	}
	return nil
}

// registerCodexMarketplace is retained for package-local compatibility tests.
// Production installation uses commitRelease, which stages all owned paths in
// one transaction before calling the native Codex plugin manager.
func registerCodexMarketplace(paths Paths, pluginSrc string, stdout io.Writer) error {
	if paths.CodexMarketplaceDir == "" {
		return nil
	}
	if err := validateOwnedDirectory(paths.CodexMarketplaceDir, nil, false, ""); err != nil {
		return err
	}
	stage, err := stageCodexMarketplace(paths, pluginSrc)
	if err != nil {
		return err
	}
	tx := &installTransaction{}
	if err := tx.replace(paths.CodexMarketplaceDir, stage); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	if err := tx.finalize(); err != nil {
		return err
	}
	_, warnings := registerCodexMarketplaceStatus(paths, stdout)
	for _, warning := range warnings {
		if stdout != nil {
			fmt.Fprintf(stdout, "  Warning: %s\n", warning)
		}
	}
	return nil
}

func runCodexPluginCommand(codex string, args ...string) error {
	cmd := exec.Command(codex, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
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

	if len(r.File) > maxArchiveEntries {
		return fmt.Errorf("archive contains too many entries")
	}
	seen := make(map[string]struct{}, len(r.File))
	var total uint64
	for _, f := range r.File {
		target, err := safeArchiveTarget(destDir, f.Name)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.Clean(target))
		if _, exists := seen[key]; exists {
			return fmt.Errorf("archive contains duplicate output path")
		}
		seen[key] = struct{}{}
		if f.Flags&1 != 0 {
			return fmt.Errorf("archive contains an encrypted entry")
		}
		if len(f.Name) > maxArchiveNameBytes || !utf8.ValidString(f.Name) {
			return fmt.Errorf("archive entry name is invalid")
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 || mode&os.ModeDevice != 0 || mode&os.ModeCharDevice != 0 {
			return fmt.Errorf("archive contains an unsupported file type")
		}
		if f.UncompressedSize64 > maxArchiveFileBytes {
			return fmt.Errorf("archive entry exceeds the individual size limit")
		}
		if f.CompressedSize64 > maxArchiveTotalBytes {
			return fmt.Errorf("archive entry exceeds the compressed size limit")
		}
		total += f.UncompressedSize64
		if total > maxArchiveTotalBytes {
			return fmt.Errorf("archive exceeds the expanded size limit")
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

		written, err := io.Copy(outFile, io.LimitReader(rc, maxArchiveFileBytes+1))
		if err != nil {
			outFile.Close()
			rc.Close()
			return fmt.Errorf("write file: %w", err)
		}
		if written > maxArchiveFileBytes {
			outFile.Close()
			rc.Close()
			return fmt.Errorf("archive entry exceeds the individual size limit")
		}
		if err := outFile.Close(); err != nil {
			rc.Close()
			return fmt.Errorf("close extracted file: %w", err)
		}
		rc.Close()
		mode = f.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		if err := os.Chmod(target, mode); err != nil {
			return fmt.Errorf("set extracted file mode: %w", err)
		}
	}
	return nil
}

func safeArchiveTarget(destDir, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') || !utf8.ValidString(name) {
		return "", errors.New("archive entry name is invalid")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || (len(name) >= 2 && name[1] == ':') {
		return "", errors.New("archive contains an absolute path")
	}
	name = strings.ReplaceAll(name, "\\", "/")
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." || filepath.IsAbs(cleanName) {
		return "", errors.New("archive contains an invalid path")
	}
	root := filepath.Clean(destDir)
	target := filepath.Join(root, cleanName)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("archive path escapes extraction directory")
	}
	return target, nil
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", src)
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	in, readErr := io.ReadAll(io.LimitReader(f, maxArchiveFileBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(in) > maxArchiveFileBytes {
		return fmt.Errorf("source file exceeds the supported size limit")
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0644
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := out.Write(in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
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
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported plugin file type: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		return os.WriteFile(dstPath, data, mode)
	})
}
