package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

// Install performs a full installation. The installed version is read from
// persistent ownership metadata, not from the executable invoking the command.
func Install(opts Options, stdout, stderr io.Writer) (*Result, error) {
	return installOrUpdate(opts, stdout, stderr, false)
}

// Update performs a metadata-driven update. --force reinstalls an equal
// release but never downgrades an existing installation.
func Update(opts Options, stdout, stderr io.Writer) (*Result, error) {
	return installOrUpdate(opts, stdout, stderr, true)
}

func installOrUpdate(opts Options, stdout, stderr io.Writer, update bool) (*Result, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}
	if opts.NoBin && opts.NoPlugin {
		return nil, errors.New("cannot install with both --no-bin and --no-plugin")
	}
	metadata, metadataFound, metadataErr := LoadInstallationMetadata(paths.MetadataPath())
	if metadataErr != nil && !opts.Force {
		return nil, fmt.Errorf("read installation metadata: %w (use --force to repair)", metadataErr)
	}
	if metadataFound {
		if err := validateMetadataPaths(metadata, paths); err != nil {
			return nil, err
		}
	}
	installedVersion := ""
	if metadataFound && managedBinaryExists(metadata, paths) {
		installedVersion = metadata.InstalledVersion
	}
	manifest, err := FetchManifest(opts.ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	if installedVersion != "" {
		comparison := compareVersions(manifest.Version, installedVersion)
		if comparison < 0 || (comparison == 0 && !opts.Force) {
			fmt.Fprintf(stdout, "Already at latest installed version: %s\n", installedVersion)
			return &Result{Version: installedVersion, OldVersion: installedVersion, AlreadyLatest: true}, nil
		}
	}

	result := &Result{Version: manifest.Version, OldVersion: installedVersion}
	if update && installedVersion != "" {
		fmt.Fprintf(stdout, "Updating FreeInference Companion %s -> %s\n", installedVersion, manifest.Version)
	} else {
		fmt.Fprintf(stdout, "Installing FreeInference Companion %s\n", manifest.Version)
	}
	fmt.Fprintf(stdout, "  Manifest: %s\n", opts.ManifestURL)
	pi, err := manifest.Platform(opts.Platform)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(stdout, "  Platform: %s\n  URL: %s\n", opts.Platform, pi.URL)
	if opts.DryRun {
		fmt.Fprintln(stdout, "  Dry run: no files will be downloaded or changed.")
		return result, nil
	}
	tmpZip, err := uniqueTempFile(".freeinference-release-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create download temp: %w", err)
	}
	defer os.Remove(tmpZip)
	if _, err := DownloadTo(pi.URL, tmpZip); err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	data, err := os.ReadFile(tmpZip)
	if err != nil {
		return nil, fmt.Errorf("read zip for checksum: %w", err)
	}
	if err := VerifyChecksum(data, pi.Hash); err != nil {
		return nil, fmt.Errorf("checksum: %w", err)
	}
	extractDir, err := os.MkdirTemp("", "freeinference-extract-*")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(extractDir)
	if err := extractZIP(tmpZip, extractDir); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if err := validateReleaseLayout(extractDir, !opts.NoBin, !opts.NoPlugin); err != nil {
		return nil, fmt.Errorf("validate release: %w", err)
	}
	if err := withInstallerLock(paths, func() error {
		latest, found, err := LoadInstallationMetadata(paths.MetadataPath())
		if err != nil && !opts.Force {
			return fmt.Errorf("read installation metadata: %w", err)
		}
		if found {
			if err := validateMetadataPaths(latest, paths); err != nil {
				return err
			}
			if managedBinaryExists(latest, paths) {
				cmp := compareVersions(manifest.Version, latest.InstalledVersion)
				if cmp < 0 || (cmp == 0 && !opts.Force) {
					result.Version = latest.InstalledVersion
					result.OldVersion = latest.InstalledVersion
					result.AlreadyLatest = true
					return nil
				}
			}
		}
		return commitRelease(extractDir, paths, opts, manifest, pi.Hash, result, stdout, latest, found)
	}); err != nil {
		return nil, err
	}
	if result.AlreadyLatest {
		fmt.Fprintf(stdout, "Already at latest installed version: %s\n", result.Version)
		return result, nil
	}
	result.Installed = true
	result.Updated = update && result.OldVersion != ""
	if result.Updated {
		fmt.Fprintf(stdout, "Update complete: %s -> %s\n", result.OldVersion, result.Version)
	} else {
		fmt.Fprintln(stdout, "Installation complete.")
	}
	if result.PartiallyInstalled {
		fmt.Fprintln(stdout, "Core installation complete with warnings; review the lines above.")
	}
	if result.PathMsg != "" {
		fmt.Fprintf(stdout, "  %s\n", result.PathMsg)
	}
	return result, nil
}

