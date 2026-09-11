package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
)

// Paths holds the filesystem locations used by the installer.
type Paths struct {
	// InstallDir is the root for the installation (default: ~/.local/freeinference).
	InstallDir string
	// BinaryPath is the path to the freeinference binary.
	BinaryPath string
	// LocalBin is the ~/.local/bin directory (or $HOME/.local/bin).
	LocalBin string
	// ClaudePluginDir is the directory where the Claude Code plugin ZIP is extracted.
	ClaudePluginDir string
	// CodexPluginDir is the canonical Codex plugin parent directory. It is also
	// retained when reading legacy ownership records.
	CodexPluginDir string
	// CodexHome is the canonical Codex configuration root targeted by core install.
	CodexHome string
	// ClaudeHome is the canonical Claude configuration root targeted by core install.
	ClaudeHome string
	// CodexMarketplaceDir is the local marketplace root used to register the
	// bundled Codex plugin with Codex's native plugin manager.
	CodexMarketplaceDir string
	// ShimPath is the PATH-facing executable owned by the installer.
	ShimPath string
	// ClaudePluginPath is the installed Companion Claude plugin directory.
	ClaudePluginPath string
	// CodexPluginPath is the installed Companion Codex plugin directory.
	CodexPluginPath string
	// CoreClaudePluginPath is the installer-owned verified Claude artifact.
	CoreClaudePluginPath string
	// CoreCodexPluginPath is the installer-owned verified skill-only Codex artifact.
	CoreCodexPluginPath string
	// Home records the home directory that produced these canonical paths.
	Home         string
	metadataPath string
}

// DefaultPaths returns Paths using standard locations.
func DefaultPaths() (Paths, error) {
	return PathsForHome(homeDir())
}

// PathsForHome resolves deterministic core paths for an explicit home.
func PathsForHome(home string) (Paths, error) {
	if home == "" {
		return Paths{}, fmt.Errorf("home dir: HOME unset")
	}
	localBin := filepath.Join(home, ".local", "bin")
	installDir := filepath.Join(home, ".local", "freeinference")
	// Core ownership is deterministic. CODEX_HOME remains a runtime selector;
	// installer fan-out treats it as an additional environment below.
	codexHome := clientenv.CanonicalRoot(home, clientenv.ClientCodex)
	claudeHome := clientenv.CanonicalRoot(home, clientenv.ClientClaudeCode)

	claudePluginDir := filepath.Join(claudeHome, "plugins")
	claudePluginPath := filepath.Join(claudePluginDir, "freeinference-companion")
	codexPluginPath := filepath.Join(codexHome, "plugins", "freeinference-companion")
	return Paths{
		InstallDir:           installDir,
		CodexHome:            codexHome,
		ClaudeHome:           claudeHome,
		BinaryPath:           filepath.Join(installDir, "bin", "freeinference"),
		LocalBin:             localBin,
		ClaudePluginDir:      claudePluginDir,
		CodexPluginDir:       filepath.Join(codexHome, "plugins"),
		CodexMarketplaceDir:  filepath.Join(codexHome, "plugins", "freeinference-companion-marketplace"),
		CoreClaudePluginPath: filepath.Join(installDir, "plugins", "claude-code"),
		CoreCodexPluginPath:  filepath.Join(installDir, "plugins", "codex"),
		ShimPath:             filepath.Join(localBin, "freeinference"),
		ClaudePluginPath:     claudePluginPath,
		CodexPluginPath:      codexPluginPath,
		Home:                 home,
		metadataPath:         installationMetadataPath(home),
	}, nil
}

// homeDir returns the current user's home directory. Tests can override via the
// homeDirFunc variable to use a temporary directory.
var homeDirFunc func() string

func init() {
	homeDirFunc = func() string { return os.Getenv("HOME") }
}

