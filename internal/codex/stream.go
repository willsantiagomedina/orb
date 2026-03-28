package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/willsantiagomedina/orb/internal/config"
)

const chatCompletionsEndpoint = "https://api.openai.com/v1/chat/completions"
const codexExecTimeout = 15 * time.Minute

const (
	ExecutionModeUnblocked = "unblocked"
	ExecutionModeSandboxed = "sandboxed"
)

var (
	modelOverrideMu  sync.RWMutex
	modelOverride    string
	effortOverride   string
	cwdOverride      string
	execModeOverride string
)

// Message is a chat message sent to the Codex streaming endpoint.
type Message struct {
	Role       string   `json:"role"`
	Content    string   `json:"content"`
	ImagePaths []string `json:"-"` // local image paths; serialised as multimodal content by buildAPIMessages
}

// apiMessage is the on-wire format for the chat completions API.
// Content is json.RawMessage to support both plain-string and array (multimodal) values.
type apiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// imageMediaType returns the MIME type for a local image path based on its extension.
func imageMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// buildAPIMessages converts []Message into the wire format expected by the
// chat completions API, embedding base64-encoded images when present.
func buildAPIMessages(messages []Message) ([]apiMessage, error) {
	result := make([]apiMessage, 0, len(messages))
	for _, m := range messages {
		if len(m.ImagePaths) == 0 {
			contentJSON, err := json.Marshal(m.Content)
			if err != nil {
				return nil, fmt.Errorf("marshal text content: %w", err)
			}
			result = append(result, apiMessage{Role: m.Role, Content: contentJSON})
			continue
		}

		// Multimodal message: text part + one image_url part per image.
		type textPart struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		type imageURLInner struct {
			URL string `json:"url"`
		}
		type imageURLPart struct {
			Type     string        `json:"type"`
			ImageURL imageURLInner `json:"image_url"`
		}
		parts := make([]interface{}, 0, 1+len(m.ImagePaths))
		if m.Content != "" {
			parts = append(parts, textPart{Type: "text", Text: m.Content})
		}
		for _, imgPath := range m.ImagePaths {
			raw, err := os.ReadFile(imgPath)
			if err != nil {
				continue // skip unreadable images silently
			}
			dataURL := "data:" + imageMediaType(imgPath) + ";base64," +
				base64.StdEncoding.EncodeToString(raw)
			parts = append(parts, imageURLPart{
				Type:     "image_url",
				ImageURL: imageURLInner{URL: dataURL},
			})
		}
		contentJSON, err := json.Marshal(parts)
		if err != nil {
			return nil, fmt.Errorf("marshal multimodal content: %w", err)
		}
		result = append(result, apiMessage{Role: m.Role, Content: contentJSON})
	}
	return result, nil
}

// ToolDefinition describes a function tool available to the model.
type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

// ToolFunctionDefinition describes one callable tool schema.
type ToolFunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Event is a typed Codex stream event.
type Event interface {
	isEvent()
}

// TokenEvent contains streamed text output from the model.
type TokenEvent struct {
	Token string
}

// ToolCallEvent contains streamed function call metadata and arguments.
type ToolCallEvent struct {
	ID        string
	Name      string
	Arguments string
}

// DoneEvent signals that the stream ended successfully.
type DoneEvent struct{}

// ErrorEvent signals that stream processing failed.
type ErrorEvent struct {
	Err error
}

func (TokenEvent) isEvent()    {}
func (ToolCallEvent) isEvent() {}
func (DoneEvent) isEvent()     {}
func (ErrorEvent) isEvent()    {}

type chatCompletionsRequest struct {
	Model    string           `json:"model"`
	Messages []apiMessage     `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
}

type chatCompletionsChunk struct {
	Choices []chatChoiceChunk `json:"choices"`
}

type chatChoiceChunk struct {
	Delta chatDeltaChunk `json:"delta"`
}

type chatDeltaChunk struct {
	Content   string             `json:"content"`
	ToolCalls []chatToolCallPart `json:"tool_calls"`
}

type chatToolCallPart struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Function chatFunctionPart `json:"function"`
}

type chatFunctionPart struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolCallAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

type requestStatusError struct {
	StatusCode int
	Body       string
}

type codexExecEvent struct {
	Type string        `json:"type"`
	Item codexExecItem `json:"item"`
}

type codexExecItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	OutputText       string          `json:"output_text"`
	Delta            string          `json:"delta"`
	Content          json.RawMessage `json:"content"`
	Message          json.RawMessage `json:"message"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
}

