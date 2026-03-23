package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/willdev/orb/internal/config"
)

const (
	defaultClaudeModel  = "sonnet"
	defaultClaudeEffort = "medium"
)

// ClaudeBackend implements Backend using the installed Claude Code CLI.
type ClaudeBackend struct {
	configPath string

	mu             sync.RWMutex
	modelOverride  string
	effortOverride string
	cwdOverride    string
}

type claudeStreamEnvelope struct {
	Type    string              `json:"type"`
	Subtype string              `json:"subtype"`
	Error   string              `json:"error"`
	IsError bool                `json:"is_error"`
	Result  string              `json:"result"`
	Message claudeMessageRecord `json:"message"`
}

type claudeMessageRecord struct {
	Content []claudeContentRecord `json:"content"`
}

type claudeContentRecord struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Text  string          `json:"text"`
	Input json.RawMessage `json:"input"`
}

// NewClaudeBackend creates a Claude backend bound to a config path.
func NewClaudeBackend(configPath string) *ClaudeBackend {
	return &ClaudeBackend{
		configPath: strings.TrimSpace(configPath),
	}
}

// ID returns the backend identifier.
func (c *ClaudeBackend) ID() ID {
	return ClaudeID
}

// Label returns the display name.
func (c *ClaudeBackend) Label() string {
	return "Claude"
}

// AuthStatus returns Claude CLI connectivity/auth status.
func (c *ClaudeBackend) AuthStatus() (string, bool, error) {
	claudeBinary, err := findClaudeBinary()
	if err != nil {
		return "● no auth", false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		claudeBinary,
		"-p",
		"--verbose",
		"--output-format",
		"stream-json",
		"Ping auth status.",
	)
	output, runErr := cmd.CombinedOutput()
	body := strings.ToLower(string(output))
	if strings.Contains(body, "authentication_failed") ||
		strings.Contains(body, "invalid api key") ||
		strings.Contains(body, "please run /login") {
		return "● no auth", false, nil
	}

	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// Avoid reporting stale "no auth" when CLI is slow; keep available state.
			return "● claude connected", true, nil
		}
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return "● claude unavailable", false, nil
		}
		return "● claude unavailable", false, nil
	}

	return "● claude connected", true, nil
}

// AvailableModels returns selectable Claude model aliases and explicit versions.
func (c *ClaudeBackend) AvailableModels() []string {
	return []string{
		"sonnet",
		"opus",
		"claude-sonnet-4-5-20250929",
		"claude-opus-4-1-20250805",
	}
}

// CurrentModel resolves Claude model from runtime override or config.
func (c *ClaudeBackend) CurrentModel() (string, error) {
	c.mu.RLock()
	override := strings.TrimSpace(c.modelOverride)
	c.mu.RUnlock()
	if override != "" {
		return override, nil
	}

	cfg, err := config.Load(c.configPath)
	if err != nil {
		return "", fmt.Errorf("load config for claude model resolution: %w", err)
	}
	configured := strings.TrimSpace(cfg.Model)
	if configured == "" {
		return defaultClaudeModel, nil
	}
	return configured, nil
}

// CurrentReasoningEffort resolves reasoning effort for UI parity.
func (c *ClaudeBackend) CurrentReasoningEffort() (string, error) {
	c.mu.RLock()
	override := normalizeClaudeEffort(c.effortOverride)
	c.mu.RUnlock()
	if override != "" {
		return override, nil
	}

	cfg, err := config.Load(c.configPath)
	if err != nil {
		return "", fmt.Errorf("load config for claude reasoning effort: %w", err)
	}
	configured := normalizeClaudeEffort(cfg.ReasoningEffort)
	if configured != "" {
		return configured, nil
	}
	return defaultClaudeEffort, nil
}

// SetRuntimeModel sets the in-memory Claude model override.
func (c *ClaudeBackend) SetRuntimeModel(model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modelOverride = strings.TrimSpace(model)
}