func homeDir() string {
	h := homeDirFunc()
	if h != "" {
		return h
	}
	// Fallback for production when HOME is not set.
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// EnsureInPath checks whether p.LocalBin is on PATH and, if not, reports how
// to add it. It does NOT modify any shell config files (that would require
// interactive input).
func (p Paths) EnsureInPath() (inPath bool, msg string) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		if filepath.Clean(dir) == filepath.Clean(p.LocalBin) {
			return true, ""
		}
	}
	// Check if the user's default shell config files exist and contain local/bin.
	shellConfigFiles := []string{
		filepath.Join(os.Getenv("HOME"), ".bashrc"),
		filepath.Join(os.Getenv("HOME"), ".bash_profile"),
		filepath.Join(os.Getenv("HOME"), ".zshrc"),
		filepath.Join(os.Getenv("HOME"), ".profile"),
	}
	for _, f := range shellConfigFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), p.LocalBin) {
			return true, "exists in shell config but not on current PATH"
		}
	}
	msg = fmt.Sprintf("add %s to your PATH (e.g. export PATH=%s:\"$PATH\" in ~/.bashrc)",
		p.LocalBin, p.LocalBin)
	return false, msg
}

// PathIsOnPath checks whether the given directory is currently on PATH.
func PathIsOnPath(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// home returns the home directory represented by canonical installer paths.
func (p Paths) home() string {
	if p.Home != "" {
		return p.Home
	}
	if p.ClaudeHome != "" {
		return filepath.Dir(p.ClaudeHome)
	}
	if p.CodexHome != "" {
		return filepath.Dir(p.CodexHome)
	}
	if p.InstallDir != "" {
		return filepath.Dir(filepath.Dir(p.InstallDir))
	}
	return ""
}

// normalizeLegacyPaths accepts Paths values from older callers and ownership
// records created before Codex core ownership became canonical ~/.codex. It
// derives Home from trusted installation roots and repairs partial Paths
// values without weakening path containment for new writes.
func (p Paths) normalizeLegacyPaths() (Paths, error) {
	if strings.TrimSpace(p.Home) == "" {
		home := ""
		if strings.TrimSpace(p.ClaudeHome) != "" {
			home = filepath.Dir(p.ClaudeHome)
		} else if strings.TrimSpace(p.CodexHome) != "" {
			home = filepath.Dir(p.CodexHome)
		} else if strings.TrimSpace(p.InstallDir) != "" {
			home = filepath.Dir(filepath.Dir(p.InstallDir))
		}
		if strings.TrimSpace(home) == "" {
			return Paths{}, errors.New("installer home path is unavailable")
		}
		p.Home = home
	}
	canonical, err := PathsForHome(p.Home)
	if err != nil {
		return Paths{}, err
	}
	if strings.TrimSpace(p.ClaudeHome) == "" {
		p.ClaudeHome = canonical.ClaudeHome
	}
	if strings.TrimSpace(p.CodexHome) == "" {
		p.CodexHome = canonical.CodexHome
	}
	if strings.TrimSpace(p.InstallDir) == "" {
		p.InstallDir = canonical.InstallDir
	}
	if strings.TrimSpace(p.BinaryPath) == "" {
		p.BinaryPath = canonical.BinaryPath
	}
	if strings.TrimSpace(p.LocalBin) == "" {
		p.LocalBin = canonical.LocalBin
	}
	if strings.TrimSpace(p.ClaudePluginDir) == "" {
		p.ClaudePluginDir = canonical.ClaudePluginDir
	}
	if strings.TrimSpace(p.CodexPluginDir) == "" {
		// Legacy records may have pointed this directory at CODEX_HOME/plugins.
		// Preserve an explicit legacy value; otherwise use the canonical path.
		p.CodexPluginDir = canonical.CodexPluginDir
	}
	if strings.TrimSpace(p.CodexMarketplaceDir) == "" {
		p.CodexMarketplaceDir = canonical.CodexMarketplaceDir
	}
	if strings.TrimSpace(p.ClaudePluginPath) == "" {
		p.ClaudePluginPath = canonical.ClaudePluginPath
	}
	if strings.TrimSpace(p.CodexPluginPath) == "" {
		p.CodexPluginPath = canonical.CodexPluginPath
	}
	if strings.TrimSpace(p.CoreClaudePluginPath) == "" {
		p.CoreClaudePluginPath = canonical.CoreClaudePluginPath
	}
	if strings.TrimSpace(p.CoreCodexPluginPath) == "" {
		p.CoreCodexPluginPath = canonical.CoreCodexPluginPath
	}
	if strings.TrimSpace(p.ShimPath) == "" {
		p.ShimPath = canonical.ShimPath
	}
	if strings.TrimSpace(p.metadataPath) == "" {
		p.metadataPath = canonical.metadataPath
	}
	return p, nil
}