func (e requestStatusError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("completions status %d", e.StatusCode)
	}
	if len(body) > 720 {
		body = body[:720] + "..."
	}
	return fmt.Sprintf("completions status %d: %s", e.StatusCode, body)
}

// AvailableModels returns the built-in selectable Codex model options for Orb.
func AvailableModels() []string {
	return []string{
		"gpt-5.4",
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.1-codex-mini",
	}
}

// SetRuntimeModel sets an in-memory model override used for future streams.
func SetRuntimeModel(model string) {
	modelOverrideMu.Lock()
	defer modelOverrideMu.Unlock()
	modelOverride = strings.TrimSpace(model)
}

// SetRuntimeReasoningEffort sets an in-memory reasoning effort override.
func SetRuntimeReasoningEffort(effort string) {
	modelOverrideMu.Lock()
	defer modelOverrideMu.Unlock()
	effortOverride = normalizeReasoningEffort(effort)
}

// SetRuntimeWorkingDir sets the in-memory working directory used by codex exec streams.
func SetRuntimeWorkingDir(path string) {
	clean := strings.TrimSpace(path)
	if clean == "" {
		modelOverrideMu.Lock()
		cwdOverride = ""
		modelOverrideMu.Unlock()
		return
	}
	clean = filepath.Clean(clean)
	modelOverrideMu.Lock()
	cwdOverride = clean
	modelOverrideMu.Unlock()
}

// SetRuntimeExecutionMode sets the in-memory Codex execution mode override.
func SetRuntimeExecutionMode(mode string) {
	modelOverrideMu.Lock()
	defer modelOverrideMu.Unlock()
	execModeOverride = normalizeExecutionMode(mode)
}

// CurrentModel resolves the model currently used by Orb streams.
func CurrentModel() (string, error) {
	return resolveModelName()
}

// CurrentReasoningEffort resolves the reasoning effort currently used by Orb streams.
func CurrentReasoningEffort() (string, error) {
	return resolveReasoningEffort()
}

// CurrentExecutionMode resolves the Codex execution mode currently used by Orb streams.
func CurrentExecutionMode() (string, error) {
	return resolveExecutionMode()
}

// Stream connects to the OpenAI chat completions SSE stream and returns typed events.
func Stream(ctx context.Context, messages []Message, toolDefs []ToolDefinition) <-chan Event {
	events := make(chan Event)

	go func() {
		defer close(events)

		modelName, err := resolveModelName()
		if err != nil {
			emitEvent(ctx, events, ErrorEvent{Err: err})
			return
		}
		reasoningEffort, err := resolveReasoningEffort()
		if err != nil {
			emitEvent(ctx, events, ErrorEvent{Err: err})
			return
		}
		executionMode, err := resolveExecutionMode()
		if err != nil {
			emitEvent(ctx, events, ErrorEvent{Err: err})
			return
		}

		credentials := make([]Credential, 0, 4)
		credentialsErr := error(nil)
		resolvedCredentials, resolveErr := ResolveCandidates()
		if resolveErr != nil {
			if !errors.Is(resolveErr, ErrNoAuth) {
				credentialsErr = resolveErr
			}
		} else {
			credentials = prioritizeStreamCredentials(resolvedCredentials)
		}
		apiCredentials := filterAPICredentials(credentials)
		hasAPIKeyCredential := len(apiCredentials) > 0

		attemptErrors := make([]string, 0, len(credentials)+1)

		if shouldUseCodexCLI() {
			err = streamWithCodexCLI(ctx, modelName, reasoningEffort, executionMode, messages, events)
			if err == nil {
				return
			}
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			attemptErrors = append(attemptErrors, fmt.Sprintf("codex-cli-exec -> %s", err.Error()))
			if !hasAPIKeyCredential {
				emitEvent(ctx, events, ErrorEvent{Err: fmt.Errorf(
					"codex cli execution failed (no API fallback key configured): %s",
					err.Error(),
				)})
				return
			}
		}

		if len(apiCredentials) == 0 {
			if credentialsErr != nil {
				attemptErrors = append(attemptErrors, fmt.Sprintf("resolve codex auth -> %s", credentialsErr.Error()))
			}
			if len(attemptErrors) == 0 {
				emitEvent(ctx, events, ErrorEvent{Err: errors.New(
					"no codex auth found: run `codex login` or set orb api_key/OPENAI_API_KEY/CODEX_API_KEY",
				)})
				return
			}
			emitEvent(ctx, events, ErrorEvent{Err: fmt.Errorf(
				"stream unavailable without API credentials; attempts: %s",
				strings.Join(attemptErrors, " | "),
			)})
			return
		}

		attemptedSources := make([]string, 0, len(apiCredentials))
		for index, credential := range apiCredentials {
			source := strings.TrimSpace(credential.Source)
			if source == "" {
				source = "unknown"
			}
			attemptedSources = append(attemptedSources, source)

			err = streamWithAPI(ctx, modelName, credential, messages, toolDefs, events)
			if err == nil {
				SetCachedCredential(credential.Key, credential.Source)
				return
			}
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return
			}

			var statusErr requestStatusError
			if errors.As(err, &statusErr) && shouldRetryCredential(statusErr) {
				attemptErrors = append(attemptErrors, fmt.Sprintf("%s -> %s", source, statusErr.Error()))
				if isQuotaFromCodexSession(statusErr, source, hasAPIKeyCredential) {
					emitEvent(ctx, events, ErrorEvent{Err: errors.New(
						"codex session quota unavailable for Chat Completions; configure orb api_key or set OPENAI_API_KEY/CODEX_API_KEY",
					)})
					return
				}
				if index < len(apiCredentials)-1 {
					continue
				}
			}

			attemptErrors = append(attemptErrors, fmt.Sprintf("%s -> %s", source, err.Error()))
			emitEvent(ctx, events, ErrorEvent{Err: fmt.Errorf("stream using %s: %w", source, err)})
			return
		}

		if len(attemptErrors) == 0 {
			emitEvent(ctx, events, ErrorEvent{Err: fmt.Errorf("stream failed for credential sources: %s", strings.Join(attemptedSources, ", "))})
			return
		}
		emitEvent(ctx, events, ErrorEvent{Err: fmt.Errorf("stream auth attempts failed: %s", strings.Join(attemptErrors, " | "))})
	}()

	return events
}