func uniqueTempFile(pattern string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func compareVersions(left, right string) int {
	a, b := parseVersion(left), parseVersion(right)
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch < b.patch {
		return -1
	}
	if a.patch > b.patch {
		return 1
	}
	return 0
}

func managedBinaryExists(metadata *InstallationMetadata, paths Paths) bool {
	if metadata == nil || !metadata.ManagedBinaryOwned || metadata.ManagedBinaryPath != paths.BinaryPath {
		return false
	}
	info, err := os.Lstat(paths.BinaryPath)
	return err == nil && info.Mode().IsRegular()
}

func validateMetadataPaths(metadata *InstallationMetadata, paths Paths) error {
	if metadata == nil {
		return errors.New("installation metadata is empty")
	}
	for label, pathPair := range map[string][2]string{
		"managed binary":    {metadata.ManagedBinaryPath, paths.BinaryPath},
		"shim":              {metadata.ShimPath, paths.shimPath()},
		"Claude plugin":     {metadata.ClaudePluginPath, paths.claudePluginPath()},
		"Codex plugin":      {metadata.CodexPluginPath, paths.codexPluginPath()},
		"Codex marketplace": {metadata.CodexMarketplacePath, paths.CodexMarketplaceDir},
	} {
		recorded, err := canonicalPath(pathPair[0])
		if err != nil {
			return fmt.Errorf("installation metadata has invalid %s path", label)
		}
		expected, err := canonicalPath(pathPair[1])
		if err != nil || recorded != expected {
			return fmt.Errorf("installation metadata belongs to a different %s path", label)
		}
	}
	return nil
}

func commitRelease(extractDir string, paths Paths, opts Options, manifest *MarketplaceManifest, artifactHash string, result *Result, stdout io.Writer, prior *InstallationMetadata, priorFound bool) error {
	tx := &installTransaction{}
	failed := func(err error) error { tx.rollback(); return err }
	if !opts.NoBin {
		binarySrc := findBinary(extractDir)
		if err := validateBinaryOwnership(paths, prior, priorFound); err != nil {
			return failed(err)
		}
		staged, err := stageFile(binarySrc, paths.BinaryPath)
		if err != nil {
			return failed(fmt.Errorf("stage binary: %w", err))
		}
		if err := os.Chmod(staged, 0755); err != nil {
			return failed(err)
		}
		if err := tx.replace(paths.BinaryPath, staged); err != nil {
			return failed(fmt.Errorf("install binary: %w", err))
		}
		if err := validateShimOwnership(paths, prior, priorFound); err != nil {
			return failed(err)
		}
		shim, err := stageShim(paths.shimPath(), paths.BinaryPath)
		if err != nil {
			return failed(fmt.Errorf("stage shim: %w", err))
		}
		if err := tx.replaceAllowSymlink(paths.shimPath(), shim); err != nil {
			return failed(fmt.Errorf("install shim: %w", err))
		}
		result.BinaryPath = paths.BinaryPath
		if inPath, msg := paths.EnsureInPath(); !inPath {
			result.PathMsg = msg
		}
	}
	if !opts.NoPlugin {
		claudeSrc := filepath.Join(extractDir, "plugins", "claude-code")
		codexSrc := filepath.Join(extractDir, "plugins", "codex")
		if err := validateOwnedDirectory(paths.claudePluginPath(), prior, priorFound, priorDigest(prior, true)); err != nil {
			return failed(err)
		}
		if err := validateOwnedDirectory(paths.codexPluginPath(), prior, priorFound, priorDigest(prior, false)); err != nil {
			return failed(err)
		}
		if err := validateOwnedDirectory(paths.CodexMarketplaceDir, prior, priorFound, priorMarketplaceDigest(prior)); err != nil {
			return failed(err)
		}
		claudeStage, err := stageDirectory(claudeSrc, paths.claudePluginPath())
		if err != nil {
			return failed(fmt.Errorf("stage Claude plugin: %w", err))
		}
		if err := tx.replace(paths.claudePluginPath(), claudeStage); err != nil {
			return failed(fmt.Errorf("install Claude plugin: %w", err))
		}
		codexStage, err := stageDirectory(codexSrc, paths.codexPluginPath())
		if err != nil {
			return failed(fmt.Errorf("stage Codex plugin: %w", err))
		}
		if err := tx.replace(paths.codexPluginPath(), codexStage); err != nil {
			return failed(fmt.Errorf("install Codex plugin: %w", err))
		}
		marketplaceStage, err := stageCodexMarketplace(paths, codexSrc)
		if err != nil {
			return failed(fmt.Errorf("stage Codex marketplace: %w", err))
		}
		if err := tx.replace(paths.CodexMarketplaceDir, marketplaceStage); err != nil {
			return failed(fmt.Errorf("install Codex marketplace: %w", err))
		}
		result.ClaudePluginReady = true
		result.CodexFilesReady = true
		result.Plugins = extractPluginPaths(paths)
	}
	metadata := metadataForPaths(paths, manifest.Version, manifestURLOrigin(opts.ManifestURL), artifactHash, version.Version)
	metadata.ManagedBinaryOwned = !opts.NoBin
	metadata.ShimOwned = !opts.NoBin
	metadata.ClaudePluginOwned = !opts.NoPlugin
	metadata.CodexPluginOwned = !opts.NoPlugin
	metadata.CodexMarketplaceOwned = !opts.NoPlugin
	metadata.ManagedBinarySHA256, _ = pathDigest(paths.BinaryPath)
	metadata.ClaudePluginSHA256, _ = pathDigest(paths.claudePluginPath())
	metadata.CodexPluginSHA256, _ = pathDigest(paths.codexPluginPath())
	metadata.CodexMarketplaceSHA256, _ = pathDigest(paths.CodexMarketplaceDir)
	metadataStage, err := stageInstallationMetadata(paths.MetadataPath(), metadata)
	if err != nil {
		return failed(fmt.Errorf("stage installation metadata: %w", err))
	}
	if err := tx.replace(paths.MetadataPath(), metadataStage); err != nil {
		_ = os.Remove(metadataStage)
		return failed(fmt.Errorf("commit installation metadata: %w", err))
	}
	if err := tx.finalize(); err != nil {
		return fmt.Errorf("clean installation rollback files: %w", err)
	}
	if result.CodexFilesReady {
		registered, warnings := registerCodexMarketplaceStatus(paths, stdout)
		result.CodexRegistered = registered
		result.Warnings = append(result.Warnings, warnings...)
		if len(warnings) > 0 {
			result.PartiallyInstalled = true
			for _, warning := range warnings {
				fmt.Fprintf(stdout, "  Warning: %s\n", warning)
			}
		}
	}
	return nil
}

func priorDigest(metadata *InstallationMetadata, claude bool) string {
	if metadata == nil {
		return ""
	}
	if claude {
		return metadata.ClaudePluginSHA256
	}
	return metadata.CodexPluginSHA256
}

func priorMarketplaceDigest(metadata *InstallationMetadata) string {
	if metadata == nil {
		return ""
	}
	return metadata.CodexMarketplaceSHA256
}

func manifestURLOrigin(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Scheme + "://" + u.Host
	}
	return raw
}

