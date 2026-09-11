// Package commitattribution safely rewrites only agent-issued git commit
// commands. It fails open by returning the original command whenever grammar
// or message attribution is ambiguous.
package commitattribution

import (
	"fmt"
	"strings"
	"unicode"

	"mvdan.cc/sh/v3/syntax"
)

// CommitMode selects attribution behavior.
type CommitMode string

const (
	ModeOff        CommitMode = "off"
	ModeAppend     CommitMode = "append"
	ModeStandalone CommitMode = "standalone"
)

// Reason explains why no rewrite occurred.
type Reason string

const (
	ReasonDisabled         Reason = "disabled"
	ReasonNotGit           Reason = "not-git-commit"
	ReasonNative           Reason = "native-attribution-present"
	ReasonMissingNative    Reason = "native-attribution-missing"
	ReasonDuplicate        Reason = "already-attributed"
	ReasonAmbiguous        Reason = "ambiguous-command"
	ReasonModelUnavailable Reason = "model-unavailable"
)

// Policy is the only external state the package needs.
type Policy struct {
	Mode   CommitMode
	Client string
	Model  string
}

// AttributionFooter is the stable provenance marker.
const AttributionFooter = "Inference-Provider: FreeInference.org"

// Rewrite parses a POSIX shell program, finds every eligible git commit, and
// returns a minimally rewritten command. Uncertainty is fail-open.
func Rewrite(command string, policy Policy) (string, bool, Reason) {
	if policy.Mode == ModeOff || strings.TrimSpace(command) == "" {
		return command, false, ReasonDisabled
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return command, false, ReasonAmbiguous
	}
	changed := false
	var reason Reason
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if !isGitCommitCall(call) {
			return true
		}
		newCommand, didChange, why := rewriteCallExpr(command, call, policy)
		if didChange {
			command, changed = newCommand, true
			reason = why
			return false
		}
		if reason == "" {
			reason = why
		}
		return true
	})
	return command, changed, reason
}

func isGitCommitCall(call *syntax.CallExpr) bool {
	if len(call.Args) < 3 {
		return false
	}
	if value, ok := literal(call.Args[0]); !ok || value != "git" {
		return false
	}
	// Conservatively accept only direct commit subcommands. Flags such as
	// `git -C path commit` are intentionally not rewritten.
	value, ok := literal(call.Args[1])
	return ok && value == "commit"
}

func rewriteCallExpr(original string, call *syntax.CallExpr, policy Policy) (string, bool, Reason) {
	args := call.Args[2:]
	var (
		messages []*syntax.Word
		unsafe   bool
		expect   bool
	)
	for _, arg := range args {
		text, ok := literal(arg)
		if !ok {
			unsafe = true
			break
		}
		if expect {
			messages = append(messages, arg)
			expect = false
			continue
		}
		if strings.HasPrefix(text, "-") {
			switch text {
			case "-m", "--message":
				expect = true
			default:
				// Only the ordinary visible-message grammar is supported. Any
				// other option can alter message source or interaction, so
				// attribution must fail open.
				unsafe = true
			}
			continue
		}
		// Unquoted pathspec without a preceding message means the effective
		// message is not visible. Never guess.
		unsafe = true
	}
	if unsafe || expect || len(messages) == 0 {
		return original, false, ReasonAmbiguous
	}
	last := messages[len(messages)-1]
	message, ok := literal(last)
	if !ok {
		return original, false, ReasonAmbiguous
	}
	if strings.Contains(message, AttributionFooter) {
		return original, false, ReasonDuplicate
	}
	if policy.Mode == ModeAppend && !nativeAttributionPresent(message) {
		return original, false, ReasonMissingNative
	}
	model, modelOK := sanitizeModel(policy.Model)
	if policy.Model != "" && !modelOK {
		return original, false, ReasonModelUnavailable
	}
	var footer string
	if !modelOK {
		footer = "\n\nInference provided via FreeInference.org.\nSupport-FreeInference: https://freeinference.org/"
	} else {
		footer = fmt.Sprintf("\n\nInference: %s via FreeInference.org\nSupport-FreeInference: https://freeinference.org/", model)
	}
	updated := message + strings.TrimRight(footer, "\n") + "\n"
	newWord, err := syntax.Quote(updated, syntax.LangBash)
	if err != nil {
		return original, false, ReasonAmbiguous
	}
	return replaceWord(original, last, newWord), true, ""
}

func nativeAttributionPresent(message string) bool {
	lines := strings.Split(strings.TrimSpace(message), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Co-Authored-By:") || strings.Contains(trimmed, "Generated with Claude") {
			return true
		}
	}
	return false
}

func sanitizeModel(model string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	var b strings.Builder
	for _, r := range model {
		if r == '\r' || r == '\n' || unicode.IsControl(r) {
			return "", false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > 128 {
		return "", false
	}
	return out, true
}

func literal(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, part := range word.Parts {
		switch value := part.(type) {
		case *syntax.Lit:
			b.WriteString(value.Value)
		case *syntax.SglQuoted:
			if value.Dollar {
				return "", false
			}
			b.WriteString(value.Value)
		case *syntax.DblQuoted:
			for _, inner := range value.Parts {
				if lit, ok := inner.(*syntax.Lit); ok {
					b.WriteString(lit.Value)
				} else {
					return "", false
				}
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

func replaceWord(source string, target *syntax.Word, replacement string) string {
	start, end := wordPosition(target)
	if start < 0 || end <= start || end > len(source) || source[start:end] == "" {
		return source
	}
	return source[:start] + replacement + source[end:]
}

func wordPosition(word *syntax.Word) (int, int) {
	if word == nil || !word.Pos().IsValid() || !word.End().IsValid() {
		return -1, -1
	}
	return int(word.Pos().Offset()), int(word.End().Offset())
}