func streamWithAPI(
	ctx context.Context,
	modelName string,
	credential Credential,
	messages []Message,
	toolDefs []ToolDefinition,
	events chan<- Event,
) error {
	apiMessages, err := buildAPIMessages(messages)
	if err != nil {
		return fmt.Errorf("build api messages: %w", err)
	}
	requestBody := chatCompletionsRequest{
		Model:    modelName,
		Messages: apiMessages,
		Tools:    toolDefs,
		Stream:   true,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal completions request: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		chatCompletionsEndpoint,
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create completions request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential.Key)
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 90 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("execute completions request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return fmt.Errorf("completions status %d and read body: %w", response.StatusCode, readErr)
		}
		return requestStatusError{
			StatusCode: response.StatusCode,
			Body:       strings.TrimSpace(string(bodyBytes)),
		}
	}

	return consumeSSEStream(ctx, response.Body, events)
}

func streamWithCodexCLI(
	ctx context.Context,
	modelName string,
	reasoningEffort string,
	executionMode string,
	messages []Message,
	events chan<- Event,
) error {
	codexBinary, err := findCodexBinary()
	if err != nil {
		return err
	}

	prompt := buildCodexExecPrompt(messages)
	if strings.TrimSpace(prompt) == "" {
		return errors.New("empty prompt for codex exec")
	}

	streamCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		streamCtx, cancel = context.WithTimeout(ctx, codexExecTimeout)
	}
	defer cancel()

	args := []string{
		"exec",
		"--skip-git-repo-check",
		"--json",
		"--ephemeral",
		"--color",
		"never",
	}
	if normalizeExecutionMode(executionMode) == ExecutionModeUnblocked {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else {
		args = append(args, "--full-auto", "--sandbox", "workspace-write")
	}
	if workingDir := runtimeWorkingDir(); workingDir != "" {
		args = append(args, "--cd", workingDir)
	}
	trimmedModel := strings.TrimSpace(modelName)
	if trimmedModel != "" {
		args = append(args, "--model", trimmedModel)
	}
	if normalizedEffort := normalizeReasoningEffort(reasoningEffort); normalizedEffort != "" {
		args = append(args, "-c", fmt.Sprintf(`model_reasoning_effort=%q`, normalizedEffort))
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(streamCtx, codexBinary, args...)
	if workingDir := runtimeWorkingDir(); workingDir != "" {
		if info, statErr := os.Stat(workingDir); statErr == nil && info.IsDir() {
			cmd.Dir = workingDir
		}
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open codex exec stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open codex exec stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start codex exec: %w", err)
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

	sawTurnCompleted := false
	assistantSnapshots := make(map[string]string)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "{") {
			continue
		}

		var parsed codexExecEvent
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			continue
		}

		switch parsed.Type {
		case "item.started":
			if parsed.Item.Type == "command_execution" {
				toolEvent := ToolCallEvent{
					ID:        strings.TrimSpace(parsed.Item.ID),
					Name:      "shell",
					Arguments: strings.TrimSpace(parsed.Item.Command),
				}
				if !emitEvent(ctx, events, toolEvent) {
					return context.Canceled
				}
			}
		case "item.updated", "item.delta", "item.completed":
			if isExecAssistantMessageType(parsed.Item.Type) {
				text := extractExecAssistantText(parsed.Item)
				if !emitAssistantDelta(ctx, events, strings.TrimSpace(parsed.Item.ID), text, assistantSnapshots) {
					return context.Canceled
				}
			}
			if parsed.Type == "item.completed" && parsed.Item.Type == "command_execution" {
				summary := renderCommandExecutionSummary(parsed.Item)
				if summary != "" {
					toolEvent := ToolCallEvent{
						ID:        strings.TrimSpace(parsed.Item.ID),
						Name:      "shell_result",
						Arguments: summary,
					}
					if !emitEvent(ctx, events, toolEvent) {
						return context.Canceled
					}
				}
			}
		case "turn.completed":
			sawTurnCompleted = true
			if !emitEvent(ctx, events, DoneEvent{}) {
				return context.Canceled
			}
			return nil
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		if errors.Is(scanErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		return fmt.Errorf("read codex exec output: %w", scanErr)
	}

	waitErr := cmd.Wait()
	stderrText := <-stderrDone
	if waitErr != nil {
		if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("codex exec timed out after %s", codexExecTimeout)
		}
		return fmt.Errorf("codex exec failed: %w%s", waitErr, formatCodexExecStderr(stderrText))
	}

	if strings.TrimSpace(stderrText) != "" && !sawTurnCompleted {
		return fmt.Errorf("codex exec produced no completion%s", formatCodexExecStderr(stderrText))
	}

	if !sawTurnCompleted {
		if !emitEvent(ctx, events, DoneEvent{}) {
			return context.Canceled
		}
	}
	return nil
}