// SetRuntimeReasoningEffort sets the in-memory reasoning override.
func (c *ClaudeBackend) SetRuntimeReasoningEffort(effort string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.effortOverride = normalizeClaudeEffort(effort)
}

// SetRuntimeWorkingDir sets the Claude command working directory.
func (c *ClaudeBackend) SetRuntimeWorkingDir(path string) {
	clean := strings.TrimSpace(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if clean == "" {
		c.cwdOverride = ""
		return
	}
	c.cwdOverride = filepath.Clean(clean)
}

// Stream starts a Claude CLI JSON stream and maps it into backend events.
func (c *ClaudeBackend) Stream(ctx context.Context, messages []Message, toolDefs []ToolDefinition) <-chan Event {
	stream := make(chan Event)

	go func() {
		defer close(stream)

		claudeBinary, err := findClaudeBinary()
		if err != nil {
			sendEvent(ctx, stream, ErrorEvent{Err: err})
			return
		}

		modelName, err := c.CurrentModel()
		if err != nil {
			sendEvent(ctx, stream, ErrorEvent{Err: err})
			return
		}

		prompt := buildClaudePrompt(messages, toolDefs)
		if strings.TrimSpace(prompt) == "" {
			prompt = "Respond helpfully to the user request."
		}

		args := []string{
			"-p",
			"--verbose",
			"--output-format",
			"stream-json",
			"--include-partial-messages",
		}
		if strings.TrimSpace(modelName) != "" {
			args = append(args, "--model", strings.TrimSpace(modelName))
		}
		args = append(args, prompt)

		cmd := exec.CommandContext(ctx, claudeBinary, args...)
		if workingDir := c.runtimeWorkingDir(); workingDir != "" {
			if info, statErr := os.Stat(workingDir); statErr == nil && info.IsDir() {
				cmd.Dir = workingDir
			}
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			sendEvent(ctx, stream, ErrorEvent{Err: fmt.Errorf("open claude stdout: %w", err)})
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			sendEvent(ctx, stream, ErrorEvent{Err: fmt.Errorf("open claude stderr: %w", err)})
			return
		}

		if err := cmd.Start(); err != nil {
			sendEvent(ctx, stream, ErrorEvent{Err: fmt.Errorf("start claude process: %w", err)})
			return
		}

		stderrDone := make(chan string, 1)
		go func() {
			bytes, readErr := io.ReadAll(stderr)
			if readErr != nil {
				stderrDone <- readErr.Error()
				return
			}
			stderrDone <- string(bytes)
		}()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

		assistantText := ""
		done := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "{") {
				continue
			}

			var envelope claudeStreamEnvelope
			if err := json.Unmarshal([]byte(line), &envelope); err != nil {
				continue
			}

			switch envelope.Type {
			case "assistant":
				textChunk, toolEvents := parseClaudeAssistant(envelope)
				if strings.TrimSpace(textChunk) != "" {
					delta := diffAssistantText(assistantText, textChunk)
					if delta == "" {
						delta = textChunk
					}
					assistantText = textChunk
					if !sendEvent(ctx, stream, TokenEvent{Token: delta}) {
						return
					}
				}
				for _, toolEvent := range toolEvents {
					if !sendEvent(ctx, stream, toolEvent) {
						return
					}
				}

				if strings.TrimSpace(envelope.Error) != "" {
					sendEvent(ctx, stream, ErrorEvent{Err: errors.New(strings.TrimSpace(envelope.Error))})
					return
				}
			case "result":
				if envelope.IsError {
					errText := strings.TrimSpace(envelope.Result)
					if errText == "" {
						errText = "claude stream reported an error"
					}
					sendEvent(ctx, stream, ErrorEvent{Err: errors.New(errText)})
					return
				}
				done = true
				sendEvent(ctx, stream, DoneEvent{})
				return
			case "error":
				errText := strings.TrimSpace(envelope.Error)
				if errText == "" {
					errText = strings.TrimSpace(envelope.Result)
				}
				if errText == "" {
					errText = "claude stream error"
				}
				sendEvent(ctx, stream, ErrorEvent{Err: errors.New(errText)})
				return
			}
		}

		if scanErr := scanner.Err(); scanErr != nil {
			sendEvent(ctx, stream, ErrorEvent{Err: fmt.Errorf("read claude stream: %w", scanErr)})
			_ = cmd.Wait()
			<-stderrDone
			return
		}

		waitErr := cmd.Wait()
		stderrText := strings.TrimSpace(<-stderrDone)
		if waitErr != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			errText := fmt.Sprintf("claude stream failed: %v", waitErr)
			if stderrText != "" {
				errText += ": " + truncateText(stderrText, 700)
			}
			sendEvent(ctx, stream, ErrorEvent{Err: errors.New(errText)})
			return
		}

		if !done {
			sendEvent(ctx, stream, DoneEvent{})
		}
	}()

	return stream
}

