package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCodexExecPromptPreservesAssistantHistory(t *testing.T) {
	t.Parallel()

	prompt := buildCodexExecPrompt([]Message{
		{Role: "system", Content: "Use the session history."},
		{Role: "user", Content: "Inspect the repo."},
		{Role: "assistant", Content: "I checked the repo state and found one issue."},
		{Role: "system", Content: "Tool shell status=done | result=clean"},
		{Role: "user", Content: "continue now"},
	})

	expectedParts := []string{
		"System instructions:\nUse the session history.",
		"User request:\nInspect the repo.",
		"Conversation history:\nAssistant reply:\nI checked the repo state and found one issue.",
		"System instructions:\nTool shell status=done | result=clean",
		"User request:\ncontinue now",
	}
	for _, expected := range expectedParts {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing expected section %q\nfull prompt:\n%s", expected, prompt)
		}
	}
}

func TestExtractExecAssistantTextFromStructuredContent(t *testing.T) {
	t.Parallel()

	content := []map[string]string{
		{"type": "output_text", "text": "First line."},
		{"type": "output_text", "text": "Second line."},
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}

	item := codexExecItem{
		ID:      "item_1",
		Type:    "assistant_message",
		Content: raw,
	}

	got := extractExecAssistantText(item)
	if !strings.Contains(got, "First line.") {
		t.Fatalf("missing first line in extracted text: %q", got)
	}
	if !strings.Contains(got, "Second line.") {
		t.Fatalf("missing second line in extracted text: %q", got)
	}
}

func TestEmitAssistantDeltaDeduplicatesCumulativeUpdates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	events := make(chan Event, 3)
	snapshots := make(map[string]string)

	if !emitAssistantDelta(ctx, events, "item_1", "hello", snapshots) {
		t.Fatal("first emit returned false")
	}
	if !emitAssistantDelta(ctx, events, "item_1", "hello world", snapshots) {
		t.Fatal("second emit returned false")
	}
	if !emitAssistantDelta(ctx, events, "item_1", "hello world", snapshots) {
		t.Fatal("third emit returned false")
	}

	close(events)

	got := make([]string, 0, 3)
	for event := range events {
		tokenEvent, ok := event.(TokenEvent)
		if !ok {
			t.Fatalf("unexpected event type %T", event)
		}
		got = append(got, tokenEvent.Token)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 token events, got %d (%v)", len(got), got)
	}
	if got[0] != "hello" {
		t.Fatalf("unexpected first token: %q", got[0])
	}
	if got[1] != " world" {
		t.Fatalf("unexpected second token: %q", got[1])
	}
}
