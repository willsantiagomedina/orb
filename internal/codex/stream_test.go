package codex

import (
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