func formatCodexExecStderr(stderr string) string {
	clean := strings.TrimSpace(stderr)
	if clean == "" {
		return ""
	}
	if len(clean) > 600 {
		clean = clean[:600] + "..."
	}
	return ": " + clean
}

func renderCommandExecutionSummary(item codexExecItem) string {
	command := strings.TrimSpace(item.Command)
	if command == "" {
		command = "(unknown command)"
	}

	exit := "unknown"
	if item.ExitCode != nil {
		exit = fmt.Sprintf("%d", *item.ExitCode)
	}

	output := strings.TrimSpace(item.AggregatedOutput)
	if output != "" {
		output = truncateForEvent(output, 320)
		return fmt.Sprintf("%s\nexit=%s\noutput=%s", command, exit, output)
	}

	return fmt.Sprintf("%s\nexit=%s", command, exit)
}

func truncateForEvent(text string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	clean := strings.TrimSpace(text)
	if len(clean) <= maxLen {
		return clean
	}
	return clean[:maxLen] + "..."
}

func isExecAssistantMessageType(itemType string) bool {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "agent_message", "assistant_message", "output_text":
		return true
	default:
		return false
	}
}

func emitAssistantDelta(
	ctx context.Context,
	events chan<- Event,
	itemID string,
	text string,
	snapshots map[string]string,
) bool {
	sanitized := sanitizeStreamText(text)
	if strings.TrimSpace(sanitized) == "" {
		return true
	}

	if itemID == "" {
		return emitEvent(ctx, events, TokenEvent{Token: sanitized})
	}

	previous := snapshots[itemID]
	next := sanitized
	if previous != "" {
		switch {
		case strings.HasPrefix(sanitized, previous):
			next = sanitized[len(previous):]
		case strings.HasPrefix(previous, sanitized):
			next = ""
		default:
			next = "\n" + sanitized
		}
	}
	snapshots[itemID] = sanitized
	if strings.TrimSpace(next) == "" {
		return true
	}
	return emitEvent(ctx, events, TokenEvent{Token: next})
}

