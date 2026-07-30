package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// CodexPluginDir is the directory where the Codex plugin ZIP is extracted.
	CodexPluginDir string
}

// DefaultPaths returns Paths using standard locations.
func DefaultPaths() (Paths, error) {
	home := homeDir()
	if home == "" {
		return Paths{}, fmt.Errorf("home dir: HOME unset")
	}

	localBin := filepath.Join(home, ".local", "bin")
	installDir := filepath.Join(home, ".local", "freeinference")

	return Paths{
		InstallDir:      installDir,
		BinaryPath:      filepath.Join(installDir, "bin", "freeinference"),
		LocalBin:        localBin,
		ClaudePluginDir: filepath.Join(home, ".claude", "plugins"),
		CodexPluginDir:  filepath.Join(home, ".codex", "plugins"),
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

// OpenInBrowser opens a URL in the user's default browser. If the command
// fails (e.g. no $BROWSER), the error is returned.
func OpenInBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtimeGOOS() {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Run()
}

// runtimeGOOS is an indirection that tests can override.
var runtimeGOOS = func() string { return goos() }

func goos() string { return os.Getenv("GOOS_FALLBACK") }