func validateOwnedDirectory(path string, metadata *InstallationMetadata, found bool, expectedDigest string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !found || !ownsDirectory(metadata, path) {
		return fmt.Errorf("refusing to replace unowned plugin path %s", path)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("owned plugin path is not a directory: %s", path)
	}
	if expectedDigest != "" {
		actual, err := pathDigest(path)
		if err != nil || actual != expectedDigest {
			return fmt.Errorf("plugin path changed after installation: %s", path)
		}
	}
	return nil
}

func ownsDirectory(metadata *InstallationMetadata, path string) bool {
	if metadata == nil {
		return false
	}
	switch path {
	case metadata.ClaudePluginPath:
		return metadata.ClaudePluginOwned
	case metadata.CodexPluginPath:
		return metadata.CodexPluginOwned
	case metadata.CodexMarketplacePath:
		return metadata.CodexMarketplaceOwned
	default:
		return false
	}
}

func validateBinaryOwnership(paths Paths, metadata *InstallationMetadata, found bool) error {
	info, err := os.Lstat(paths.BinaryPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !found || metadata == nil || !metadata.ManagedBinaryOwned {
		return fmt.Errorf("refusing to replace unowned binary %s", paths.BinaryPath)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("owned binary is not a regular file: %s", paths.BinaryPath)
	}
	if metadata.ManagedBinarySHA256 == "" {
		return fmt.Errorf("owned binary has no recorded checksum: %s", paths.BinaryPath)
	}
	digest, err := pathDigest(paths.BinaryPath)
	if err != nil || digest != metadata.ManagedBinarySHA256 {
		return fmt.Errorf("binary changed after installation: %s", paths.BinaryPath)
	}
	return nil
}

func validateShimOwnership(paths Paths, metadata *InstallationMetadata, found bool) error {
	path := paths.shimPath()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !found || metadata == nil || !metadata.ShimOwned {
		return fmt.Errorf("refusing to replace unowned shim %s", path)
	}
	recorded, _ := canonicalPath(metadata.ShimPath)
	expectedPath, _ := canonicalPath(path)
	if recorded == "" || recorded != expectedPath {
		return errors.New("installation metadata shim path does not match the requested installation")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("inspect existing shim: %w", err)
		}
		expected, _ := filepath.Abs(paths.BinaryPath)
		actual, _ := filepath.Abs(target)
		if actual != expected {
			return fmt.Errorf("refusing to replace a foreign shim %s", path)
		}
		return nil
	}
	if !info.Mode().IsRegular() || metadata.ManagedBinarySHA256 == "" {
		return fmt.Errorf("refusing to replace a foreign shim %s", path)
	}
	digest, err := pathDigest(path)
	if err != nil || digest != metadata.ManagedBinarySHA256 {
		return fmt.Errorf("refusing to replace a foreign shim %s", path)
	}
	return nil
}