func extractExecAssistantText(item codexExecItem) string {
	parts := make([]string, 0, 8)
	appendUniqueText(&parts, item.Text)
	appendUniqueText(&parts, item.OutputText)
	appendUniqueText(&parts, item.Delta)
	for _, text := range extractTextFromRawJSON(item.Content) {
		appendUniqueText(&parts, text)
	}
	for _, text := range extractTextFromRawJSON(item.Message) {
		appendUniqueText(&parts, text)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func extractTextFromRawJSON(raw json.RawMessage) []string {
	clean := strings.TrimSpace(string(raw))
	if clean == "" || clean == "null" {
		return nil
	}

	if strings.HasPrefix(clean, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	}

	if strings.HasPrefix(clean, "[") {
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil
		}
		parts := make([]string, 0, len(list))
		for _, item := range list {
			for _, text := range extractTextFromRawJSON(item) {
				appendUniqueText(&parts, text)
			}
		}
		return parts
	}

	if strings.HasPrefix(clean, "{") {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil
		}
		keys := []string{"text", "output_text", "delta", "content", "message", "value", "parts"}
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			value, ok := object[key]
			if !ok {
				continue
			}
			for _, text := range extractTextFromRawJSON(value) {
				appendUniqueText(&parts, text)
			}
		}
		return parts
	}

	return nil
}

func appendUniqueText(parts *[]string, text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	for _, existing := range *parts {
		if existing == trimmed {
			return
		}
	}
	*parts = append(*parts, trimmed)
}

func sanitizeStreamText(text string) string {
	if text == "" {
		return ""
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	var builder strings.Builder
	builder.Grow(len(normalized))
	for _, r := range normalized {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func buildCodexExecPrompt(messages []Message) string {
	var builder strings.Builder
	historyOpened := false

	openHistory := func() {
		if historyOpened {
			builder.WriteString("\n\n")
			return
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString("Conversation history:\n")
		historyOpened = true
	}

	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}

		switch role {
		case "system":
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString("System instructions:\n")
			builder.WriteString(content)
		case "user":
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString("User request:\n")
			builder.WriteString(content)
		case "assistant":
			openHistory()
			builder.WriteString("Assistant reply:\n")
			builder.WriteString(content)
		default:
			openHistory()
			builder.WriteString(codexPromptRoleLabel(role))
			builder.WriteString(" message:\n")
			builder.WriteString(content)
		}
	}

	prompt := strings.TrimSpace(builder.String())
	if prompt == "" {
		return "Respond helpfully to the user request."
	}
	return prompt
}

func codexPromptRoleLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "tool":
		return "Tool"
	case "reasoning":
		return "Reasoning"
	case "error":
		return "Error"
	default:
		return "Message"
	}
}

func consumeSSEStream(ctx context.Context, body io.Reader, events chan<- Event) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	accumulators := map[int]*toolCallAccumulator{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			emitEvent(ctx, events, DoneEvent{})
			return nil
		}

		var chunk chatCompletionsChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode stream chunk: %w", err)
		}

		for _, choice := range chunk.Choices {
			token := choice.Delta.Content
			if token != "" {
				if !emitEvent(ctx, events, TokenEvent{Token: token}) {
					return context.Canceled
				}
			}

			for _, part := range choice.Delta.ToolCalls {
				accumulator := accumulators[part.Index]
				if accumulator == nil {
					accumulator = &toolCallAccumulator{}
					accumulators[part.Index] = accumulator
				}

				if trimmed := strings.TrimSpace(part.ID); trimmed != "" {
					accumulator.id = trimmed
				}
				if trimmed := strings.TrimSpace(part.Function.Name); trimmed != "" {
					accumulator.name = trimmed
				}
				if part.Function.Arguments != "" {
					accumulator.arguments.WriteString(part.Function.Arguments)
				}

				toolEvent := ToolCallEvent{
					ID:        accumulator.id,
					Name:      accumulator.name,
					Arguments: strings.TrimSpace(accumulator.arguments.String()),
				}
				if !emitEvent(ctx, events, toolEvent) {
					return context.Canceled
				}
			}
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		if errors.Is(scanErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		return fmt.Errorf("read stream: %w", scanErr)
	}

	emitEvent(ctx, events, DoneEvent{})
	return nil
}

