package cli

import (
	"fmt"
	"io"
	"runtime"

	"github.com/b-a-m-n/freeinference-companion/internal/installer"
	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/version"
)

const (
	defaultManifestURL = "https://github.com/b-a-m-n/freeinference-companion/releases/latest/download/marketplace.json"
)

// cmdInstall implements `freeinference install`.
func cmdInstall(paths state.Paths, rest []string, stdout, stderr io.Writer) int {
	opts := installer.Options{
		ManifestURL:     defaultManifestURL,
		Platform:        installer.PlatformKey(runtime.GOOS + "-" + runtime.GOARCH),
		ExistingVersion: version.Version,
	}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--manifest":
			i++
			if i >= len(rest) {
				fmt.Fprintln(stderr, "error: --manifest requires a URL argument")
				return 2
			}
			opts.ManifestURL = rest[i]
		case "--platform":
			i++
			if i >= len(rest) {
				fmt.Fprintln(stderr, "error: --platform requires a platform argument (e.g. linux-amd64)")
				return 2
			}
			opts.Platform = installer.PlatformKey(rest[i])
		case "--dry-run":
			opts.DryRun = true
		case "--no-browser":
			opts.NoBrowser = true
		case "--no-plugin":
			opts.NoPlugin = true
		case "--no-bin":
			opts.NoBin = true
		case "--force":
			opts.Force = true
		case "--help", "-h":
			fmt.Fprint(stdout, helpInstall)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown flag: %s\n", rest[i])
			fmt.Fprintln(stderr, "Run 'freeinference install --help' for usage.")
			return 2
		}
	}

	result, err := installer.Install(opts, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if result.AlreadyLatest {
		return 0
	}
	return 0
}

// cmdUpdate implements `freeinference update`.
func cmdUpdate(paths state.Paths, rest []string, stdout, stderr io.Writer) int {
	opts := installer.Options{
		ManifestURL:     defaultManifestURL,
		Platform:        installer.PlatformKey(runtime.GOOS + "-" + runtime.GOARCH),
		ExistingVersion: version.Version,
	}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--manifest":
			i++
			if i >= len(rest) {
				fmt.Fprintln(stderr, "error: --manifest requires a URL argument")
				return 2
			}
			opts.ManifestURL = rest[i]
		case "--platform":
			i++
			if i >= len(rest) {
				fmt.Fprintln(stderr, "error: --platform requires a platform argument")
				return 2
			}
			opts.Platform = installer.PlatformKey(rest[i])
		case "--dry-run":
			opts.DryRun = true
		case "--no-browser":
			opts.NoBrowser = true
		case "--no-plugin":
			opts.NoPlugin = true
		case "--force":
			opts.Force = true
		case "--help", "-h":
			fmt.Fprint(stdout, helpUpdate)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown flag: %s\n", rest[i])
			fmt.Fprintln(stderr, "Run 'freeinference update --help' for usage.")
			return 2
		}
	}

	result, err := installer.Update(opts, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if result.AlreadyLatest {
		return 0
	}
	return 0
}

const helpInstall = `Usage: freeinference install [--manifest <url>] [--platform <key>] [--dry-run] [--no-browser] [--no-plugin] [--force] [--help]

Download and install the FreeInference Companion CLI binary and plugins.

The installer:
  1. Fetches the latest marketplace manifest
  2. Downloads the release ZIP for the current platform
  3. Verifies the SHA-256 checksum
  4. Extracts the binary to ~/.local/freeinference/bin/
  5. Symlinks to ~/.local/bin/freeinference (or adds to PATH)
  6. Extracts plugins to ~/.claude/plugins/ and ~/.codex/plugins/

Flags:
  --manifest <url>     URL of the marketplace.json file (default: GitHub latest release)
  --platform <key>     Platform override (e.g. linux-amd64, darwin-arm64)
  --dry-run            Show what would be done without making changes
  --no-browser         Skip opening a browser after installation
  --no-plugin          Skip plugin extraction
  --no-bin             Skip binary installation (extract plugins only)
  --force              Force reinstallation even if already at latest version
  --help               Show this help message
`

const helpUpdate = `Usage: freeinference update [--manifest <url>] [--platform <key>] [--dry-run] [--no-browser] [--no-plugin] [--force] [--help]

Check for updates and upgrade the FreeInference Companion installation.

The updater:
  1. Checks the manifest for a newer version
  2. Backs up the current binary before replacing
  3. Downloads and verifies the new release
  4. Replaces the binary and updates plugins

Flags:
  --manifest <url>     URL of the marketplace.json file
  --platform <key>     Platform override (default: current platform)
  --dry-run            Show what would be done without making changes
  --no-browser         Skip opening a browser after update
  --no-plugin          Skip plugin updates
  --force              Force update even if already at latest version
  --help               Show this help message
`
