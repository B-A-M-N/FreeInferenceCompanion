package installer

import (
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/clientenv"
)

func TestCorePluginVersionForClientUsesRequestedClientVersion(t *testing.T) {
	metadata := &InstallationMetadata{
		ClaudePluginVersion: "v0.1.0",
		CodexPluginVersion:  "v0.2.0",
	}
	if got := corePluginVersionForClient(metadata, clientenv.ClientClaudeCode); got != "v0.1.0" {
		t.Fatalf("Claude plugin version = %q, want v0.1.0", got)
	}
	if got := corePluginVersionForClient(metadata, clientenv.ClientCodex); got != "v0.2.0" {
		t.Fatalf("Codex plugin version = %q, want v0.2.0", got)
	}
}