func stageShim(shimPath, binaryPath string) (string, error) {
	staged, err := newSiblingPath(filepath.Dir(shimPath), ".freeinference-shim-*")
	if err != nil {
		return "", err
	}
	if err := os.Symlink(binaryPath, staged); err == nil {
		return staged, nil
	}
	if err := copyFile(binaryPath, staged); err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	if err := os.Chmod(staged, 0755); err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	return staged, nil
}

func stageCodexMarketplace(paths Paths, pluginSource string) (string, error) {
	stage, err := os.MkdirTemp(filepath.Dir(paths.CodexMarketplaceDir), ".freeinference-marketplace-*")
	if err != nil {
		return "", err
	}
	cleanup := func(err error) (string, error) {
		_ = os.RemoveAll(stage)
		return "", err
	}
	pluginDest := filepath.Join(stage, "plugins", "freeinference-companion")
	if err := copyDir(pluginDest, pluginSource); err != nil {
		return cleanup(err)
	}
	marketplace := map[string]any{
		"name":      "freeinference-companion-local",
		"interface": map[string]string{"displayName": "FreeInference Companion"},
		"plugins": []any{map[string]any{
			"name":     "freeinference-companion",
			"source":   map[string]string{"source": "local", "path": "./plugins/freeinference-companion"},
			"policy":   map[string]string{"installation": "AVAILABLE", "authentication": "ON_INSTALL"},
			"category": "Developer Tools",
		}},
	}
	data, err := json.MarshalIndent(marketplace, "", "  ")
	if err != nil {
		return cleanup(err)
	}
	marketplacePath := filepath.Join(stage, ".agents", "plugins", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(marketplacePath), 0700); err != nil {
		return cleanup(err)
	}
	if err := os.WriteFile(marketplacePath, append(data, '\n'), 0600); err != nil {
		return cleanup(err)
	}
	return stage, nil
}

