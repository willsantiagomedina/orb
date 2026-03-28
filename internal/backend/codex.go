package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/willsantiagomedina/orb/internal/codex"
)

// CodexBackend adapts internal/codex to the shared Backend interface.
type CodexBackend struct {
	configPath string
}

// NewCodexBackend creates a Codex backend adapter bound to a config path.
func NewCodexBackend(configPath string) *CodexBackend {
	cleanPath := strings.TrimSpace(configPath)
	codex.SetConfigPath(cleanPath)
	return &CodexBackend{configPath: cleanPath}
}

// ID returns the backend identifier.
func (c *CodexBackend) ID() ID {
	return CodexID
}

// Label returns the display name.
func (c *CodexBackend) Label() string {
	return "Codex"
}

// AuthStatus returns Codex authentication status for UI display.
func (c *CodexBackend) AuthStatus() (string, bool, error) {
	credentials, err := codex.ResolveCandidatesWithConfigPath(c.configPath)
	if errors.Is(err, codex.ErrNoAuth) {
		return "● no auth", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolve codex auth status: %w", err)
	}

	for _, credential := range credentials {
		source := strings.TrimSpace(credential.Source)
		if source == "orb-config" || source == "codex-auth-json:OPENAI_API_KEY" || strings.HasPrefix(source, "env:") {
			return "● codex connected", true, nil
		}
	}
	return "● session auth", true, nil
}

// AvailableModels returns selectable Codex models.
func (c *CodexBackend) AvailableModels() []string {
	return codex.AvailableModels()
}

// CurrentModel resolves the active Codex model.
func (c *CodexBackend) CurrentModel() (string, error) {
	modelName, err := codex.CurrentModel()
	if err != nil {
		return "", fmt.Errorf("resolve codex model: %w", err)
	}
	if strings.TrimSpace(modelName) == "" {
		return "gpt-5.4", nil
	}
	return modelName, nil
}

// CurrentReasoningEffort resolves active Codex reasoning effort.
func (c *CodexBackend) CurrentReasoningEffort() (string, error) {
	effort, err := codex.CurrentReasoningEffort()
	if err != nil {
		return "", fmt.Errorf("resolve codex reasoning effort: %w", err)
	}
	if strings.TrimSpace(effort) == "" {
		return "medium", nil
	}
	return effort, nil
}

// SetRuntimeModel sets the in-memory Codex runtime model.
func (c *CodexBackend) SetRuntimeModel(model string) {
	codex.SetRuntimeModel(model)
}

// SetRuntimeReasoningEffort sets in-memory Codex reasoning effort.
func (c *CodexBackend) SetRuntimeReasoningEffort(effort string) {
	codex.SetRuntimeReasoningEffort(effort)
}

// SetRuntimeWorkingDir sets in-memory Codex execution working directory.
func (c *CodexBackend) SetRuntimeWorkingDir(path string) {
	codex.SetRuntimeWorkingDir(path)
}

// Stream starts a Codex stream and maps codex events to backend events.
func (c *CodexBackend) Stream(ctx context.Context, messages []Message, toolDefs []ToolDefinition) <-chan Event {
	codexMessages := make([]codex.Message, 0, len(messages))
	for _, message := range messages {
		codexMessages = append(codexMessages, codex.Message{
			Role:       message.Role,
			Content:    message.Content,
			ImagePaths: message.ImagePaths,
		})
	}

	codexTools := make([]codex.ToolDefinition, 0, len(toolDefs))
	for _, toolDef := range toolDefs {
		codexTools = append(codexTools, codex.ToolDefinition{
			Type: toolDef.Type,
			Function: codex.ToolFunctionDefinition{
				Name:        toolDef.Function.Name,
				Description: toolDef.Function.Description,
				Parameters:  toolDef.Function.Parameters,
			},
		})
	}

	rawStream := codex.Stream(ctx, codexMessages, codexTools)
	stream := make(chan Event)

	go func() {
		defer close(stream)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-rawStream:
				if !ok {
					return
				}

				switch typed := event.(type) {
				case codex.TokenEvent:
					if !sendEvent(ctx, stream, TokenEvent{Token: typed.Token}) {
						return
					}
				case codex.ToolCallEvent:
					if !sendEvent(ctx, stream, ToolCallEvent{
						ID:        typed.ID,
						Name:      typed.Name,
						Arguments: typed.Arguments,
					}) {
						return
					}
				case codex.DoneEvent:
					if !sendEvent(ctx, stream, DoneEvent{}) {
						return
					}
				case codex.ErrorEvent:
					if !sendEvent(ctx, stream, ErrorEvent{Err: typed.Err}) {
						return
					}
				default:
					if !sendEvent(ctx, stream, ErrorEvent{Err: errors.New("unknown codex stream event")}) {
						return
					}
				}
			}
		}
	}()

	return stream
}

func sendEvent(ctx context.Context, stream chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case stream <- event:
		return true
	}
}
