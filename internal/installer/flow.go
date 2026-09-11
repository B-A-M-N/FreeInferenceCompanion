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

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
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
	if metadataErr != nil {
		if !opts.Force {
			return nil, fmt.Errorf("read installation metadata: %w (use --force to repair)", metadataErr)
		}
		// Force can repair corrupt metadata only when every target that would be
		// replaced is absent or independently proven owned below. Never treat a
		// corrupt record as proof that an existing file belongs to us.
		metadata = nil
		metadataFound = false
		if stderr != nil {
			fmt.Fprintf(stderr, "warning: installation metadata is unusable; force repair will refuse unowned existing targets: %v\n", metadataErr)
		}
	}
	if metadataFound {
		paths, err = validateAndNormalizeMetadataPaths(metadata, paths)
		if err != nil {
			return nil, err
		}
		paths = pathsForRecordedCodex(paths, metadata)
	} else {
		paths, err = paths.normalizeLegacyPaths()
		if err != nil {
			return nil, err
		}
	}
	installedVersion := installedComponentsVersion(metadata, metadataFound, paths, opts)
	manifest, err := FetchManifest(opts.ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	coreCurrent := false
	if installedVersion != "" {
		comparison := compareVersions(manifest.Version, installedVersion)
		if comparison < 0 {
			fmt.Fprintf(stdout, "Refusing downgrade: installed version %s is newer than %s\n", installedVersion, manifest.Version)
			return &Result{Version: installedVersion, OldVersion: installedVersion, AlreadyLatest: true}, nil
		}
		coreCurrent = comparison == 0 && !opts.Force && requestedComponentsReady(metadata, paths, manifest.Version, opts)
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
	if coreCurrent {
		result := &Result{Version: installedVersion, OldVersion: installedVersion, AlreadyLatest: true}
		integrationErr := withInstallerLock(paths, func() error {
			if !opts.NoPlugin {
				if err := reconcileCanonicalCodexRegistration(paths, result, stdout); err != nil {
					return err
				}
			}
			return reconcileClientEnvironmentsFromCore(paths, opts, installedVersion, result, stdout)
		})
		if integrationErr != nil {
			return nil, integrationErr
		}
		fmt.Fprintf(stdout, "Already at latest installed version: %s\n", installedVersion)
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
	if err := validateReleaseLayout(extractDir, !opts.NoBin, !opts.NoPlugin, opts.Platform); err != nil {
		return nil, fmt.Errorf("validate release: %w", err)
	}
	if err := withInstallerLock(paths, func() error {
		latest, found, err := LoadInstallationMetadata(paths.MetadataPath())
		if err != nil && !opts.Force {
			return fmt.Errorf("read installation metadata: %w", err)
		}
		if found {
			paths, err = validateAndNormalizeMetadataPaths(latest, paths)
			if err != nil {
				return err
			}
			paths = pathsForRecordedCodex(paths, latest)
			if latestVersion := installedComponentsVersion(latest, found, paths, opts); latestVersion != "" {
				cmp := compareVersions(manifest.Version, latestVersion)
				if cmp == 0 && !opts.Force && requestedComponentsReady(latest, paths, manifest.Version, opts) {
					result.Version = latestVersion
					result.OldVersion = latestVersion
					result.AlreadyLatest = true
					if !opts.NoPlugin {
						if err := reconcileCanonicalCodexRegistration(paths, result, stdout); err != nil {
							return err
						}
					}
					return reconcileClientEnvironmentsFromCore(paths, opts, latestVersion, result, stdout)
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
	if !opts.NoPlugin {
		integrationErr := withInstallerLock(paths, func() error {
			return reconcileClientEnvironmentsFromCore(paths, opts, manifest.Version, result, stdout)
		})
		if integrationErr != nil {
			return nil, integrationErr
		}
	}
	result.Installed = true
	result.Updated = update && result.OldVersion != ""
	if result.Updated {
		fmt.Fprintf(stdout, "Update complete: %s -> %s\n", result.OldVersion, result.Version)
	} else if result.PartiallyInstalled {
		fmt.Fprintln(stdout, "Core installation complete with warnings; review the lines above.")
	} else {
		fmt.Fprintln(stdout, "Installation complete.")
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

// installedComponentsVersion returns the highest version recorded for every
// component this operation may replace. Readiness is checked separately:
// retaining a recorded version here prevents a missing or modified component
// from silently bypassing the no-downgrade guard.
func installedComponentsVersion(metadata *InstallationMetadata, found bool, paths Paths, opts Options) string {
	if !found || metadata == nil {
		return ""
	}
	versions := make([]string, 0, 2)
	if !opts.NoBin {
		versions = append(versions, metadata.BinaryVersion)
	}
	if !opts.NoPlugin {
		versions = append(versions, metadata.ClaudePluginVersion)
		versions = append(versions, metadata.CodexPluginVersion, metadata.CodexMarketplaceVersion)
	}
	return highestVersion(versions)
}

func highestVersion(versions []string) string {
	highest := ""
	for _, candidate := range versions {
		if candidate != "" && (highest == "" || compareVersions(candidate, highest) > 0) {
			highest = candidate
		}
	}
	return highest
}

func metadataVersion(metadata *InstallationMetadata, component string) string {
	if metadata == nil {
		return ""
	}
	switch component {
	case "binary":
		return metadata.BinaryVersion
	case "claude":
		return metadata.ClaudePluginVersion
	case "codex":
		return metadata.CodexPluginVersion
	case "marketplace":
		return metadata.CodexMarketplaceVersion
	default:
		return ""
	}
}

func componentPathReady(metadata *InstallationMetadata, componentVersion, expectedPath string, binary bool) bool {
	if metadata == nil || componentVersion == "" {
		return false
	}
	var owned bool
	var recordedPath, recordedDigest string
	if binary {
		owned = metadata.ManagedBinaryOwned
		recordedPath, recordedDigest = metadata.ManagedBinaryPath, metadata.ManagedBinarySHA256
	} else {
		return false
	}
	if !owned || recordedPath != expectedPath || recordedDigest == "" {
		return false
	}
	info, err := os.Lstat(expectedPath)
	if err != nil || (binary && !info.Mode().IsRegular()) {
		return false
	}
	matched, err := pathDigestMatches(expectedPath, recordedDigest)
	return err == nil && matched
}

func directoryComponentReady(metadata *InstallationMetadata, component, expectedPath string) bool {
	if metadata == nil {
		return false
	}
	var owned bool
	var recordedPath, recordedDigest string
	switch component {
	case "claude":
		owned, recordedPath, recordedDigest = metadata.ClaudePluginOwned, metadata.ClaudePluginPath, metadata.ClaudePluginSHA256
	case "codex":
		owned, recordedPath, recordedDigest = metadata.CodexPluginOwned, metadata.CodexPluginPath, metadata.CodexPluginSHA256
	case "marketplace":
		owned, recordedPath, recordedDigest = metadata.CodexMarketplaceOwned, metadata.CodexMarketplacePath, metadata.CodexMarketplaceSHA256
	default:
		return false
	}
	if !owned || recordedPath != expectedPath || recordedDigest == "" || metadataVersion(metadata, component) == "" {
		return false
	}
	info, err := os.Lstat(expectedPath)
	if err != nil || !info.IsDir() {
		return false
	}
	matched, err := pathDigestMatches(expectedPath, recordedDigest)
	return err == nil && matched
}

func requestedComponentsReady(metadata *InstallationMetadata, paths Paths, version string, opts Options) bool {
	if metadata == nil {
		return false
	}
	if !opts.NoBin && (!componentPathReady(metadata, metadata.BinaryVersion, paths.BinaryPath, true) || metadata.BinaryVersion != version) {
		return false
	}
	if !opts.NoPlugin {
		if metadata.ClaudePluginVersion != version {
			return false
		}
		if !directoryComponentReady(metadata, "claude", paths.claudePluginPath()) {
			return false
		}
		if !coreDirectoryComponentReady(metadata.CoreClaudePluginPath, metadata.CoreClaudePluginSHA256, paths.CoreClaudePluginPath) {
			return false
		}
		codexTracked := metadata.CodexPluginOwned || metadata.CodexPluginVersion != "" || metadata.CoreCodexPluginPath != ""
		if codexTracked && (metadata.CodexPluginVersion != version || metadata.CodexMarketplaceVersion != version ||
			!directoryComponentReady(metadata, "codex", paths.codexPluginPath()) ||
			!directoryComponentReady(metadata, "marketplace", paths.CodexMarketplaceDir) ||
			!coreDirectoryComponentReady(metadata.CoreCodexPluginPath, metadata.CoreCodexPluginSHA256, paths.CoreCodexPluginPath) ||
			!metadata.CodexNativeRegistrationKnown) {
			return false
		}
	}
	return true
}

func validateMetadataPaths(metadata *InstallationMetadata, paths Paths) error {
	_, err := validateAndNormalizeMetadataPaths(metadata, paths)
	return err
}

func validateAndNormalizeMetadataPaths(metadata *InstallationMetadata, paths Paths) (Paths, error) {
	if metadata == nil {
		return Paths{}, errors.New("installation metadata is empty")
	}
	normalizedPaths, normalizeErr := paths.normalizeLegacyPaths()
	if normalizeErr != nil {
		return Paths{}, normalizeErr
	}
	codexExpected := normalizedPaths.codexPluginPath()
	marketplaceExpected := normalizedPaths.CodexMarketplaceDir
	canonicalPaths, canonicalErr := PathsForHome(normalizedPaths.Home)
	legacyPathsExplicit := canonicalErr == nil && canonical(normalizedPaths.codexPluginPath()) != canonical(canonicalPaths.codexPluginPath())
	if !legacyPathsExplicit {
		if legacyPlugin, legacyMarketplace, ok := recordedLegacyCodexPaths(metadata); ok {
			// Older installers used CODEX_HOME for core ownership. The recorded pair
			// is the migration source; new Paths still target canonical ~/.codex.
			codexExpected = legacyPlugin
			marketplaceExpected = legacyMarketplace
		}
	}
	for label, pathPair := range map[string][2]string{
		"managed binary":    {metadata.ManagedBinaryPath, normalizedPaths.BinaryPath},
		"shim":              {metadata.ShimPath, normalizedPaths.shimPath()},
		"Claude plugin":     {metadata.ClaudePluginPath, normalizedPaths.claudePluginPath()},
		"Codex plugin":      {metadata.CodexPluginPath, codexExpected},
		"Codex marketplace": {metadata.CodexMarketplacePath, marketplaceExpected},
	} {
		recorded, err := canonicalPath(pathPair[0])
		if err != nil {
			return Paths{}, fmt.Errorf("installation metadata has invalid %s path", label)
		}
		expected, err := canonicalPath(pathPair[1])
		if err != nil || recorded != expected {
			return Paths{}, fmt.Errorf("installation metadata belongs to a different %s path", label)
		}
	}
	for label, pathPair := range map[string][2]string{
		"core Claude plugin": {metadata.CoreClaudePluginPath, normalizedPaths.CoreClaudePluginPath},
		"core Codex plugin":  {metadata.CoreCodexPluginPath, normalizedPaths.CoreCodexPluginPath},
	} {
		if pathPair[0] == "" {
			continue
		}
		recorded, err := canonicalPath(pathPair[0])
		if err != nil {
			return Paths{}, fmt.Errorf("installation metadata has invalid %s path", label)
		}
		expected, err := canonicalPath(pathPair[1])
		if err != nil || recorded != expected {
			return Paths{}, fmt.Errorf("installation metadata belongs to a different %s path", label)
		}
	}
	return normalizedPaths, nil
}

func coreDirectoryComponentReady(recordedPath, recordedDigest, expectedPath string) bool {
	if recordedPath == "" || recordedDigest == "" || recordedPath != expectedPath {
		return false
	}
	info, err := os.Lstat(expectedPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	matched, err := pathDigestMatches(expectedPath, recordedDigest)
	return err == nil && matched
}

func recordedLegacyCodexPaths(metadata *InstallationMetadata) (string, string, bool) {
	if metadata == nil {
		return "", "", false
	}
	plugin, pluginErr := canonicalPath(metadata.CodexPluginPath)
	marketplace, marketplaceErr := canonicalPath(metadata.CodexMarketplacePath)
	if pluginErr != nil || marketplaceErr != nil {
		return "", "", false
	}
	root := filepath.Dir(filepath.Dir(plugin))
	if plugin != filepath.Join(root, "plugins", "freeinference-companion") || marketplace != filepath.Join(root, "plugins", "freeinference-companion-marketplace") {
		return "", "", false
	}
	return plugin, marketplace, true
}

func pathsForRecordedCodex(paths Paths, metadata *InstallationMetadata) Paths {
	plugin, marketplace, ok := recordedLegacyCodexPaths(metadata)
	if !ok {
		return paths
	}
	canonical, err := canonicalPath(paths.codexPluginPath())
	if err == nil && canonical == plugin {
		return paths
	}
	paths.CodexHome = filepath.Dir(filepath.Dir(plugin))
	paths.CodexPluginDir = filepath.Dir(plugin)
	paths.CodexPluginPath = plugin
	paths.CodexMarketplaceDir = marketplace
	return paths
}

func commitRelease(extractDir string, paths Paths, opts Options, manifest *MarketplaceManifest, artifactHash string, result *Result, stdout io.Writer, prior *InstallationMetadata, priorFound bool) error {
	tx := &installTransaction{}
	nativeRegistrationAttempted := false
	appendNativeWarnings := func(warnings []string) {
		for _, warning := range warnings {
			result.PartiallyInstalled = true
			result.Warnings = append(result.Warnings, warning)
			if stdout != nil {
				fmt.Fprintf(stdout, "  Warning: %s\n", warning)
			}
		}
	}
	rollbackNative := func() {
		if !nativeRegistrationAttempted {
			return
		}
		appendNativeWarnings(unregisterCodexMarketplaceStatus(paths))
		if prior == nil {
			return
		}
		appendNativeWarnings(restoreCodexNativeRegistrationForMetadata(paths, prior))
	}
	defer tx.rollback()
	failed := func(err error) error { rollbackNative(); tx.rollback(); return err }
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
	codexAvailable := false
	if !opts.NoPlugin {
		claudeSrc := filepath.Join(extractDir, "plugins", "claude-code")
		codexSrc := filepath.Join(extractDir, "plugins", "codex")
		if info, sourceErr := os.Lstat(codexSrc); sourceErr == nil {
			codexAvailable = info.IsDir()
		} else if !os.IsNotExist(sourceErr) {
			return failed(fmt.Errorf("inspect Codex plugin source: %w", sourceErr))
		}
		if err := validateOwnedDirectory(paths.claudePluginPath(), prior, priorFound, priorDigest(prior, true)); err != nil {
			return failed(err)
		}
		claudeStage, err := stageDirectory(claudeSrc, paths.claudePluginPath())
		if err != nil {
			return failed(fmt.Errorf("stage Claude plugin: %w", err))
		}
		if err := tx.replaceStaged(paths.claudePluginPath(), claudeStage, false); err != nil {
			return failed(fmt.Errorf("install Claude plugin: %w", err))
		}
		priorCoreClaudePath, priorCoreClaudeDigest := priorCoreEvidence(prior)
		if err := validateCoreDirectory(paths.CoreClaudePluginPath, prior, priorFound, priorCoreClaudePath, priorCoreClaudeDigest); err != nil {
			return failed(err)
		}
		claudeCoreStage, err := stageDirectory(claudeSrc, paths.CoreClaudePluginPath)
		if err != nil {
			return failed(fmt.Errorf("stage core Claude plugin: %w", err))
		}
		if err := tx.replaceStaged(paths.CoreClaudePluginPath, claudeCoreStage, false); err != nil {
			return failed(fmt.Errorf("install core Claude plugin: %w", err))
		}
		priorCoreCodexPath, priorCoreCodexDigest := priorCodexCoreEvidence(prior)
		if codexAvailable {
			if err := validateCoreDirectory(paths.CoreCodexPluginPath, prior, priorFound, priorCoreCodexPath, priorCoreCodexDigest); err != nil {
				return failed(err)
			}
			codexCoreStage, err := stageDirectory(codexSrc, paths.CoreCodexPluginPath)
			if err != nil {
				return failed(fmt.Errorf("stage core Codex plugin: %w", err))
			}
			if err := tx.replaceStaged(paths.CoreCodexPluginPath, codexCoreStage, false); err != nil {
				return failed(fmt.Errorf("install core Codex plugin: %w", err))
			}
			if err := validateOwnedDirectory(paths.codexPluginPath(), prior, priorFound, priorDigest(prior, false)); err != nil {
				return failed(err)
			}
			codexPluginStage, err := stageDirectory(codexSrc, paths.codexPluginPath())
			if err != nil {
				return failed(fmt.Errorf("stage canonical Codex plugin: %w", err))
			}
			if err := tx.replaceStaged(paths.codexPluginPath(), codexPluginStage, false); err != nil {
				return failed(fmt.Errorf("install canonical Codex plugin: %w", err))
			}
			if err := validateOwnedDirectory(paths.CodexMarketplaceDir, prior, priorFound, priorMarketplaceDigest(prior)); err != nil {
				return failed(err)
			}
			marketplaceStage, err := stageCodexMarketplace(paths, codexSrc)
			if err != nil {
				return failed(fmt.Errorf("stage canonical Codex marketplace: %w", err))
			}
			if err := tx.replace(paths.CodexMarketplaceDir, marketplaceStage); err != nil {
				return failed(fmt.Errorf("install canonical Codex marketplace: %w", err))
			}
		}
		result.ClaudePluginReady = true
		result.Plugins = extractPluginPaths(paths)
	}
	metadata := metadataForPaths(paths, manifest.Version, manifestURLOrigin(opts.ManifestURL), artifactHash, version.Version)
	metadata.ManagedBinaryOwned = !opts.NoBin
	metadata.ShimOwned = !opts.NoBin
	metadata.ClaudePluginOwned = !opts.NoPlugin
	metadata.ManagedBinarySHA256, _ = pathDigest(paths.BinaryPath)
	metadata.ClaudePluginSHA256, _ = pathDigest(paths.claudePluginPath())
	if !opts.NoPlugin {
		metadata.CoreClaudePluginPath = paths.CoreClaudePluginPath
		metadata.CoreClaudePluginSHA256, _ = pathDigest(paths.CoreClaudePluginPath)
		if codexAvailable {
			metadata.CodexPluginOwned = true
			metadata.CodexPluginSHA256, _ = pathDigest(paths.codexPluginPath())
			metadata.CodexPluginVersion = manifest.Version
			metadata.CodexMarketplaceOwned = true
			metadata.CodexMarketplaceSHA256, _ = pathDigest(paths.CodexMarketplaceDir)
			metadata.CodexMarketplaceVersion = manifest.Version
			metadata.CoreCodexPluginPath = paths.CoreCodexPluginPath
			metadata.CoreCodexPluginSHA256, _ = pathDigest(paths.CoreCodexPluginPath)
		}
	}
	if priorFound && prior != nil {
		if opts.NoBin {
			metadata.ManagedBinaryOwned = prior.ManagedBinaryOwned
			metadata.ManagedBinarySHA256 = prior.ManagedBinarySHA256
			metadata.BinaryVersion = prior.BinaryVersion
			metadata.ShimOwned = prior.ShimOwned
		}
		if opts.NoPlugin {
			metadata.ClaudePluginOwned = prior.ClaudePluginOwned
			metadata.ClaudePluginSHA256 = prior.ClaudePluginSHA256
			metadata.ClaudePluginVersion = prior.ClaudePluginVersion
			metadata.CodexPluginOwned = prior.CodexPluginOwned
			metadata.CodexPluginSHA256 = prior.CodexPluginSHA256
			metadata.CodexPluginVersion = prior.CodexPluginVersion
			metadata.CodexMarketplaceOwned = prior.CodexMarketplaceOwned
			metadata.CodexMarketplaceSHA256 = prior.CodexMarketplaceSHA256
			metadata.CodexMarketplaceVersion = prior.CodexMarketplaceVersion
			metadata.CoreClaudePluginPath = prior.CoreClaudePluginPath
			metadata.CoreClaudePluginSHA256 = prior.CoreClaudePluginSHA256
			metadata.CoreCodexPluginPath = prior.CoreCodexPluginPath
			metadata.CoreCodexPluginSHA256 = prior.CoreCodexPluginSHA256
		}
		if !opts.NoPlugin && !codexAvailable {
			metadata.CodexPluginOwned = prior.CodexPluginOwned
			metadata.CodexPluginSHA256 = prior.CodexPluginSHA256
			metadata.CodexPluginVersion = prior.CodexPluginVersion
			metadata.CodexMarketplaceOwned = prior.CodexMarketplaceOwned
			metadata.CodexMarketplaceSHA256 = prior.CodexMarketplaceSHA256
			metadata.CodexMarketplaceVersion = prior.CodexMarketplaceVersion
			metadata.CoreCodexPluginPath = prior.CoreCodexPluginPath
			metadata.CoreCodexPluginSHA256 = prior.CoreCodexPluginSHA256
			metadata.CodexNativeRegistrationKnown = prior.CodexNativeRegistrationKnown
			metadata.CodexMarketplaceAdded = prior.CodexMarketplaceAdded
			metadata.CodexPluginRegistered = prior.CodexPluginRegistered
		}
		if opts.NoPlugin {
			metadata.CodexNativeRegistrationKnown = prior.CodexNativeRegistrationKnown
			metadata.CodexMarketplaceAdded = prior.CodexMarketplaceAdded
			metadata.CodexPluginRegistered = prior.CodexPluginRegistered
		}
	}
	if codexAvailable {
		nativeRegistrationAttempted = true
		registered, marketplaceAdded, warnings := registerCodexMarketplaceForHome(paths.CodexHome, paths.CodexMarketplaceDir, stdout)
		metadata.CodexNativeRegistrationKnown = true
		metadata.CodexMarketplaceAdded = marketplaceAdded
		metadata.CodexPluginRegistered = registered
		result.CodexPluginInstalled = true
		result.CodexMarketplaceInstalled = true
		result.CodexMarketplaceAdded = marketplaceAdded
		result.CodexPluginRegistered = registered
		result.CodexNativeStatusKnown = true
		appendNativeWarnings(warnings)
		if stdout != nil {
			fmt.Fprintf(stdout, "  Codex payload: plugin installed=%t, marketplace installed=%t\n", result.CodexPluginInstalled, result.CodexMarketplaceInstalled)
			fmt.Fprintf(stdout, "  Codex native registration: marketplace added=%t, plugin registered=%t\n", marketplaceAdded, registered)
		}
	}
	metadata.InstalledVersion = highestVersion([]string{
		metadata.BinaryVersion,
		metadata.ClaudePluginVersion,
		metadata.CodexPluginVersion,
	})
	// Metadata is the ownership commit point for every target recorded by this
	// transaction. Until it commits, deferred rollback restores all filesystem
	// replacements. After it commits, rollback is disallowed by transaction
	// state and only backup cleanup can produce a recoverable warning.
	metadataStage, err := stageInstallationMetadata(paths.MetadataPath(), metadata)
	if err != nil {
		return failed(fmt.Errorf("stage installation metadata: %w", err))
	}
	if err := tx.replace(paths.MetadataPath(), metadataStage); err != nil {
		_ = os.Remove(metadataStage)
		return failed(fmt.Errorf("commit installation metadata: %w", err))
	}
	tx.cleanupCommitted = true
	if err := tx.finalize(); err != nil {
		result.PartiallyInstalled = true
		warning := fmt.Sprintf("cleanup of installation rollback files failed: %v", err)
		result.Warnings = append(result.Warnings, warning)
		if stdout != nil {
			fmt.Fprintf(stdout, "  Warning: %s\n", warning)
		}
	}
	return nil
}

func reconcileCanonicalCodexRegistration(paths Paths, result *Result, stdout io.Writer) error {
	metadata, found, err := LoadInstallationMetadata(paths.MetadataPath())
	if err != nil {
		return fmt.Errorf("read Codex registration status: %w", err)
	}
	if !found || metadata == nil || !metadata.CodexMarketplaceOwned {
		return nil
	}
	result.CodexPluginInstalled = metadata.CodexPluginOwned
	result.CodexMarketplaceInstalled = metadata.CodexMarketplaceOwned
	result.CodexMarketplaceAdded = metadata.CodexMarketplaceAdded
	result.CodexPluginRegistered = metadata.CodexPluginRegistered
	result.CodexNativeStatusKnown = metadata.CodexNativeRegistrationKnown
	if metadata.CodexNativeRegistrationKnown && metadata.CodexPluginRegistered {
		return nil
	}

	registered, marketplaceAdded, warnings := registerCodexMarketplaceForHome(paths.CodexHome, paths.CodexMarketplaceDir, stdout)
	appendWarnings := func(items []string) {
		for _, warning := range items {
			result.PartiallyInstalled = true
			result.Warnings = append(result.Warnings, warning)
			if stdout != nil {
				fmt.Fprintf(stdout, "  Warning: %s\n", warning)
			}
		}
	}
	appendWarnings(warnings)
	previousMarketplaceAdded, previousPluginRegistered := metadata.CodexMarketplaceAdded, metadata.CodexPluginRegistered
	metadata.CodexNativeRegistrationKnown = true
	metadata.CodexMarketplaceAdded = marketplaceAdded
	metadata.CodexPluginRegistered = registered
	result.CodexMarketplaceAdded = marketplaceAdded
	result.CodexPluginRegistered = registered
	result.CodexNativeStatusKnown = true

	metadataStage, err := stageInstallationMetadata(paths.MetadataPath(), *metadata)
	if err != nil {
		appendWarnings(unregisterCodexMarketplaceStatus(paths))
		appendWarnings(restoreCodexNativeRegistration(paths, previousMarketplaceAdded, previousPluginRegistered))
		return fmt.Errorf("stage Codex registration status: %w", err)
	}
	tx := &installTransaction{}
	defer tx.rollback()
	if err := tx.replace(paths.MetadataPath(), metadataStage); err != nil {
		_ = os.Remove(metadataStage)
		appendWarnings(unregisterCodexMarketplaceStatus(paths))
		appendWarnings(restoreCodexNativeRegistration(paths, previousMarketplaceAdded, previousPluginRegistered))
		return fmt.Errorf("commit Codex registration status: %w", err)
	}
	if err := tx.finalize(); err != nil {
		warning := fmt.Sprintf("cleanup of Codex registration status rollback files failed: %v", err)
		appendWarnings([]string{warning})
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

func validateCoreDirectory(path string, metadata *InstallationMetadata, found bool, recordedPath, recordedDigest string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !found || metadata == nil || recordedPath != path || recordedDigest == "" {
		return fmt.Errorf("refusing to replace unowned core plugin path %s", path)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("owned core plugin path is not a directory: %s", path)
	}
	matched, err := pathDigestMatches(path, recordedDigest)
	if err != nil || !matched {
		return fmt.Errorf("core plugin path changed after installation: %s", path)
	}
	return nil
}

func priorCoreEvidence(metadata *InstallationMetadata) (string, string) {
	if metadata == nil {
		return "", ""
	}
	return metadata.CoreClaudePluginPath, metadata.CoreClaudePluginSHA256
}

func priorCodexCoreEvidence(metadata *InstallationMetadata) (string, string) {
	if metadata == nil {
		return "", ""
	}
	return metadata.CoreCodexPluginPath, metadata.CoreCodexPluginSHA256
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
		matched, err := pathDigestMatches(path, expectedDigest)
		if err != nil || !matched {
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
	matched, err := pathDigestMatches(paths.BinaryPath, metadata.ManagedBinarySHA256)
	if err != nil || !matched {
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
		expected, _ := canonicalMutationPath(paths.BinaryPath)
		actual, _ := canonicalMutationPath(target)
		if actual != expected {
			return fmt.Errorf("refusing to replace a foreign shim %s", path)
		}
		return nil
	}
	if !info.Mode().IsRegular() || metadata.ManagedBinarySHA256 == "" {
		return fmt.Errorf("refusing to replace a foreign shim %s", path)
	}
	matched, err := pathDigestMatches(path, metadata.ManagedBinarySHA256)
	if err != nil || !matched {
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
	if err := ensureNoSymlinkedAncestors(paths.CodexMarketplaceDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(paths.CodexMarketplaceDir), 0755); err != nil {
		return "", err
	}
	if err := ensureNoSymlinkedAncestors(paths.CodexMarketplaceDir); err != nil {
		return "", err
	}
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
	normalized, normalizeErr := paths.normalizeLegacyPaths()
	if normalizeErr == nil {
		paths = normalized
	}
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
		var normalizeErr error
		paths, normalizeErr = validateAndNormalizeMetadataPaths(metadata, paths)
		if normalizeErr != nil {
			return normalizeErr
		}
		paths = pathsForRecordedCodex(paths, metadata)
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
		}
		tx := &installTransaction{}
		nativeCleaned := false
		nativeCleanupCommitted := false
		if metadata.CodexMarketplaceOwned && nativeCodexRegistrationNeedsCleanup(metadata) {
			nativeWarnings := unregisterCodexMarketplaceStatus(paths)
			if len(nativeWarnings) > 0 && metadata.CodexNativeRegistrationKnown {
				result.Warnings = append(result.Warnings, nativeWarnings...)
				return fmt.Errorf("native Codex cleanup failed; local files were preserved: %s", nativeWarnings[0])
			}
			result.Warnings = append(result.Warnings, nativeWarnings...)
			nativeCleaned = len(nativeWarnings) == 0
		}
		failed := func(err error) error {
			tx.rollback()
			if nativeCleaned && !nativeCleanupCommitted {
				result.Warnings = append(result.Warnings, restoreCodexNativeRegistrationForMetadata(paths, metadata)...)
			}
			return err
		}
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
		// All owned targets and the ownership document have been moved into the
		// transaction's committed state. A later rollback-file cleanup error must
		// not restore native registration to paths that are now intentionally gone.
		nativeCleanupCommitted = true
		if err := tx.finalize(); err != nil {
			return failed(fmt.Errorf("cleanup of uninstall rollback files: %w", err))
		}
		_ = os.Remove(filepath.Dir(paths.BinaryPath))
		_ = os.Remove(paths.InstallDir)
		// Alternate cleanup happens only after the core transaction has committed;
		// a core failure above must not strand or forget alternate ownership.
		alternateRemoved, alternateWarnings := uninstallClientEnvironmentsLocked(paths.home(), stdout)
		result.Removed = append(result.Removed, alternateRemoved...)
		result.Warnings = append(result.Warnings, alternateWarnings...)
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
		matched, err := pathDigestMatches(path, expectedDigest)
		if err != nil || !matched {
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
		actual, _ := canonicalMutationPath(target)
		expected, _ := canonicalMutationPath(paths.BinaryPath)
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
		matched, err := pathDigestMatches(path, expectedDigest)
		if err != nil || !matched {
			return fmt.Errorf("owned directory changed after installation: %s", path)
		}
	}
	return nil
}

func unregisterCodexMarketplaceStatus(paths Paths) []string {
	codex, err := exec.LookPath("codex")
	if err != nil {
		return []string{"Codex CLI was not found; native plugin registration may remain"}
	}
	var warnings []string
	if err := runCodexPluginCommandForHome(codex, paths.CodexHome, "plugin", "remove", "freeinference-companion@freeinference-companion-local", "--json"); err != nil {
		warnings = append(warnings, "Codex plugin registration cleanup did not complete")
	}
	if err := runCodexPluginCommandForHome(codex, paths.CodexHome, "plugin", "marketplace", "remove", "freeinference-companion-local", "--json"); err != nil {
		warnings = append(warnings, "Codex marketplace registration cleanup did not complete")
	}
	return warnings
}

func nativeCodexRegistrationNeedsCleanup(metadata *InstallationMetadata) bool {
	if metadata == nil || !metadata.CodexMarketplaceOwned {
		return false
	}
	// Older metadata did not record native state. Make a best effort to remove
	// both names in that case; current metadata can avoid touching Codex when
	// installation already proved that native registration was absent.
	return !metadata.CodexNativeRegistrationKnown || metadata.CodexMarketplaceAdded || metadata.CodexPluginRegistered
}

func restoreCodexNativeRegistration(paths Paths, marketplaceAdded, pluginRegistered bool) []string {
	if !marketplaceAdded && !pluginRegistered {
		return nil
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		return []string{"Codex CLI was not found; prior native plugin registration could not be restored"}
	}
	var warnings []string
	if marketplaceAdded {
		if err := runCodexPluginCommandForHome(codex, paths.CodexHome, "plugin", "marketplace", "add", paths.CodexMarketplaceDir, "--json"); err != nil {
			warnings = append(warnings, "prior Codex marketplace registration could not be restored")
			return warnings
		}
	}
	if pluginRegistered {
		if err := runCodexPluginCommandForHome(codex, paths.CodexHome, "plugin", "add", "freeinference-companion@freeinference-companion-local", "--json"); err != nil {
			warnings = append(warnings, "prior Codex plugin registration could not be restored")
		}
	}
	return warnings
}

func restoreCodexNativeRegistrationForMetadata(paths Paths, metadata *InstallationMetadata) []string {
	if metadata == nil {
		return nil
	}
	if !metadata.CodexNativeRegistrationKnown {
		// Legacy metadata cannot tell us whether native registration existed;
		// restore both names after a failed local transaction to preserve the
		// most likely pre-existing state.
		return restoreCodexNativeRegistration(paths, true, true)
	}
	return restoreCodexNativeRegistration(paths, metadata.CodexMarketplaceAdded, metadata.CodexPluginRegistered)
}

func registerCodexMarketplaceStatus(paths Paths, stdout io.Writer) (bool, []string) {
	registered, _, warnings := registerCodexMarketplaceForHome(paths.CodexHome, paths.CodexMarketplaceDir, stdout)
	return registered, warnings
}

func registerCodexMarketplaceForHome(codexHome, marketplacePath string, stdout io.Writer) (bool, bool, []string) {
	codex, err := exec.LookPath("codex")
	if err != nil {
		return false, false, []string{"Codex plugin files installed; Codex CLI was not found for native registration"}
	}
	if err := runCodexPluginCommandForHome(codex, codexHome, "plugin", "marketplace", "add", marketplacePath, "--json"); err != nil {
		return false, false, []string{"Codex marketplace registration did not complete; run `codex plugin marketplace add` manually"}
	}
	if err := runCodexPluginCommandForHome(codex, codexHome, "plugin", "add", "freeinference-companion@freeinference-companion-local", "--json"); err != nil {
		return false, true, []string{"Codex plugin installation did not complete; run `codex plugin add freeinference-companion@freeinference-companion-local` manually"}
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "  Registered and installed the Codex plugin through its local marketplace.")
	}
	return true, true, nil
}

func reconcileClientEnvironmentsFromCore(paths Paths, opts Options, version string, result *Result, stdout io.Writer) error {
	if opts.NoPlugin {
		return nil
	}
	metadata, found, metadataErr := LoadInstallationMetadata(paths.MetadataPath())
	if metadataErr != nil || !found || metadata == nil {
		return fmt.Errorf("verified core installation metadata is unavailable")
	}
	if !coreDirectoryComponentReady(metadata.CoreClaudePluginPath, metadata.CoreClaudePluginSHA256, paths.CoreClaudePluginPath) {
		return fmt.Errorf("verified core plugin ownership evidence is missing or changed")
	}
	pluginSources := map[clientenv.Client]string{
		clientenv.ClientClaudeCode: paths.CoreClaudePluginPath,
	}
	if metadata.CoreCodexPluginPath != "" {
		if !coreDirectoryComponentReady(metadata.CoreCodexPluginPath, metadata.CoreCodexPluginSHA256, paths.CoreCodexPluginPath) {
			return fmt.Errorf("verified core Codex plugin ownership evidence is missing or changed")
		}
		pluginSources[clientenv.ClientCodex] = paths.CoreCodexPluginPath
	}
	integrations, err := ReconcileClientEnvironments(reconcileOptions{
		home:          paths.home(),
		pluginSources: pluginSources,
		version:       version,
		dryRun:        opts.DryRun,
		discovery:     !opts.NoIntegrationDiscovery,
		stdout:        stdout,
	})
	if err != nil {
		warning := fmt.Sprintf("reconcile client environments: %v", err)
		result.PartiallyInstalled = true
		result.Warnings = append(result.Warnings, warning)
		if stdout != nil {
			fmt.Fprintf(stdout, "  Warning: %s\n", warning)
		}
		return nil
	}
	for _, integration := range integrations {
		if integration.Action == "installed" || integration.Action == "planned" {
			result.IntegrationsChanged = true
		}
		if integration.Warning != "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s environment %s: %s", integration.Client, integration.ConfigRoot, integration.Warning))
		}
	}
	result.EnvironmentIntegrations = integrations
	return nil
}
