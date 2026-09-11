package commitattribution

import (
	"strings"
	"testing"
)

func TestRewriteModes(t *testing.T) {
	native := `git commit -m "fix bug\n\nGenerated with Claude Code\nCo-Authored-By: Claude <noreply@anthropic.com>"`
	plain := `git commit -m "fix bug"`
	tests := []struct {
		name, command string
		mode          CommitMode
		wantContains  string
		wantChanged   bool
	}{
		{"off never changes", native, ModeOff, "Generated with Claude", false},
		{"append native", native, ModeAppend, "Inference: test-model via FreeInference.org", true},
		{"append without native does nothing", plain, ModeAppend, "fix bug", false},
		{"standalone plain", plain, ModeStandalone, "Inference: test-model via FreeInference.org", true},
		{"standalone native appends", native, ModeStandalone, "Inference: test-model via FreeInference.org", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rewritten, changed, _ := Rewrite(tt.command, Policy{Mode: tt.mode, Model: "test-model"})
			if changed != tt.wantChanged || (changed && !strings.Contains(rewritten, tt.wantContains)) {
				t.Fatalf("changed=%v rewritten=%q", changed, rewritten)
			}
		})
	}
}

func TestRewriteSafetyAndIdempotence(t *testing.T) {
	already := `git commit -m "fix\n\nInference-Provider: FreeInference.org\n"`
	if _, changed, reason := Rewrite(already, Policy{Mode: ModeStandalone, Model: "m"}); changed || reason != ReasonDuplicate {
		t.Fatalf("duplicate changed=%v reason=%s", changed, reason)
	}
	for _, unsafe := range []string{
		`sudo git commit -m "x"`,
		`env GIT_AUTHOR_NAME=bot git commit -m "x"`,
		`git commit -F message.txt`,
		`git commit --amend`,
		`git commit --fixup HEAD`,
		`echo "git commit -m fake"`,
		`git -C /tmp commit -m "x"`,
		`git commit`,
		`echo "$(git commit -m 'x')"`,
	} {
		if _, changed, _ := Rewrite(unsafe, Policy{Mode: ModeStandalone}); changed {
			t.Fatalf("unsafe command changed: %s", unsafe)
		}
	}
	rewritten, changed, _ := Rewrite(`git add . && git commit -m "fix"`, Policy{Mode: ModeStandalone, Model: "m"})
	if !changed || !strings.Contains(rewritten, "git add . &&") || !strings.Contains(rewritten, "Support-FreeInference") {
		t.Fatalf("compound command was not safely preserved: %q", rewritten)
	}
	if _, changed, _ = Rewrite(rewritten, Policy{Mode: ModeStandalone, Model: "m"}); changed {
		t.Fatal("rewrite was not idempotent")
	}
	injection := `git commit -m $'line\rInference: fake'`
	if _, changed, _ := Rewrite(injection, Policy{Mode: ModeStandalone, Model: "m"}); changed {
		t.Fatal("injection-bearing shell word accepted")
	}
	if _, changed, _ := Rewrite(`git commit -m "x"`, Policy{Mode: ModeStandalone, Model: "bad\rmodel"}); changed {
		t.Fatal("control-character model accepted")
	}
	if _, changed, _ := Rewrite(`git commit -m "x"`, Policy{Mode: ModeStandalone, Model: strings.Repeat("m", 129)}); changed {
		t.Fatal("oversized model accepted")
	}
	for _, model := range []string{"model Inference-Provider: fake", "model Support-FreeInference: fake", "model Inference: fake"} {
		if _, changed, _ := Rewrite(`git commit -m "x"`, Policy{Mode: ModeStandalone, Model: model}); changed {
			t.Fatalf("footer-like model accepted: %q", model)
		}
	}
}