func (c *ClaudeBackend) runtimeWorkingDir() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.cwdOverride)
}

func parseClaudeAssistant(envelope claudeStreamEnvelope) (string, []ToolCallEvent) {
	if len(envelope.Message.Content) == 0 {
		return "", []ToolCallEvent{}
	}

	textParts := make([]string, 0, len(envelope.Message.Content))
	toolEvents := make([]ToolCallEvent, 0, 2)
	for _, content := range envelope.Message.Content {
		switch strings.ToLower(strings.TrimSpace(content.Type)) {
		case "text":
			if strings.TrimSpace(content.Text) != "" {
				textParts = append(textParts, content.Text)
			}
		case "tool_use":
			name := strings.TrimSpace(content.Name)
			if name == "" {
				name = "tool"
			}
			args := strings.TrimSpace(string(content.Input))
			if args == "" {
				args = "{}"
			}
			toolEvents = append(toolEvents, ToolCallEvent{
				ID:        strings.TrimSpace(content.ID),
				Name:      name,
				Arguments: args,
			})
		}
	}

	return strings.TrimSpace(strings.Join(textParts, "")), toolEvents
}

func diffAssistantText(previous string, next string) string {
	if strings.HasPrefix(next, previous) {
		return strings.TrimPrefix(next, previous)
	}
	return next
}

func buildClaudePrompt(messages []Message, toolDefs []ToolDefinition) string {
	systemParts := make([]string, 0, len(messages))
	userParts := make([]string, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		switch role {
		case "system":
			systemParts = append(systemParts, content)
		case "user":
			userParts = append(userParts, content)
		}
	}

	var builder strings.Builder
	if len(systemParts) > 0 {
		builder.WriteString("System instructions:\n")
		builder.WriteString(strings.Join(systemParts, "\n\n"))
		builder.WriteString("\n\n")
	}

	if len(toolDefs) > 0 {
		builder.WriteString("Available tools:\n")
		for _, toolDef := range toolDefs {
			name := strings.TrimSpace(toolDef.Function.Name)
			if name == "" {
				continue
			}
			description := strings.TrimSpace(toolDef.Function.Description)
			if description == "" {
				builder.WriteString("- ")
				builder.WriteString(name)
				builder.WriteString("\n")
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(name)
			builder.WriteString(": ")
			builder.WriteString(description)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	if len(userParts) > 0 {
		builder.WriteString("User request:\n")
		builder.WriteString(userParts[len(userParts)-1])
	}

	return strings.TrimSpace(builder.String())
}

func normalizeClaudeEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "fast":
		return "low"
	case "medium", "normal":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "max":
		return "xhigh"
	default:
		return ""
	}
}

func truncateText(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "..."
}

func findClaudeBinary() (string, error) {
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	candidates := []string{
		filepath.Join(strings.TrimSpace(os.Getenv("HOME")), ".local", "bin", "claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
		"/usr/bin/claude",
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", errors.New("claude binary not found in PATH or common install locations")
}