// UninstallResult distinguishes successful local removal from native Codex
// cleanup that still needs user attention.
type UninstallResult struct {
	Removed  []string
	Warnings []string
}

// Uninstall removes only installer-owned files. Runtime configuration, client
// configuration, cache, and historical diagnostic state are preserved.
func Uninstall(paths Paths, stdout, stderr io.Writer) error {
	result, err := UninstallWithResult(paths, stdout, stderr)
	if err == nil && result != nil && stdout != nil {
		if len(result.Removed) == 0 {
			fmt.Fprintln(stdout, "FreeInference Companion application files were already absent.")
		} else {
			fmt.Fprintln(stdout, "Application removed.")
		}
		fmt.Fprintln(stdout, "Configuration and local diagnostic history were preserved.")
		for _, warning := range result.Warnings {
			fmt.Fprintf(stdout, "Warning: %s\n", warning)
		}
	}
	return err
}

func UninstallWithResult(paths Paths, stdout, stderr io.Writer) (*UninstallResult, error) {
	if paths.MetadataPath() == "" {
		return nil, errors.New("installer metadata path is unavailable")
	}
	result := &UninstallResult{}
	err := withInstallerLock(paths, func() error {
		metadata, found, err := LoadInstallationMetadata(paths.MetadataPath())
		if err != nil {
			return fmt.Errorf("read installation metadata: %w", err)
		}
		if !found {
			if !anyOwnedTargetExists(paths) {
				if stdout != nil {
					fmt.Fprintln(stdout, "FreeInference Companion is not installed by this installer.")
				}
				return nil
			}
			return errors.New("installation metadata is missing; refusing to remove unowned files")
		}
		if err := validateMetadataPaths(metadata, paths); err != nil {
			return err
		}
		if metadata.ManagedBinaryOwned {
			if err := validateOwnedFile(paths.BinaryPath, metadata.ManagedBinarySHA256, true); err != nil {
				return err
			}
		}
		if metadata.ShimOwned {
			if err := validateOwnedShimForRemoval(paths, metadata); err != nil {
				return err
			}
		}
		if metadata.ClaudePluginOwned {
			if err := validateOwnedDirectoryForRemoval(paths.claudePluginPath(), metadata.ClaudePluginSHA256); err != nil {
				return err
			}
		}
		if metadata.CodexPluginOwned {
			if err := validateOwnedDirectoryForRemoval(paths.codexPluginPath(), metadata.CodexPluginSHA256); err != nil {
				return err
			}
		}
		if metadata.CodexMarketplaceOwned {
			if err := validateOwnedDirectoryForRemoval(paths.CodexMarketplaceDir, metadata.CodexMarketplaceSHA256); err != nil {
				return err
			}
			result.Warnings = append(result.Warnings, unregisterCodexMarketplaceStatus()...)
		}

		tx := &installTransaction{}
		failed := func(err error) error { tx.rollback(); return err }
		for _, target := range []struct {
			path, label string
			owned       bool
		}{
			{paths.shimPath(), "shim", metadata.ShimOwned},
			{paths.BinaryPath, "binary", metadata.ManagedBinaryOwned},
			{paths.claudePluginPath(), "Claude plugin", metadata.ClaudePluginOwned},
			{paths.codexPluginPath(), "Codex plugin", metadata.CodexPluginOwned},
			{paths.CodexMarketplaceDir, "Codex marketplace", metadata.CodexMarketplaceOwned},
		} {
			if !target.owned {
				continue
			}
			existed := pathExists(target.path)
			remove := tx.remove
			if target.label == "shim" {
				remove = tx.removeAllowSymlink
			}
			if err := remove(target.path); err != nil {
				return failed(fmt.Errorf("remove %s: %w", target.label, err))
			}
			if existed {
				result.Removed = append(result.Removed, target.path)
			}
		}
		if err := tx.remove(paths.MetadataPath()); err != nil {
			return failed(fmt.Errorf("remove installation metadata: %w", err))
		}
		if err := tx.finalize(); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(paths.BinaryPath))
		_ = os.Remove(paths.InstallDir)
		return nil
	})
	return result, err
}

