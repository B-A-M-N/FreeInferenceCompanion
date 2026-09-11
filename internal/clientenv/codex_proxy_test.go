package clientenv

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCodexConfig(t *testing.T, root, baseURL string) {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	contents := "model_provider = \"fi\"\n\n[model_providers.fi]\nbase_url = \"" + baseURL + "\"\n"
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCodexConfigRouteStates(t *testing.T) {
	home := t.TempDir()
	direct := filepath.Join(home, "agent-alpha")
	proxy := filepath.Join(home, "agent-beta")
	proxyAttested := filepath.Join(home, ".totally-unrelated-name")
	other := filepath.Join(home, "other")
	writeCodexConfig(t, direct, "https://freeinference.org/v1")
	writeCodexConfig(t, proxy, "http://127.0.0.1:18769/v1")
	writeCodexConfig(t, proxyAttested, "http://localhost:18769/v1")
	writeCodexConfig(t, other, "https://api.openai.com/v1")

	state, _, err := VerifyCodexConfigRoute(home, direct)
	if err != nil || state != CodexRouteVerifiedDirect {
		t.Fatalf("direct=%s err=%v", state, err)
	}
	state, _, err = VerifyCodexConfigRoute(home, proxy)
	if err != nil || state != CodexRouteCandidate {
		t.Fatalf("proxy unattested=%s err=%v", state, err)
	}
	if err := SetCodexProxyAttestation(home, proxyAttested, "https://freeinference.org/v1"); err != nil {
		t.Fatal(err)
	}
	state, _, err = VerifyCodexConfigRoute(home, proxyAttested)
	if err != nil || state != CodexRouteVerifiedProxy {
		t.Fatalf("proxy attested=%s err=%v", state, err)
	}
	state, _, err = VerifyCodexConfigRoute(home, other)
	if err != nil || state != CodexRouteUnrelated {
		t.Fatalf("other=%s err=%v", state, err)
	}
	if err := SetCodexProxyAttestation(home, proxy, "https://example.test/v1"); err == nil {
		t.Fatal("non-FI upstream attestation accepted")
	}
}
