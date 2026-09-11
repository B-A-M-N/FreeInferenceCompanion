// Package commitattribution safely rewrites only agent-issued git commit
// commands. It fails open by returning the original command whenever grammar
// or message attribution is ambiguous.
package commitattribution

import (
	"fmt"
	"sort"
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

const attributionSupportMarker = "Support-FreeInference:"

// Rewrite parses a POSIX shell program, finds every eligible direct git commit,
// and returns a minimally rewritten command. Uncertainty is fail-open.
func Rewrite(command string, policy Policy) (string, bool, Reason) {
	if policy.Mode != ModeAppend && policy.Mode != ModeStandalone {
		return command, false, ReasonDisabled
	}
	if strings.TrimSpace(command) == "" {
		return command, false, ReasonDisabled
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return command, false, ReasonAmbiguous
	}
	var replacements []wordReplacement
	var reason Reason
	containsSubstitution := false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			containsSubstitution = true
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || !isGitCommitCall(call) {
			return true
		}
		replacement, why := replacementForCall(command, call, policy)
		if replacement != nil {
			replacements = append(replacements, *replacement)
			return true
		}
		if reason == "" {
			reason = why
		}
		return true
	})
	if containsSubstitution {
		return command, false, ReasonAmbiguous
	}
	if len(replacements) == 0 {
		return command, false, reason
	}
	// Spans are disjoint because shell words are disjoint. Build the result once
	// so large shell programs do not repeatedly copy the growing command.
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	cursor := 0
	var rewritten strings.Builder
	rewritten.Grow(len(command))
	for _, replacement := range replacements {
		if replacement.start < cursor || replacement.end < replacement.start || replacement.end > len(command) {
			return command, false, ReasonAmbiguous
		}
		rewritten.WriteString(command[cursor:replacement.start])
		rewritten.WriteString(replacement.text)
		cursor = replacement.end
	}
	rewritten.WriteString(command[cursor:])
	return rewritten.String(), true, ""
}

type wordReplacement struct {
	start, end int
	text       string
}

func isGitCommitCall(call *syntax.CallExpr) bool {
	if len(call.Assigns) > 0 || len(call.Args) < 3 {
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

func replacementForCall(original string, call *syntax.CallExpr, policy Policy) (*wordReplacement, Reason) {
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
		return nil, ReasonAmbiguous
	}
	last := messages[len(messages)-1]
	message, ok := literal(last)
	if !ok {
		return nil, ReasonAmbiguous
	}
	// Detect both the legacy marker and the support line emitted by current
	// footers so repeated hooks remain idempotent across shell quoting styles.
	if strings.Contains(message, AttributionFooter) || strings.Contains(message, attributionSupportMarker) {
		return nil, ReasonDuplicate
	}
	if policy.Mode == ModeAppend && !nativeAttributionPresent(message) {
		return nil, ReasonMissingNative
	}
	model, modelOK := sanitizeModel(policy.Model)
	if policy.Model != "" && !modelOK {
		return nil, ReasonModelUnavailable
	}
	var footer string
	if !modelOK {
		footer = "\n\n" + AttributionFooter + "\nInference provided via FreeInference.org.\nSupport-FreeInference: https://freeinference.org/"
	} else {
		footer = fmt.Sprintf("\n\n%s\nInference: %s via FreeInference.org\nSupport-FreeInference: https://freeinference.org/", AttributionFooter, model)
	}
	updated := message + strings.TrimRight(footer, "\n") + "\n"
	newWord, err := syntax.Quote(updated, syntax.LangBash)
	if err != nil {
		return nil, ReasonAmbiguous
	}
	start, end := wordPosition(last)
	if start < 0 || end <= start || end > len(original) {
		return nil, ReasonAmbiguous
	}
	return &wordReplacement{start: start, end: end, text: newWord}, ""
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
	if len(out) > 128 || strings.Contains(out, "Inference-Provider:") || strings.Contains(out, attributionSupportMarker) || strings.Contains(out, "Inference:") {
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

func wordPosition(word *syntax.Word) (int, int) {
	if word == nil || !word.Pos().IsValid() || !word.End().IsValid() {
		return -1, -1
	}
	return int(word.Pos().Offset()), int(word.End().Offset())
}