func anyOwnedTargetExists(paths Paths) bool {
	for _, path := range []string{paths.BinaryPath, paths.shimPath(), paths.claudePluginPath(), paths.codexPluginPath(), paths.CodexMarketplaceDir} {
		if pathExists(path) {
			return true
		}
	}
	return false
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func validateOwnedFile(path, expectedDigest string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && allowMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("owned file is not a regular file: %s", path)
	}
	if expectedDigest != "" {
		actual, err := pathDigest(path)
		if err != nil || actual != expectedDigest {
			return fmt.Errorf("owned file changed after installation: %s", path)
		}
	}
	return nil
}

func validateOwnedShimForRemoval(paths Paths, metadata *InstallationMetadata) error {
	path := paths.shimPath()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		actual, _ := filepath.Abs(target)
		expected, _ := filepath.Abs(paths.BinaryPath)
		if actual != expected {
			return fmt.Errorf("refusing to remove foreign shim %s", path)
		}
		return nil
	}
	return validateOwnedFile(path, metadata.ManagedBinarySHA256, false)
}

func validateOwnedDirectoryForRemoval(path, expectedDigest string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("owned directory is not a directory: %s", path)
	}
	if expectedDigest != "" {
		actual, err := pathDigest(path)
		if err != nil || actual != expectedDigest {
			return fmt.Errorf("owned directory changed after installation: %s", path)
		}
	}
	return nil
}

func unregisterCodexMarketplaceStatus() []string {
	codex, err := exec.LookPath("codex")
	if err != nil {
		return []string{"Codex CLI was not found; native plugin registration may remain"}
	}
	var warnings []string
	if err := runCodexPluginCommand(codex, "plugin", "remove", "freeinference-companion@freeinference-companion-local", "--json"); err != nil {
		warnings = append(warnings, "Codex plugin registration cleanup did not complete")
	}
	if err := runCodexPluginCommand(codex, "plugin", "marketplace", "remove", "freeinference-companion-local", "--json"); err != nil {
		warnings = append(warnings, "Codex marketplace registration cleanup did not complete")
	}
	return warnings
}

func registerCodexMarketplaceStatus(paths Paths, stdout io.Writer) (bool, []string) {
	codex, err := exec.LookPath("codex")
	if err != nil {
		return false, []string{"Codex plugin files installed; Codex CLI was not found for native registration"}
	}
	if err := runCodexPluginCommand(codex, "plugin", "marketplace", "add", paths.CodexMarketplaceDir, "--json"); err != nil {
		return false, []string{"Codex marketplace registration did not complete; run `codex plugin marketplace add` manually"}
	}
	if err := runCodexPluginCommand(codex, "plugin", "add", "freeinference-companion@freeinference-companion-local", "--json"); err != nil {
		return false, []string{"Codex plugin installation did not complete; run `codex plugin add freeinference-companion@freeinference-companion-local` manually"}
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "  Registered and installed the Codex plugin through its local marketplace.")
	}
	return true, nil
}