func TestRewriteRecognizesLegacyAndCurrentDuplicateMarkers(t *testing.T) {
	for _, message := range []string{
		"Inference-Provider: FreeInference.org",
		"Support-FreeInference: https://freeinference.org/",
		"Inference-Provider: FreeInference.org\nSupport-FreeInference: https://freeinference.org/",
	} {
		command := "git commit -m \"fix\\n\\n" + message + "\""
		if _, changed, reason := Rewrite(command, Policy{Mode: ModeStandalone, Model: "model"}); changed || reason != ReasonDuplicate {
			t.Fatalf("duplicate marker %q changed=%v reason=%s", message, changed, reason)
		}
	}
}

func TestRewriteEligibleDirectCallsInsideShellControlFlow(t *testing.T) {
	for _, command := range []string{
		`git commit -m "first" | cat`,
		`if true; then git commit -m "first"; fi`,
		`for x in one; do git commit -m "first"; done`,
	} {
		rewritten, changed, reason := Rewrite(command, Policy{Mode: ModeStandalone, Model: "model"})
		if !changed || reason != "" || !strings.Contains(rewritten, "Support-FreeInference") {
			t.Fatalf("eligible control-flow command %q changed=%v reason=%s output=%q", command, changed, reason, rewritten)
		}
	}
}

func TestRewriteRecognizesCurrentFooterInDoubleQuotedMessage(t *testing.T) {
	command := "git commit -m \"fix\n\nInference: model via FreeInference.org\nSupport-FreeInference: https://freeinference.org/\n\""
	if _, changed, reason := Rewrite(command, Policy{Mode: ModeStandalone, Model: "model"}); changed || reason != ReasonDuplicate {
		t.Fatalf("current footer changed=%v reason=%s", changed, reason)
	}
}

func TestMissingModelUsesGenericAttribution(t *testing.T) {
	rewritten, changed, _ := Rewrite(`git commit -m "fix"`, Policy{Mode: ModeStandalone})
	if !changed || !strings.Contains(rewritten, "Inference provided via FreeInference.org") {
		t.Fatalf("generic attribution missing: %q", rewritten)
	}
}

func FuzzRewriteFailsOpen(f *testing.F) {
	f.Add("git commit -m \"fix\"")
	f.Add("echo \"git commit\"")
	f.Add("git commit -F x")
	f.Add("git commit --amend")
	f.Add("git -C /x commit -m y")
	f.Fuzz(func(t *testing.T, command string) {
		rewritten, changed, _ := Rewrite(command, Policy{Mode: ModeStandalone, Model: "model"})
		if changed && !strings.Contains(rewritten, "Support-FreeInference") {
			t.Fatalf("changed rewrite missing provenance: %q", rewritten)
		}
	})
}

func TestRewriteMultilineCommandPreservesPriorLines(t *testing.T) {
	command := "# prepare\ncd /tmp && \\\ngit commit -m \"first\"\necho done"
	rewritten, changed, _ := Rewrite(command, Policy{Mode: ModeStandalone, Model: "model"})
	if !changed || !strings.HasPrefix(rewritten, "# prepare\n") || !strings.Contains(rewritten, "echo done") {
		t.Fatalf("multiline rewrite damaged unrelated commands: %q", rewritten)
	}
}

func TestRewriteAllCommitsInOneCommand(t *testing.T) {
	for _, command := range []string{
		`git commit -m "first" && git commit -m "second"`,
		`git commit -m "first"; git commit -m "second"`,
		`(git commit -m "first"; git commit -m "second")`,
	} {
		rewritten, changed, reason := Rewrite(command, Policy{Mode: ModeStandalone, Model: "model"})
		if !changed || reason != "" {
			t.Fatalf("command %q changed=%v reason=%s output=%q", command, changed, reason, rewritten)
		}
		if got := strings.Count(rewritten, "Inference: model via FreeInference.org"); got != 2 {
			t.Fatalf("command %q footer count=%d output=%q", command, got, rewritten)
		}
	}
}

func TestUnknownAttributionModeIsDisabled(t *testing.T) {
	for _, mode := range []CommitMode{"", "unexpected", ModeOff} {
		if _, changed, reason := Rewrite(`git commit -m "x"`, Policy{Mode: mode}); changed || reason != ReasonDisabled {
			t.Fatalf("mode %q changed=%v reason=%s", mode, changed, reason)
		}
	}
}