func shouldRetryCredential(err requestStatusError) bool {
	switch err.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}

	body := strings.ToLower(err.Body)
	retryHints := []string{
		"insufficient_quota",
		"invalid_api_key",
		"authentication",
		"organization",
	}
	for _, hint := range retryHints {
		if strings.Contains(body, hint) {
			return true
		}
	}

	return false
}

func prioritizeStreamCredentials(credentials []Credential) []Credential {
	if len(credentials) < 2 {
		return credentials
	}

	prioritized := make([]Credential, 0, len(credentials))
	for _, credential := range credentials {
		if !isCodexSessionSource(credential.Source) {
			prioritized = append(prioritized, credential)
		}
	}
	for _, credential := range credentials {
		if isCodexSessionSource(credential.Source) {
			prioritized = append(prioritized, credential)
		}
	}
	return prioritized
}

func shouldUseCodexCLI() bool {
	if _, err := findCodexBinary(); err != nil {
		return false
	}
	return true
}

func isCodexSessionSource(source string) bool {
	return strings.HasPrefix(strings.TrimSpace(source), "codex-cli-session")
}

func filterAPICredentials(credentials []Credential) []Credential {
	filtered := make([]Credential, 0, len(credentials))
	for _, credential := range credentials {
		if isCodexSessionSource(credential.Source) {
			continue
		}
		filtered = append(filtered, credential)
	}
	return filtered
}

func isQuotaFromCodexSession(err requestStatusError, source string, hasAPIKeyCredential bool) bool {
	if hasAPIKeyCredential {
		return false
	}
	if !isCodexSessionSource(source) {
		return false
	}
	if err.StatusCode != http.StatusTooManyRequests {
		return false
	}
	return strings.Contains(strings.ToLower(err.Body), "insufficient_quota")
}

func resolveModelName() (string, error) {
	modelOverrideMu.RLock()
	override := strings.TrimSpace(modelOverride)
	modelOverrideMu.RUnlock()
	if override != "" {
		return override, nil
	}

	cfg, err := config.Load(configPathOrDefault(""))
	if err != nil {
		return "", fmt.Errorf("load orb config for model resolution: %w", err)
	}
	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		return "gpt-5.4", nil
	}
	if strings.EqualFold(modelName, "codex") {
		// "codex" is treated as a friendly alias to a current Codex-capable model.
		return "gpt-5.4", nil
	}
	return modelName, nil
}

func resolveReasoningEffort() (string, error) {
	modelOverrideMu.RLock()
	override := normalizeReasoningEffort(effortOverride)
	modelOverrideMu.RUnlock()
	if override != "" {
		return override, nil
	}

	cfg, err := config.Load(configPathOrDefault(""))
	if err != nil {
		return "", fmt.Errorf("load orb config for reasoning effort resolution: %w", err)
	}
	configured := normalizeReasoningEffort(cfg.ReasoningEffort)
	if configured != "" {
		return configured, nil
	}
	return "medium", nil
}

func resolveExecutionMode() (string, error) {
	modelOverrideMu.RLock()
	rawOverride := strings.TrimSpace(execModeOverride)
	modelOverrideMu.RUnlock()
	if rawOverride != "" {
		return normalizeExecutionMode(rawOverride), nil
	}

	cfg, err := config.Load(configPathOrDefault(""))
	if err != nil {
		return "", fmt.Errorf("load orb config for execution mode resolution: %w", err)
	}
	mode := normalizeExecutionMode(cfg.Codex.ExecutionMode)
	if mode == "" {
		return ExecutionModeUnblocked, nil
	}
	return mode, nil
}

func normalizeReasoningEffort(value string) string {
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

func normalizeExecutionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ExecutionModeSandboxed:
		return ExecutionModeSandboxed
	case ExecutionModeUnblocked:
		return ExecutionModeUnblocked
	default:
		return ExecutionModeUnblocked
	}
}

func runtimeWorkingDir() string {
	modelOverrideMu.RLock()
	defer modelOverrideMu.RUnlock()
	return strings.TrimSpace(cwdOverride)
}

func emitEvent(ctx context.Context, stream chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case stream <- event:
		return true
	}
}

func findCodexBinary() (string, error) {
	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}

	candidates := []string{
		"/opt/homebrew/bin/codex",
		"/usr/local/bin/codex",
		"/usr/bin/codex",
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}

	return "", errors.New("codex binary not found in PATH or common install locations")
}
