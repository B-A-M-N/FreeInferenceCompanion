package cli

import (
	"strings"
	"testing"
)

func TestAttributionHelpAndValidation(t *testing.T) {
	var out, errOut strings.Builder
	if code := cmdAttribution([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "attribution set off|append|standalone") {
		t.Fatalf("help code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := cmdAttribution([]string{"set", "bogus"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "invalid attribution.commit_mode") {
		t.Fatalf("invalid code=%d err=%q", code, errOut.String())
	}
}
