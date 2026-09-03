package adapters

import (
	"strings"
	"testing"

	"github.com/b-a-m-n/freeinference-companion/internal/state"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func loadCodex(t *testing.T, paths state.Paths, sessionID string) *schema.Snapshot {
	t.Helper()
	snap, err := state.LoadSnapshot(paths, schema.ClientCodex, sessionID)
	if err != nil || snap == nil {
		t.Fatalf("load snapshot: err=%v snap=%v", err, snap)
	}
	return snap
}

func TestCodexModelIsSanitizedBeforePersistence(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewCodexAdapter(paths)
	if err := a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c1", Model: "glm\x1b[31m-5.1"}); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if model := loadCodex(t, paths, "c1").Model.ID; strings.ContainsRune(model, '\x1b') {
		t.Errorf("persisted model contains terminal control byte: %q", model)
	}
}

func TestCodexSessionLifecycle(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewCodexAdapter(paths)

	if err := a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c1", Model: "glm-5.1"}); err != nil {
		t.Fatalf("session start: %v", err)
	}
	snap := loadCodex(t, paths, "c1")
	if snap.Client.Type != schema.ClientCodex {
		t.Errorf("client = %s", snap.Client.Type)
	}
	if snap.Session.Status != schema.SessionActive {
		t.Errorf("status = %s", snap.Session.Status)
	}
	if snap.Model.ContextLength != nil {
		t.Error("codex context length must stay null (unknown)")
	}

	out, err := a.HandleUserPromptSubmit(&schema.CodexHookInput{SessionID: "c1", Prompt: "hi"}, "c1")
	if err != nil || out != nil {
		t.Errorf("prompt submit = %v, %v — codex never emits warnings", out, err)
	}
	snap = loadCodex(t, paths, "c1")
	if snap.Activity.TurnActive == nil || !*snap.Activity.TurnActive {
		t.Error("turn should be active")
	}

	if err := a.HandleStop("c1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	snap = loadCodex(t, paths, "c1")
	if snap.Activity.TurnActive == nil || *snap.Activity.TurnActive {
		t.Error("turn should be inactive")
	}
	if snap.Session.Status == schema.SessionCompleted {
		t.Error("stop must not complete the session")
	}

	if err := a.HandleSessionEnd("c1"); err != nil {
		t.Fatalf("session end: %v", err)
	}
	snap = loadCodex(t, paths, "c1")
	if snap.Session.Status != schema.SessionCompleted || snap.Session.EndedAt == nil {
		t.Errorf("session should be completed with EndedAt: %+v", snap.Session)
	}
}

func TestCodexLifecycleSourcePreservesLogicalSession(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewCodexAdapter(paths)

	if err := a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c-source", Model: "glm-5.1", Source: "startup"}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	first := loadCodex(t, paths, "c-source")
	startedAt := first.Session.StartedAt
	if first.Session.StartSource != "startup" || first.Session.ConversationEpoch != 1 {
		t.Fatalf("startup metadata = %+v", first.Session)
	}

	for _, source := range []string{"resume", "compact"} {
		if err := a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c-source", Model: "glm-5.1", Source: source}); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		snap := loadCodex(t, paths, "c-source")
		if !snap.Session.StartedAt.Equal(startedAt) {
			t.Fatalf("%s created a new logical session", source)
		}
		if snap.Session.ConversationEpoch != 1 {
			t.Fatalf("%s changed conversational epoch: %d", source, snap.Session.ConversationEpoch)
		}
	}

	if err := a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c-source", Model: "glm-5.1", Source: "clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	snap := loadCodex(t, paths, "c-source")
	if !snap.Session.StartedAt.Equal(startedAt) || snap.Session.ConversationEpoch != 2 || snap.Session.StartSource != "clear" {
		t.Fatalf("clear metadata = %+v", snap.Session)
	}

	events, err := state.ReadEvents(paths, schema.ClientCodex, "c-source", 0)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	starts := 0
	for _, event := range events {
		if event.Type == state.EventSessionStarted {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("session-start events = %d, want startup + clear only", starts)
	}
}

func TestCodexTurnIDSuppressesDuplicateAndStaleStops(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewCodexAdapter(paths)
	_ = a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c-turn", Model: "glm-5.1", Source: "startup"})

	first := &schema.CodexHookInput{SessionID: "c-turn", TurnID: "turn-1", Model: "glm-5.1", Prompt: "private"}
	if _, err := a.HandleUserPromptSubmit(first, "c-turn"); err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	if _, err := a.HandleUserPromptSubmit(first, "c-turn"); err != nil {
		t.Fatalf("duplicate prompt: %v", err)
	}
	if err := a.HandleStop("c-turn", &schema.CodexHookInput{SessionID: "c-turn", TurnID: "stale-turn"}); err != nil {
		t.Fatalf("stale stop: %v", err)
	}
	if err := a.HandleStop("c-turn", &schema.CodexHookInput{SessionID: "c-turn", TurnID: "turn-1"}); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := a.HandleStop("c-turn", &schema.CodexHookInput{SessionID: "c-turn", TurnID: "turn-1"}); err != nil {
		t.Fatalf("duplicate stop: %v", err)
	}

	snap := loadCodex(t, paths, "c-turn")
	if snap.Activity.TurnID != "" || snap.Activity.LastTurnID != "turn-1" || snap.Activity.TurnActive == nil || *snap.Activity.TurnActive {
		t.Fatalf("turn correlation = %+v", snap.Activity)
	}
	events, err := state.ReadEvents(paths, schema.ClientCodex, "c-turn", 0)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	prompts, stops := 0, 0
	for _, event := range events {
		switch event.Type {
		case state.EventPromptSubmitted:
			prompts++
			if strings.Contains(event.Detail, "private") {
				t.Error("prompt text persisted in event detail")
			}
		case state.EventTurnStopped:
			stops++
		}
	}
	if prompts != 1 || stops != 1 {
		t.Fatalf("prompt/stop events = %d/%d, want 1/1", prompts, stops)
	}
}

func TestCodexModelObservedOnLifecycleEvents(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewCodexAdapter(paths)
	_ = a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c-model", Model: "glm-5.1", Source: "startup"})
	_, _ = a.HandleUserPromptSubmit(&schema.CodexHookInput{SessionID: "c-model", Model: "glm-5.1", TurnID: "turn-1"}, "c-model")
	_ = a.HandlePreCompact(&schema.CodexHookInput{SessionID: "c-model", Model: "glm-5.2", Trigger: "automatic"}, "c-model")

	snap := loadCodex(t, paths, "c-model")
	if snap.Model.ID != "glm-5.2" || snap.Model.MetadataSource != "client_hook" {
		t.Fatalf("model observation = %+v", snap.Model)
	}
	events, err := state.ReadEvents(paths, schema.ClientCodex, "c-model", 0)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == state.EventModelSwitch && strings.Contains(event.Detail, "PreCompact") {
			found = true
		}
	}
	if !found {
		t.Error("model discontinuity was not recorded with lifecycle provenance")
	}
}

func TestCodexCompactionNeverFabricatesPercentage(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	a := NewCodexAdapter(paths)

	_ = a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c1", Model: "glm-5.1"})
	_ = a.HandlePreCompact(&schema.CodexHookInput{SessionID: "c1", Trigger: "manual"}, "c1")

	snap := loadCodex(t, paths, "c1")
	if !snap.Compaction.Pending {
		t.Error("compaction should be pending")
	}
	if snap.Compaction.PreTokens != nil {
		t.Error("codex must not record pre tokens")
	}

	_ = a.HandlePostCompact(&schema.CodexHookInput{SessionID: "c1", Trigger: "manual"}, "c1")
	snap = loadCodex(t, paths, "c1")
	r := snap.Compaction.LastResult
	if r == nil {
		t.Fatal("compaction occurrence should be recorded")
	}
	if r.ReductionPct != nil || r.PreTokens != nil || r.PostTokens != nil {
		t.Errorf("codex must not fabricate compaction numbers: %+v", r)
	}
	if r.Trigger != "manual" {
		t.Errorf("trigger = %q", r.Trigger)
	}
}

func TestCodexNoWarningsWithoutProvider(t *testing.T) {
	unconfirmProvider(t)
	paths := testPaths(t)
	a := NewCodexAdapter(paths)

	_ = a.HandleSessionStart(&schema.CodexHookInput{SessionID: "c1", Model: "glm-5.1"})
	out, _ := a.HandleUserPromptSubmit(&schema.CodexHookInput{SessionID: "c1"}, "c1")
	if out != nil {
		t.Errorf("codex must never emit output: %+v", out)
	}
}

func TestCodexSessionIsolation(t *testing.T) {
	confirmFreeInference(t)
	paths := testPaths(t)
	codex := NewCodexAdapter(paths)
	claude := NewClaudeAdapter(paths)

	_ = codex.HandleSessionStart(&schema.CodexHookInput{SessionID: "shared-id", Model: "codex-model"})
	_ = claude.HandleSessionStart(&schema.ClaudeHookInput{SessionID: "shared-id", Model: "claude-model"})

	cs := loadCodex(t, paths, "shared-id")
	cls := loadClaude(t, paths, "shared-id")
	if cs.Model.ID != "codex-model" || cls.Model.ID != "claude-model" {
		t.Errorf("clients must stay isolated: codex=%s claude=%s", cs.Model.ID, cls.Model.ID)
	}
}
