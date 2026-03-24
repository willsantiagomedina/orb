package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/willsantiagomedina/orb/internal/config"
)

// ID is a backend identifier persisted in configuration.
type ID string

const (
	// CodexID selects the OpenAI Codex backend.
	CodexID ID = "codex"
	// ClaudeID selects the Claude Code backend.
	ClaudeID ID = "claude"
)

// Message is one chat message sent to a backend stream.
type Message struct {
	Role    string
	Content string
}

// ToolDefinition defines one callable tool for a backend.
type ToolDefinition struct {
	Type     string
	Function ToolFunctionDefinition
}

// ToolFunctionDefinition contains the tool schema metadata.
type ToolFunctionDefinition struct {
	Name        string
	Description string
	Parameters  []byte
}

// Event is a typed backend stream event.
type Event interface {
	isEvent()
}

// TokenEvent carries streamed assistant text.
type TokenEvent struct {
	Token string
}

// ToolCallEvent carries tool-call metadata and payload.
type ToolCallEvent struct {
	ID        string
	Name      string
	Arguments string
}

// DoneEvent marks successful stream completion.
type DoneEvent struct{}

// ErrorEvent marks stream failure.
type ErrorEvent struct {
	Err error
}

func (TokenEvent) isEvent()    {}
func (ToolCallEvent) isEvent() {}
func (DoneEvent) isEvent()     {}
func (ErrorEvent) isEvent()    {}

// Backend defines the streaming/runtime contract for Orb model backends.
type Backend interface {
	ID() ID
	Label() string
	AuthStatus() (status string, connected bool, err error)
	AvailableModels() []string
	CurrentModel() (string, error)
	CurrentReasoningEffort() (string, error)
	SetRuntimeModel(model string)
	SetRuntimeReasoningEffort(effort string)
	SetRuntimeWorkingDir(path string)
	Stream(ctx context.Context, messages []Message, toolDefs []ToolDefinition) <-chan Event
}

// NormalizeID maps user/config input to a supported backend ID.
func NormalizeID(value string) ID {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ClaudeID):
		return ClaudeID
	default:
		return CodexID
	}
}

// IDs returns the supported backend IDs in display order.
func IDs() []ID {
	return []ID{CodexID, ClaudeID}
}

// ResolveConfiguredID loads and normalizes backend selection from orb.toml.
func ResolveConfiguredID(configPath string) (ID, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return CodexID, fmt.Errorf("load config for backend selection: %w", err)
	}
	return NormalizeID(cfg.Codex.Backend), nil
}

// New constructs a backend implementation for an ID.
func New(id ID, configPath string) (Backend, error) {
	switch NormalizeID(string(id)) {
	case ClaudeID:
		return NewClaudeBackend(configPath), nil
	case CodexID:
		fallthrough
	default:
		return NewCodexBackend(configPath), nil
	}
}
