package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/willdev/orb/internal/agent"
	"github.com/willdev/orb/internal/backend"
	"github.com/willdev/orb/internal/config"
	"github.com/willdev/orb/internal/gitops"
	"github.com/willdev/orb/internal/store"
	"github.com/willdev/orb/internal/tui/keys"
	"github.com/willdev/orb/internal/tui/theme"
)

const (
	gitRefreshInterval  = 3 * time.Second
	gitRefreshTimeout   = 4 * time.Second
	worktreeTimeout     = 30 * time.Second
	splashFrameDelay    = 112 * time.Millisecond
	splashFrameCount    = 8
	streamWatchInterval = 1 * time.Second
	streamStallTimeout  = 90 * time.Second
	defaultScrollSpeed  = 3
	minLayoutWidth      = 60
	inputPrompt         = "> "
	fastModeModel       = "gpt-5.1-codex-mini"
)

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlayAgents
	overlaySessions
	overlayDiff
	overlayGit
	overlayActivity
	overlayModels
	overlayCommandPalette
)

type threadEntryKind int

const (
	threadUser threadEntryKind = iota
	threadAssistant
	threadTool
	threadReasoning
	threadSystem
)

type slashAction int

const (
	actionNewSession slashAction = iota
	actionAgents
	actionNewAgent
	actionResume
	actionSessions
	actionDiff
	actionGit
	actionLog
	actionModels
	actionWorktree
	actionCompact
	actionExport
	actionSteer
	actionFast
	actionHelp
	actionExit
	actionOpenEditor
)

type composeMode int

const (
	composeModeAgent composeMode = iota
	composeModePlan
	composeModeTerminal
)

type threadEntry struct {
	Kind       threadEntryKind
	Text       string
	Timestamp  time.Time
	ToolID     string
	ToolName   string
	ToolArgs   string
	ToolStatus string
	ToolResult string
	IsError    bool
}

type activityEntry struct {
	Timestamp time.Time
	Kind      string
	Text      string
	Success   bool
}

type planItem struct {
	Text string
	Done bool
}

type overlayItem struct {
	Title       string
	Description string
	Meta        string
	Value       string
	Action      slashAction
}

type slashCommand struct {
	Command     string
	Description string
	Keybind     string
	Action      slashAction
}

type reasoningModeOption struct {
	Label       string
	Effort      string
	Description string
}

type model struct {
	width  int
	height int
	ready  bool

	showSplash  bool
	splashFrame int

	version       string
	configPath    string
	authStatus    string
	authConnected bool
	backendID     backend.ID
	backendClient backend.Backend
	repoBase      string
	branch        string
	gitStatus     string
	diffLines     []string

	taskStore *store.Store
	tasks     []store.Task
	taskIndex int
	taskID    string
	agentPool *agent.Pool
	taskAgent map[string]string

	entries   []threadEntry
	activity  []activityEntry
	planItems []planItem

	viewport    viewport.Model
	sticky      bool
	scrollSpeed int

	input textarea.Model

	keys      keys.KeyMap
	helpModel help.Model

	leaderPending bool
	overlay       overlayKind
	overlayItems  []overlayItem
	overlayIndex  int
	overlayFilter string
	overlayLines  []string
	overlayScroll int
	resumeOnly    bool

	commands      []slashCommand
	showSlashMenu bool
	slashItems    []slashCommand
	slashIndex    int
	promptHistory []string
	historyIndex  int
	historyDraft  string

	streaming               bool
	streamCh                <-chan backend.Event
	cancelFn                context.CancelFunc
	streamBuffer            string
	streamingAssistantIndex int
	runningToolByID         map[string]int
	queuedPrompts           []string
	thinking                bool
	thinkingEntryIndex      int
	thinkingFrame           int
	thinkingStartedAt       time.Time
	streamStartedAt         time.Time
	lastStreamEventAt       time.Time

	currentModel string
	currentMode  string
	composeMode  composeMode
	activeAgent  string
}

type splashTickMsg struct{}

type gitTickMsg struct{}

type thinkingTickMsg struct{}

type streamWatchTickMsg struct{}

type gitRefreshedMsg struct {
	snapshot gitops.Snapshot
	err      error
}

type streamStartedMsg struct {
	stream <-chan backend.Event
	cancel context.CancelFunc
}

type streamEventMsg struct {
	event backend.Event
}

type streamEndedMsg struct{}

type shellResultMsg struct {
	toolID   string
	command  string
	output   string
	exitCode int
	err      error
	assist   bool
}

var (
	ansiOSCSequence = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiCSISequence = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiESCSequence = regexp.MustCompile(`\x1b[@-_]`)
)

// New returns Orb's OpenCode-style root Bubble Tea model.
func New(
	version string,
	authStatus string,
	taskStore *store.Store,
	repoBasePath string,
	configPath string,
	scrollSpeed int,
) tea.Model {
	basePath := strings.TrimSpace(repoBasePath)
	if basePath == "" {
		cwd, err := os.Getwd()
		if err == nil {
			basePath = cwd
		}
	}

	input := textarea.New()
	input.Placeholder = composeModePlaceholder(composeModeAgent)
	input.Prompt = inputPrompt
	promptWidth := ansi.StringWidth(inputPrompt)
	input.SetPromptFunc(promptWidth, func(lineIdx int) string {
		if lineIdx == 0 {
			return inputPrompt
		}
		return strings.Repeat(" ", promptWidth)
	})
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.SetHeight(1)
	input.MaxHeight = 12
	input.KeyMap.InsertNewline.SetEnabled(false)
	input.FocusedStyle.Base = lipgloss.NewStyle().Background(theme.BG1).Foreground(theme.Grey4)
	input.FocusedStyle.Text = theme.InputText
	input.FocusedStyle.Prompt = theme.InputPrompt
	input.FocusedStyle.Placeholder = theme.InputHint
	input.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(theme.BG1)
	input.BlurredStyle = input.FocusedStyle

	resolvedScrollSpeed := scrollSpeed
	if resolvedScrollSpeed <= 0 {
		resolvedScrollSpeed = defaultScrollSpeed
	}

	m := model{
		showSplash:              true,
		splashFrame:             0,
		version:                 version,
		configPath:              strings.TrimSpace(configPath),
		authStatus:              authStatus,
		authConnected:           !strings.Contains(strings.ToLower(authStatus), "no auth"),
		backendID:               backend.CodexID,
		repoBase:                basePath,
		branch:                  "n/a",
		gitStatus:               "status unavailable",
		diffLines:               []string{"working tree clean"},
		taskStore:               taskStore,
		taskIndex:               0,
		agentPool:               agent.NewPool(),
		taskAgent:               make(map[string]string),
		entries:                 make([]threadEntry, 0, 64),
		activity:                make([]activityEntry, 0, 128),
		planItems:               make([]planItem, 0, 12),
		viewport:                viewport.New(0, 0),
		sticky:                  true,
		scrollSpeed:             resolvedScrollSpeed,
		input:                   input,
		keys:                    keys.Default(),
		helpModel:               help.New(),
		overlay:                 overlayNone,
		overlayItems:            []overlayItem{},
		overlayLines:            []string{},
		commands:                defaultSlashCommands(),
		slashItems:              []slashCommand{},
		promptHistory:           []string{},
		historyIndex:            0,
		historyDraft:            "",
		streamingAssistantIndex: -1,
		runningToolByID:         make(map[string]int),
		queuedPrompts:           []string{},
		thinkingEntryIndex:      -1,
		currentModel:            "gpt-5.4",
		currentMode:             "medium",
		composeMode:             composeModeAgent,
	}
	m.applyComposeMode()
	if err := m.loadBackendFromConfig(); err != nil {
		m.appendSystemEntry("backend setup error: "+err.Error(), true)
	}

	if err := m.reloadTasks(); err != nil {
		m.appendSystemEntry("task load error: "+err.Error(), true)
	}
	m.syncAgentsWithTasks()

	// Orb starts a fresh chat session every launch. Older sessions remain available via /resume.
	if err := m.createTaskWithName("Task " + time.Now().Format("2006-01-02 15:04:05")); err != nil {
		m.appendSystemEntry("task create error: "+err.Error(), true)
	} else {
		m.appendActivity("system", "new startup session created", true)
	}
	if err := m.reloadTasks(); err != nil {
		m.appendSystemEntry("task refresh error: "+err.Error(), true)
	}
	m.syncAgentsWithTasks()
	if len(m.tasks) > 0 {
		selected := 0
		for idx := range m.tasks {
			if m.tasks[idx].ID == m.taskID {
				selected = idx
				break
			}
		}
		m.selectTaskByIndex(selected)
	}
	m.syncAgentsWithTasks()
	if err := m.loadThreadForCurrentTask(); err != nil {
		m.appendSystemEntry("thread load error: "+err.Error(), true)
	}
	m.appendActivity("system", "orb initialized", true)
	m.refreshViewport(true)

	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.input.Focus(),
		refreshGitCmd(m.currentGitPath()),
		gitTickCmd(),
		tea.Tick(splashFrameDelay, func(time.Time) tea.Msg {
			return splashTickMsg{}
		}),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.ready = true
		m.syncInputSize()
		m.resizeViewport()
		m.refreshViewport(false)
		return m, nil
	case splashTickMsg:
		m.splashFrame++
		if m.splashFrame >= splashFrameCount {
			m.showSplash = false
			m.refreshViewport(false)
			return m, nil
		}
		return m, tea.Tick(splashFrameDelay, func(time.Time) tea.Msg {
			return splashTickMsg{}
		})
	case gitTickMsg:
		return m, tea.Batch(refreshGitCmd(m.currentGitPath()), gitTickCmd())
	case gitRefreshedMsg:
		if typed.err != nil {
			m.branch = "n/a"
			m.gitStatus = "status unavailable"
			m.diffLines = []string{typed.err.Error()}
			m.appendActivity("system", "git refresh error: "+typed.err.Error(), false)
			return m, nil
		}

		m.branch = typed.snapshot.Branch
		m.gitStatus = typed.snapshot.StatusText
		if len(typed.snapshot.DiffLines) == 0 {
			m.diffLines = []string{"working tree clean"}
		} else {
			m.diffLines = typed.snapshot.DiffLines
		}
		return m, nil
	case streamStartedMsg:
		m.streamCh = typed.stream
		m.cancelFn = typed.cancel
		m.streaming = true
		m.streamBuffer = ""
		m.streamingAssistantIndex = -1
		now := time.Now()
		m.streamStartedAt = now
		m.lastStreamEventAt = now
		m.appendActivity("system", "stream started", true)
		m.setActiveAgentStatus(agent.StatusThinking)
		m.startThinkingStatus()
		return m, tea.Batch(waitForStreamEventCmd(m.streamCh), thinkingTickCmd(), streamWatchTickCmd())
	case streamEventMsg:
		m.lastStreamEventAt = time.Now()
		if m.handleStreamEvent(typed.event) {
			return m, waitForStreamEventCmd(m.streamCh)
		}
		m.streaming = false
		m.streamCh = nil
		m.cancelFn = nil
		m.streamingAssistantIndex = -1
		m.thinking = false
		m.thinkingEntryIndex = -1
		m.thinkingStartedAt = time.Time{}
		m.streamStartedAt = time.Time{}
		m.lastStreamEventAt = time.Time{}
		return m, m.dequeueNextPromptCmd()
	case streamEndedMsg:
		m.streaming = false
		m.streamCh = nil
		m.cancelFn = nil
		m.streamingAssistantIndex = -1
		m.thinking = false
		m.thinkingEntryIndex = -1
		m.thinkingStartedAt = time.Time{}
		m.streamStartedAt = time.Time{}
		m.lastStreamEventAt = time.Time{}
		m.setActiveAgentStatus(agent.StatusIdle)
		m.appendActivity("system", "stream ended", true)
		return m, m.dequeueNextPromptCmd()
	case thinkingTickMsg:
		if !m.streaming || !m.thinking {
			return m, nil
		}
		m.advanceThinkingStatus()
		return m, thinkingTickCmd()
	case streamWatchTickMsg:
		if !m.streaming {
			return m, nil
		}

		lastEventAt := m.lastStreamEventAt
		if lastEventAt.IsZero() {
			lastEventAt = m.streamStartedAt
		}
		if lastEventAt.IsZero() {
			lastEventAt = time.Now()
		}
		silentFor := time.Since(lastEventAt)
		if silentFor >= streamStallTimeout {
			if m.cancelFn != nil {
				m.cancelFn()
			}
			m.cancelFn = nil
			m.streaming = false
			m.streamCh = nil
			m.streamingAssistantIndex = -1
			m.thinking = false
			m.thinkingEntryIndex = -1
			m.thinkingStartedAt = time.Time{}
			m.lastStreamEventAt = time.Time{}
			m.streamBuffer = ""
			for idx := range m.entries {
				if m.entries[idx].Kind == threadTool && m.entries[idx].ToolStatus == "running" {
					m.entries[idx].ToolStatus = "error"
				}
			}
			m.runningToolByID = make(map[string]int)
			m.setActiveAgentStatus(agent.StatusIdle)
			elapsed := formatElapsedDuration(time.Since(m.streamStartedAt))
			m.streamStartedAt = time.Time{}
			m.appendActivity("error", "stream stalled and was canceled", false)
			m.appendSystemEntry("stream stalled (no events for "+formatElapsedDuration(silentFor)+", total "+elapsed+"); canceled", true)
			return m, m.dequeueNextPromptCmd()
		}
		m.refreshAgentsOverlayIfOpen()
		return m, streamWatchTickCmd()
	case shellResultMsg:
		cmd := m.handleShellResult(typed)
		return m, cmd
	case tea.KeyMsg:
		cmd, quit := m.handleKey(typed)
		if quit {
			if m.cancelFn != nil {
				m.cancelFn()
				m.cancelFn = nil
			}
			return m, tea.Quit
		}
		return m, cmd
	default:
		if m.overlay == overlayNone {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m model) View() string {
	if !m.ready {
		return ""
	}
	if m.showSplash {
		return renderSplash(m.width, m.height, m.splashFrame)
	}

	header := m.renderHeader()
	inputBar := m.renderInputBar()
	layout := computeZoneLayout(m.width, m.height, lipgloss.Height(header), lipgloss.Height(inputBar))
	content := m.renderContent(layout.ContentHeight)

	return renderShell(m.width, m.height, header, content, inputBar)
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if msg.String() == "ctrl+c" {
		return nil, true
	}

	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}

	if m.leaderPending {
		m.leaderPending = false
		return m.handleLeaderKey(msg)
	}

	if key.Matches(msg, m.keys.Leader) {
		m.leaderPending = true
		return nil, false
	}

	if key.Matches(msg, m.keys.Help) {
		m.openHelpOverlay()
		return nil, false
	}

	if key.Matches(msg, m.keys.CommandPalette) {
		m.openCommandPaletteOverlay()
		return nil, false
	}
	if key.Matches(msg, m.keys.ComposeMode) && !m.showSlashMenu {
		m.cycleComposeMode(true)
		return nil, false
	}
	if msg.String() == "shift+tab" && !m.showSlashMenu {
		m.cycleComposeMode(false)
		return nil, false
	}

	switch msg.String() {
	case "G":
		m.sticky = true
		m.viewport.GotoBottom()
		return nil, false
	case "up":
		if !m.showSlashMenu {
			m.cycleHistoryUp()
			return nil, false
		}
	case "down":
		if !m.showSlashMenu {
			m.cycleHistoryDown()
			return nil, false
		}
	case "ctrl+u":
		step := maxInt(1, (m.viewport.Height/2)*maxInt(1, m.scrollSpeed))
		m.viewport.LineUp(step)
		m.sticky = false
		return nil, false
	case "ctrl+d":
		step := maxInt(1, (m.viewport.Height/2)*maxInt(1, m.scrollSpeed))
		m.viewport.LineDown(step)
		if m.viewport.AtBottom() {
			m.sticky = true
		}
		return nil, false
	}

	if m.showSlashMenu {
		switch msg.String() {
		case "up", "k":
			m.slashIndex = clampInt(m.slashIndex-1, 0, len(m.slashItems)-1)
			return nil, false
		case "down", "j":
			m.slashIndex = clampInt(m.slashIndex+1, 0, len(m.slashItems)-1)
			return nil, false
		case "tab":
			if len(m.slashItems) > 0 {
				m.input.SetValue(m.slashItems[m.slashIndex].Command)
				m.input.CursorEnd()
				m.updateSlashMenu()
			}
			return nil, false
		case "enter":
			if len(m.slashItems) > 0 {
				selected := m.slashItems[m.slashIndex]
				m.input.Reset()
				m.syncInputSize()
				m.updateSlashMenu()
				return m.executeCommandAction(selected.Action), false
			}
		}
	}

	if key.Matches(msg, m.keys.Send) || msg.String() == "enter" {
		return m.sendCurrentPrompt(), false
	}

	focusCmd := tea.Cmd(nil)
	if !m.input.Focused() {
		focusCmd = m.input.Focus()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncInputSize()
	m.resizeViewport()
	m.refreshViewport(false)
	m.updateSlashMenu()
	if focusCmd != nil {
		return tea.Batch(focusCmd, cmd), false
	}
	return cmd, false
}

func (m *model) handleLeaderKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if key.Matches(msg, m.keys.Quit) || msg.String() == "q" {
		return nil, true
	}
	if msg.String() == "h" {
		m.openHelpOverlay()
		return nil, false
	}
	if key.Matches(msg, m.keys.NewSession) {
		return m.executeCommandAction(actionNewSession), false
	}
	if key.Matches(msg, m.keys.Agents) {
		return m.executeCommandAction(actionAgents), false
	}
	if key.Matches(msg, m.keys.Sessions) {
		return m.executeCommandAction(actionSessions), false
	}
	if key.Matches(msg, m.keys.Diff) {
		return m.executeCommandAction(actionDiff), false
	}
	if key.Matches(msg, m.keys.Git) {
		return m.executeCommandAction(actionGit), false
	}
	if key.Matches(msg, m.keys.Log) {
		return m.executeCommandAction(actionLog), false
	}
	if key.Matches(msg, m.keys.ModelSwitch) {
		return m.executeCommandAction(actionModels), false
	}
	if key.Matches(msg, m.keys.Worktree) {
		return m.executeCommandAction(actionWorktree), false
	}
	if key.Matches(msg, m.keys.Compact) {
		return m.executeCommandAction(actionCompact), false
	}
	if key.Matches(msg, m.keys.Export) {
		return m.executeCommandAction(actionExport), false
	}
	if key.Matches(msg, m.keys.OpenEditor) {
		return m.executeCommandAction(actionOpenEditor), false
	}

	m.appendActivity("system", "unknown leader key: "+msg.String(), false)
	return nil, false
}

func (m *model) handleOverlayKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if msg.String() == "esc" {
		m.closeOverlay()
		return m.input.Focus(), false
	}

	switch m.overlay {
	case overlayAgents, overlaySessions, overlayModels, overlayCommandPalette:
		switch msg.String() {
		case "up", "k":
			m.overlayIndex = clampInt(m.overlayIndex-1, 0, len(m.overlayItems)-1)
			return nil, false
		case "down", "j":
			m.overlayIndex = clampInt(m.overlayIndex+1, 0, len(m.overlayItems)-1)
			return nil, false
		case "enter":
			return m.handleOverlaySelection(), false
		case "backspace":
			if m.overlay == overlayCommandPalette && m.overlayFilter != "" {
				m.overlayFilter = m.overlayFilter[:len(m.overlayFilter)-1]
				m.rebuildCommandPaletteOverlay()
			}
			return nil, false
		default:
			if m.overlay == overlayCommandPalette && msg.Type == tea.KeyRunes {
				m.overlayFilter += string(msg.Runes)
				m.rebuildCommandPaletteOverlay()
				return nil, false
			}
		}
	case overlayHelp:
		return nil, false
	case overlayDiff, overlayGit, overlayActivity:
		switch msg.String() {
		case "up", "k":
			m.overlayScroll = clampInt(m.overlayScroll-1, 0, maxInt(0, len(m.overlayLines)-1))
			return nil, false
		case "down", "j":
			m.overlayScroll = clampInt(m.overlayScroll+1, 0, maxInt(0, len(m.overlayLines)-1))
			return nil, false
		case "ctrl+u":
			m.overlayScroll = clampInt(m.overlayScroll-maxInt(1, m.height/4), 0, maxInt(0, len(m.overlayLines)-1))
			return nil, false
		case "ctrl+d":
			m.overlayScroll = clampInt(m.overlayScroll+maxInt(1, m.height/4), 0, maxInt(0, len(m.overlayLines)-1))
			return nil, false
		}
	}

	return nil, false
}

func (m *model) handleOverlaySelection() tea.Cmd {
	if len(m.overlayItems) == 0 {
		m.closeOverlay()
		return m.input.Focus()
	}
	selected := m.overlayItems[clampInt(m.overlayIndex, 0, len(m.overlayItems)-1)]

	switch m.overlay {
	case overlayAgents:
		m.closeOverlay()
		if selected.Action == actionNewAgent {
			return tea.Batch(m.executeCommandAction(actionNewSession), m.input.Focus())
		}
		agentID := strings.TrimSpace(selected.Value)
		if agentID == "" || m.agentPool == nil {
			return m.input.Focus()
		}
		snapshot, ok := m.agentPool.Get(agentID)
		if !ok {
			m.appendActivity("system", "agent not found: "+agentID, false)
			return m.input.Focus()
		}
		m.agentPool.SetActive(agentID)
		m.activeAgent = agentID
		targetBackend := backend.NormalizeID(snapshot.BackendID)
		if targetBackend != m.backendID {
			if err := m.switchBackend(targetBackend, false); err != nil {
				m.appendActivity("system", "agent backend switch failed: "+err.Error(), false)
			}
		}
		if strings.TrimSpace(snapshot.TaskID) != "" {
			for idx := range m.tasks {
				if m.tasks[idx].ID == snapshot.TaskID {
					m.selectTaskByIndex(idx)
					m.appendActivity("system", "switched agent: "+snapshot.Name, true)
					return tea.Batch(m.input.Focus(), refreshGitCmd(m.currentGitPath()))
				}
			}
		}
		return m.input.Focus()
	case overlaySessions:
		m.closeOverlay()
		for idx := range m.tasks {
			if m.tasks[idx].ID == selected.Value {
				m.selectTaskByIndex(idx)
				m.appendActivity("system", "switched session: "+m.tasks[idx].Name, true)
				return tea.Batch(m.input.Focus(), refreshGitCmd(m.currentGitPath()))
			}
		}
		return m.input.Focus()
	case overlayModels:
		value := strings.TrimSpace(selected.Value)
		switch {
		case strings.HasPrefix(value, "backend:"):
			backendID := backend.NormalizeID(strings.TrimSpace(strings.TrimPrefix(value, "backend:")))
			if err := m.switchBackend(backendID, true); err != nil {
				m.appendSystemEntry("backend switch failed: "+err.Error(), true)
				return nil
			}
			m.appendActivity("system", "backend switched to "+string(backendID), true)
			m.appendSystemEntry("backend switched to "+string(backendID), false)
		case strings.HasPrefix(value, "model:"):
			modelName := strings.TrimSpace(strings.TrimPrefix(value, "model:"))
			if modelName != "" && m.backendClient != nil {
				m.backendClient.SetRuntimeModel(modelName)
				m.currentModel = modelName
				m.appendActivity("system", "model switched to "+modelName, true)
				m.appendSystemEntry("model switched to "+modelName, false)
			}
		case strings.HasPrefix(value, "effort:"):
			effort := strings.TrimSpace(strings.TrimPrefix(value, "effort:"))
			if effort != "" && m.backendClient != nil {
				m.backendClient.SetRuntimeReasoningEffort(effort)
				m.currentMode = effort
				label := reasoningModeLabel(effort)
				m.appendActivity("system", "reasoning mode switched to "+label, true)
				m.appendSystemEntry("reasoning mode: "+label, false)
			}
		}
		m.closeOverlay()
		return m.input.Focus()
	case overlayCommandPalette:
		m.closeOverlay()
		return tea.Batch(m.executeCommandAction(selected.Action), m.input.Focus())
	default:
		m.closeOverlay()
		return m.input.Focus()
	}
}

func (m *model) sendCurrentPrompt() tea.Cmd {
	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" {
		return nil
	}

	m.input.Reset()
	m.syncInputSize()
	m.resizeViewport()
	m.updateSlashMenu()

	if strings.HasPrefix(prompt, "/") {
		command := strings.Fields(prompt)
		if len(command) == 0 {
			return nil
		}
		switch command[0] {
		case "/steer":
			steerPrompt := strings.TrimSpace(strings.TrimPrefix(prompt, "/steer"))
			if steerPrompt == "" {
				m.appendSystemEntry("usage: /steer <message>", true)
				return nil
			}
			return m.submitPrompt(steerPrompt, true, composeModeAgent)
		case "/fast":
			return m.executeCommandAction(actionFast)
		default:
			for _, slash := range m.commands {
				if slash.Command == command[0] {
					return m.executeCommandAction(slash.Action)
				}
			}
			if m.composeMode == composeModeTerminal {
				return m.submitPrompt(prompt, false, m.composeMode)
			}
			m.appendSystemEntry("unknown command: "+command[0], true)
			return nil
		}
	}

	return m.submitPrompt(prompt, false, m.composeMode)
}

func (m *model) submitPrompt(prompt string, steer bool, mode composeMode) tea.Cmd {
	cleanPrompt := strings.TrimSpace(prompt)
	if cleanPrompt == "" {
		return nil
	}

	m.recordPromptHistory(cleanPrompt)
	m.appendEntry(threadEntry{Kind: threadUser, Text: cleanPrompt, Timestamp: time.Now()})
	m.persistMessage("user", cleanPrompt)
	m.appendActivity("user", "prompt sent ("+composeModeLabel(mode)+")", true)

	if mode == composeModeTerminal {
		command := cleanPrompt
		if strings.HasPrefix(command, "!") {
			command = strings.TrimSpace(strings.TrimPrefix(command, "!"))
		}
		return m.submitShellCommand(command, true)
	}

	if strings.HasPrefix(cleanPrompt, "!") {
		command := strings.TrimSpace(strings.TrimPrefix(cleanPrompt, "!"))
		return m.submitShellCommand(command, false)
	}

	finalPrompt := cleanPrompt
	if mode == composeModePlan {
		finalPrompt = buildPlanModePrompt(cleanPrompt)
	}

	if m.streaming && !steer {
		m.queuedPrompts = append(m.queuedPrompts, finalPrompt)
		m.appendActivity("system", fmt.Sprintf("queued prompt (%d)", len(m.queuedPrompts)), true)
		m.appendSystemEntry(
			fmt.Sprintf("queued message #%d (%s mode)", len(m.queuedPrompts), composeModeLabel(mode)),
			false,
		)
		return nil
	}

	if steer && m.streaming {
		if m.cancelFn != nil {
			m.cancelFn()
		}
		m.streaming = false
		m.streamCh = nil
		m.cancelFn = nil
		m.streamingAssistantIndex = -1
		m.thinking = false
		m.thinkingEntryIndex = -1
		m.thinkingFrame = 0
		m.thinkingStartedAt = time.Time{}
		m.streamStartedAt = time.Time{}
		m.lastStreamEventAt = time.Time{}
		m.streamBuffer = ""
		m.setActiveAgentStatus(agent.StatusIdle)
		m.appendActivity("system", "steer: interrupted current stream", true)
		m.appendSystemEntry("steer: applying new direction", false)
	}

	return m.startStreamCmd(finalPrompt)
}

func (m *model) submitShellCommand(command string, requestAssist bool) tea.Cmd {
	cleanCommand := strings.TrimSpace(command)
	if cleanCommand == "" {
		m.appendSystemEntry("shell command is empty", true)
		return nil
	}

	toolID := fmt.Sprintf("shell-%d", time.Now().UnixNano())
	m.appendEntry(threadEntry{
		Kind:       threadTool,
		ToolID:     toolID,
		ToolName:   "shell",
		ToolArgs:   cleanCommand,
		ToolStatus: "running",
		Timestamp:  time.Now(),
	})
	m.setActiveAgentStatus(agent.StatusTool)
	m.appendActivity("tool", "shell running: "+cleanCommand, true)
	return runShellCommandCmd(m.currentGitPath(), toolID, cleanCommand, requestAssist)
}

func (m *model) applyComposeMode() {
	m.input.Placeholder = composeModePlaceholder(m.composeMode)
}

func (m *model) loadBackendFromConfig() error {
	selectedBackend, err := backend.ResolveConfiguredID(m.configPath)
	if err != nil {
		if switchErr := m.switchBackend(backend.CodexID, false); switchErr != nil {
			return fmt.Errorf("fallback to codex backend after config error: %w", switchErr)
		}
		return fmt.Errorf("resolve backend from config: %w", err)
	}
	return m.switchBackend(selectedBackend, false)
}

func (m *model) switchBackend(id backend.ID, persist bool) error {
	selected := backend.NormalizeID(string(id))
	client, err := backend.New(selected, m.configPath)
	if err != nil {
		return fmt.Errorf("initialize backend %q: %w", selected, err)
	}

	m.backendClient = client
	m.backendID = selected

	status, connected, statusErr := client.AuthStatus()
	if statusErr != nil {
		m.appendActivity("error", "backend auth status failed: "+statusErr.Error(), false)
		status = "● no auth"
		connected = false
	}
	if strings.TrimSpace(status) != "" {
		m.authStatus = status
	}
	m.authConnected = connected

	modelName, modelErr := client.CurrentModel()
	if modelErr != nil {
		m.appendActivity("error", "backend model resolution failed: "+modelErr.Error(), false)
		modelName = defaultModelForBackend(selected)
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = defaultModelForBackend(selected)
	}
	m.currentModel = modelName

	reasoningEffort, effortErr := client.CurrentReasoningEffort()
	if effortErr != nil {
		m.appendActivity("error", "backend reasoning mode resolution failed: "+effortErr.Error(), false)
		reasoningEffort = "medium"
	}
	reasoningEffort = strings.TrimSpace(reasoningEffort)
	if reasoningEffort == "" {
		reasoningEffort = "medium"
	}
	m.currentMode = reasoningEffort
	m.syncAgentsWithTasks()
	if strings.TrimSpace(m.activeAgent) != "" && m.agentPool != nil {
		m.agentPool.SetBackend(m.activeAgent, string(selected))
	}

	if persist {
		if err := config.SetBackend(m.configPath, string(selected)); err != nil {
			return fmt.Errorf("persist backend selection %q: %w", selected, err)
		}
	}
	return nil
}

func (m *model) cycleComposeMode(forward bool) {
	modeCount := 3
	current := int(m.composeMode)
	if forward {
		current = (current + 1) % modeCount
	} else {
		current = (current - 1 + modeCount) % modeCount
	}
	m.composeMode = composeMode(current)
	m.applyComposeMode()
	m.syncInputSize()
	m.resizeViewport()
	m.updateSlashMenu()
	m.appendActivity("system", "compose mode: "+composeModeLabel(m.composeMode), true)
}

func (m *model) recordPromptHistory(prompt string) {
	clean := strings.TrimSpace(prompt)
	if clean == "" {
		return
	}
	if len(m.promptHistory) == 0 || m.promptHistory[len(m.promptHistory)-1] != clean {
		m.promptHistory = append(m.promptHistory, clean)
		if len(m.promptHistory) > 200 {
			m.promptHistory = m.promptHistory[len(m.promptHistory)-200:]
		}
	}
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
}

func (m *model) cycleHistoryUp() {
	if len(m.promptHistory) == 0 {
		return
	}
	if m.historyIndex >= len(m.promptHistory) {
		m.historyDraft = m.input.Value()
		m.historyIndex = len(m.promptHistory) - 1
	} else if m.historyIndex > 0 {
		m.historyIndex--
	}
	m.input.SetValue(m.promptHistory[m.historyIndex])
	m.input.CursorEnd()
	m.syncInputSize()
	m.updateSlashMenu()
}

func (m *model) cycleHistoryDown() {
	if len(m.promptHistory) == 0 {
		return
	}
	if m.historyIndex < len(m.promptHistory)-1 {
		m.historyIndex++
		m.input.SetValue(m.promptHistory[m.historyIndex])
		m.input.CursorEnd()
		m.syncInputSize()
		m.updateSlashMenu()
		return
	}
	m.historyIndex = len(m.promptHistory)
	m.input.SetValue(m.historyDraft)
	m.input.CursorEnd()
	m.syncInputSize()
	m.updateSlashMenu()
}

func (m *model) dequeueNextPromptCmd() tea.Cmd {
	if m.streaming || len(m.queuedPrompts) == 0 {
		return nil
	}
	for len(m.queuedPrompts) > 0 {
		next := strings.TrimSpace(m.queuedPrompts[0])
		m.queuedPrompts = m.queuedPrompts[1:]
		if next == "" {
			continue
		}
		m.appendActivity("system", fmt.Sprintf("sending queued prompt (%d left)", len(m.queuedPrompts)), true)
		return m.startStreamCmd(next)
	}
	return nil
}

func (m *model) executeCommandAction(action slashAction) tea.Cmd {
	switch action {
	case actionNewSession:
		name := "Task " + time.Now().Format("2006-01-02 15:04:05")
		if err := m.createTaskWithName(name); err != nil {
			m.appendSystemEntry("new session failed: "+err.Error(), true)
			return nil
		}
		if err := m.reloadTasks(); err != nil {
			m.appendSystemEntry("session refresh failed: "+err.Error(), true)
			return nil
		}
		m.syncAgentsWithTasks()
		m.selectTaskByIndex(0)
		m.syncAgentsWithTasks()
		m.appendActivity("system", "new session created", true)
		return refreshGitCmd(m.currentGitPath())
	case actionAgents:
		m.openAgentsOverlay()
	case actionResume:
		m.openResumeOverlay()
	case actionSessions:
		m.openSessionsOverlay()
	case actionDiff:
		m.openDiffOverlay()
	case actionGit:
		m.openGitOverlay()
	case actionLog:
		m.openActivityOverlay()
	case actionModels:
		m.openModelsOverlay()
	case actionWorktree:
		if err := m.createWorktreeForCurrentTask(); err != nil {
			m.appendSystemEntry("worktree error: "+err.Error(), true)
			return nil
		}
		m.syncAgentsWithTasks()
		m.appendSystemEntry("worktree created", false)
		return refreshGitCmd(m.currentGitPath())
	case actionCompact:
		summary := m.compactThread()
		m.appendSystemEntry(summary, false)
		m.persistMessage("system", summary)
	case actionExport:
		path, err := m.exportCurrentSession()
		if err != nil {
			m.appendSystemEntry("export failed: "+err.Error(), true)
			return nil
		}
		m.appendSystemEntry("session exported to "+path, false)
	case actionSteer:
		m.input.SetValue("/steer ")
		m.input.CursorEnd()
		m.syncInputSize()
		m.updateSlashMenu()
		return m.input.Focus()
	case actionFast:
		if m.backendClient == nil {
			m.appendSystemEntry("no backend available for fast mode", true)
			return nil
		}
		targetModel := fastModeModel
		if m.backendID == backend.ClaudeID {
			targetModel = "sonnet"
		}
		m.backendClient.SetRuntimeModel(targetModel)
		m.backendClient.SetRuntimeReasoningEffort("low")
		m.currentModel = targetModel
		m.currentMode = "low"
		m.appendActivity("system", "fast mode enabled", true)
		m.appendSystemEntry("fast mode enabled ("+targetModel+" + low reasoning)", false)
	case actionHelp:
		m.openHelpOverlay()
	case actionExit:
		return tea.Quit
	case actionOpenEditor:
		path, err := m.exportCurrentSession()
		if err != nil {
			m.appendSystemEntry("open editor failed: "+err.Error(), true)
			return nil
		}
		editor := strings.TrimSpace(os.Getenv("EDITOR"))
		if editor == "" {
			m.appendSystemEntry("$EDITOR is not set; exported to "+path, true)
			return nil
		}
		command := exec.Command(editor, path)
		if err := command.Start(); err != nil {
			m.appendSystemEntry("open editor failed: "+err.Error(), true)
			return nil
		}
		m.appendSystemEntry("opened in editor: "+path, false)
	}
	return nil
}

func (m *model) openHelpOverlay() {
	m.overlay = overlayHelp
	m.overlayIndex = 0
	m.overlayFilter = ""
	m.overlayScroll = 0
	m.overlayItems = nil
	m.overlayLines = nil
}

func (m *model) openAgentsOverlay() {
	items := []overlayItem{
		{
			Title:       "＋ new agent session",
			Description: "create a parallel task with current backend",
			Meta:        "ctrl+x a",
			Action:      actionNewAgent,
		},
	}

	activeID := strings.TrimSpace(m.activeAgent)
	if m.agentPool != nil {
		agents := m.agentPool.List()
		for _, snapshot := range agents {
			indicator := "○"
			if snapshot.Active {
				indicator = "●"
			}
			statusBadge := renderAgentStatusBadge(snapshot.Status)
			if snapshot.Active && m.streaming {
				elapsed := formatElapsedDuration(time.Since(m.streamStartedAt))
				switch snapshot.Status {
				case agent.StatusThinking:
					statusBadge = theme.ToolStatusRun.Render("◐ thinking" + strings.Repeat(".", (m.thinkingFrame%3)+1) + " " + elapsed)
				case agent.StatusTool:
					statusBadge = theme.ToolStatusRun.Render("◉ tool… " + elapsed)
				}
			}
			meta := strings.TrimSpace(statusBadge + " · " + snapshot.BackendID)
			items = append(items, overlayItem{
				Title:       indicator + " " + snapshot.Name,
				Description: snapshot.SessionName,
				Meta:        meta,
				Value:       snapshot.ID,
				Action:      actionAgents,
			})
		}
	}

	selected := 0
	for idx := range items {
		if strings.TrimSpace(items[idx].Value) == activeID {
			selected = idx
			break
		}
	}

	m.overlay = overlayAgents
	m.overlayItems = items
	m.overlayLines = nil
	m.overlayScroll = 0
	m.overlayFilter = ""
	m.overlayIndex = selected
}

func (m *model) openSessionsOverlay() {
	items := make([]overlayItem, 0, len(m.tasks))
	for _, task := range m.tasks {
		status := ""
		if task.ID == m.taskID {
			status = "active"
		}
		items = append(items, overlayItem{
			Title:       task.Name,
			Description: relativeTimeFromNow(task.UpdatedAt),
			Meta:        status,
			Value:       task.ID,
			Action:      actionSessions,
		})
	}

	m.overlay = overlaySessions
	m.resumeOnly = false
	m.overlayItems = items
	m.overlayLines = nil
	m.overlayScroll = 0
	m.overlayFilter = ""
	m.overlayIndex = 0
	for idx := range m.tasks {
		if m.tasks[idx].ID == m.taskID {
			m.overlayIndex = idx
			break
		}
	}
}

func (m *model) openResumeOverlay() {
	items := make([]overlayItem, 0, len(m.tasks))
	for _, task := range m.tasks {
		if task.ID == m.taskID {
			continue
		}
		items = append(items, overlayItem{
			Title:       task.Name,
			Description: relativeTimeFromNow(task.UpdatedAt),
			Meta:        "resume",
			Value:       task.ID,
			Action:      actionResume,
		})
	}

	m.overlay = overlaySessions
	m.resumeOnly = true
	m.overlayItems = items
	m.overlayLines = nil
	m.overlayScroll = 0
	m.overlayFilter = ""
	m.overlayIndex = 0
}

func (m *model) openModelsOverlay() {
	available := []string{}
	if m.backendClient != nil {
		available = m.backendClient.AvailableModels()
	}
	if len(available) == 0 {
		available = []string{"gpt-5.4"}
	}
	modeOptions := reasoningModeOptions()
	items := make([]overlayItem, 0, len(available)+len(modeOptions)+len(backend.IDs()))
	selectedIndex := 0

	for idx, backendID := range backend.IDs() {
		meta := ""
		if backendID == m.backendID {
			meta = "current"
			selectedIndex = idx
		}
		items = append(items, overlayItem{
			Title:       "backend " + string(backendID),
			Description: "runtime backend",
			Meta:        meta,
			Value:       "backend:" + string(backendID),
			Action:      actionModels,
		})
	}

	modelStart := len(items)
	modelDescription := "backend model"
	if m.backendClient != nil {
		modelDescription = m.backendClient.Label() + " model"
	}
	for idx, modelName := range available {
		meta := ""
		if modelName == m.currentModel {
			meta = "current"
			selectedIndex = modelStart + idx
		}
		items = append(items, overlayItem{
			Title:       "model " + modelName,
			Description: modelDescription,
			Meta:        meta,
			Value:       "model:" + modelName,
			Action:      actionModels,
		})
	}
	for _, option := range modeOptions {
		meta := ""
		if option.Effort == m.currentMode {
			meta = "current"
		}
		items = append(items, overlayItem{
			Title:       "mode " + option.Label,
			Description: option.Description,
			Meta:        meta,
			Value:       "effort:" + option.Effort,
			Action:      actionModels,
		})
	}

	m.overlay = overlayModels
	m.overlayItems = items
	m.overlayLines = nil
	m.overlayScroll = 0
	m.overlayFilter = ""
	m.overlayIndex = selectedIndex
}

func (m *model) openCommandPaletteOverlay() {
	m.overlay = overlayCommandPalette
	m.overlayFilter = ""
	m.overlayIndex = 0
	m.overlayScroll = 0
	m.rebuildCommandPaletteOverlay()
}

func (m *model) rebuildCommandPaletteOverlay() {
	filter := strings.ToLower(strings.TrimSpace(m.overlayFilter))
	items := make([]overlayItem, 0, len(m.commands))
	for _, command := range m.commands {
		target := strings.ToLower(command.Command + " " + command.Description + " " + command.Keybind)
		if filter != "" && !strings.Contains(target, filter) {
			continue
		}
		items = append(items, overlayItem{
			Title:       command.Command,
			Description: command.Description,
			Meta:        command.Keybind,
			Action:      command.Action,
		})
	}
	m.overlayItems = items
	m.overlayIndex = clampInt(m.overlayIndex, 0, maxInt(0, len(items)-1))
}

func (m *model) openDiffOverlay() {
	m.overlay = overlayDiff
	m.overlayItems = nil
	m.overlayFilter = ""
	m.overlayIndex = 0
	m.overlayScroll = 0
	lines := colorizeDiffLines(m.diffLines)
	m.overlayLines = lines
}

func (m *model) openGitOverlay() {
	m.overlay = overlayGit
	m.overlayItems = nil
	m.overlayFilter = ""
	m.overlayIndex = 0
	m.overlayScroll = 0
	lines := []string{
		"branch: " + m.branch,
		"status: " + m.gitStatus,
		"repo: " + m.currentGitPath(),
	}
	m.overlayLines = lines
}

func (m *model) openActivityOverlay() {
	m.overlay = overlayActivity
	m.overlayItems = nil
	m.overlayFilter = ""
	m.overlayIndex = 0
	m.overlayScroll = maxInt(0, len(m.activity)-1)
	lines := make([]string, 0, len(m.activity))
	for _, entry := range m.activity {
		prefix := entry.Timestamp.Format("15:04:05") + " "
		line := prefix + entry.Text
		if !entry.Success {
			line = theme.SystemErr.Render(line)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, theme.SystemMuted.Render("No activity yet"))
	}
	m.overlayLines = lines
}

func (m *model) closeOverlay() {
	m.overlay = overlayNone
	m.overlayItems = nil
	m.overlayFilter = ""
	m.overlayLines = nil
	m.overlayScroll = 0
	m.overlayIndex = 0
	m.resumeOnly = false
	m.updateSlashMenu()
}

func (m *model) reloadTasks() error {
	defer m.syncAgentsWithTasks()

	if m.taskStore == nil {
		m.tasks = nil
		m.taskID = ""
		m.taskIndex = 0
		return nil
	}

	tasks, err := m.taskStore.ListActiveTasks(context.Background())
	if err != nil {
		return err
	}
	m.tasks = tasks
	if len(tasks) == 0 {
		m.taskID = ""
		m.taskIndex = 0
		return nil
	}

	if m.taskID == "" {
		m.taskID = tasks[0].ID
		m.taskIndex = 0
		return nil
	}

	for idx := range tasks {
		if tasks[idx].ID == m.taskID {
			m.taskIndex = idx
			return nil
		}
	}

	m.taskID = tasks[0].ID
	m.taskIndex = 0
	return nil
}

func (m *model) selectTaskByIndex(index int) {
	if len(m.tasks) == 0 {
		m.taskIndex = 0
		m.taskID = ""
		m.entries = nil
		m.refreshViewport(true)
		return
	}

	idx := clampInt(index, 0, len(m.tasks)-1)
	m.taskIndex = idx
	m.taskID = m.tasks[idx].ID
	m.syncAgentsWithTasks()
	if err := m.loadThreadForCurrentTask(); err != nil {
		m.appendSystemEntry("thread load error: "+err.Error(), true)
	}
	m.refreshViewport(true)
}

func (m *model) createTaskWithName(name string) error {
	if m.taskStore == nil {
		return errors.New("task store unavailable")
	}

	created, err := m.taskStore.CreateTask(context.Background(), name)
	if err != nil {
		return err
	}
	m.taskID = created.ID
	return nil
}

func (m *model) loadThreadForCurrentTask() error {
	if m.taskStore == nil || strings.TrimSpace(m.taskID) == "" {
		m.entries = []threadEntry{}
		return nil
	}

	messages, err := m.taskStore.ListMessagesByTask(context.Background(), m.taskID)
	if err != nil {
		return err
	}

	entries := make([]threadEntry, 0, len(messages))
	for _, message := range messages {
		entry := threadEntry{
			Text:      message.Content,
			Timestamp: message.CreatedAt.Local(),
		}

		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			entry.Kind = threadUser
		case "assistant":
			entry.Kind = threadAssistant
		case "tool":
			entry.Kind = threadTool
			entry.ToolName = "tool"
			entry.ToolStatus = "done"
			entry.ToolResult = message.Content
		case "reasoning":
			entry.Kind = threadReasoning
		case "error":
			entry.Kind = threadSystem
			entry.IsError = true
		default:
			entry.Kind = threadSystem
		}

		entries = append(entries, entry)
	}

	m.entries = entries
	m.refreshViewport(true)
	return nil
}

func (m *model) persistMessage(role string, content string) {
	if m.taskStore == nil {
		return
	}
	if strings.TrimSpace(m.taskID) == "" {
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	if _, err := m.taskStore.CreateMessage(context.Background(), m.taskID, role, content); err != nil {
		m.appendActivity("system", "persist message failed: "+err.Error(), false)
		return
	}
	if err := m.reloadTasks(); err != nil {
		m.appendActivity("system", "task refresh failed: "+err.Error(), false)
	}
}

func (m *model) createWorktreeForCurrentTask() error {
	if m.taskStore == nil {
		return errors.New("task store unavailable")
	}
	if len(m.tasks) == 0 || m.taskIndex >= len(m.tasks) {
		return errors.New("no active task")
	}

	selected := m.tasks[m.taskIndex]
	if strings.TrimSpace(selected.Worktree) != "" {
		m.appendActivity("system", "worktree already exists", true)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), worktreeTimeout)
	defer cancel()

	worktreePath, err := gitops.CreateWorktree(ctx, m.repoBase, selected.ID)
	if err != nil {
		return err
	}

	if err := m.taskStore.SetTaskWorktreePath(context.Background(), selected.ID, worktreePath); err != nil {
		return err
	}

	if err := m.reloadTasks(); err != nil {
		return err
	}
	m.selectTaskByIndex(0)
	m.appendActivity("system", "worktree path: "+worktreePath, true)
	m.persistMessage("system", "worktree created at "+worktreePath)
	return nil
}

func (m model) currentTaskName() string {
	if len(m.tasks) == 0 || m.taskIndex >= len(m.tasks) {
		return "no session"
	}
	return m.tasks[m.taskIndex].Name
}

func (m model) currentGitPath() string {
	if len(m.tasks) > 0 && m.taskIndex < len(m.tasks) {
		worktree := strings.TrimSpace(m.tasks[m.taskIndex].Worktree)
		if worktree != "" {
			return worktree
		}
	}
	return m.repoBase
}

func (m *model) syncAgentsWithTasks() {
	if m.agentPool == nil {
		return
	}
	if m.taskAgent == nil {
		m.taskAgent = make(map[string]string)
	}

	existingTask := make(map[string]bool, len(m.tasks))
	for _, task := range m.tasks {
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			continue
		}
		existingTask[taskID] = true

		agentID := strings.TrimSpace(m.taskAgent[taskID])
		if agentID == "" {
			created, err := m.agentPool.Create(agent.CreateInput{
				Name:        task.Name,
				BackendID:   string(m.backendID),
				SessionName: task.Name,
				TaskID:      task.ID,
				Worktree:    task.Worktree,
			})
			if err != nil {
				m.appendActivity("system", "create agent failed: "+err.Error(), false)
				continue
			}
			agentID = created.ID
			m.taskAgent[taskID] = agentID
		}

		m.agentPool.SetSession(agentID, task.Name)
		m.agentPool.SetTask(agentID, task.ID)
		m.agentPool.SetWorktree(agentID, task.Worktree)
	}

	for taskID := range m.taskAgent {
		if !existingTask[taskID] {
			delete(m.taskAgent, taskID)
		}
	}

	currentTaskID := strings.TrimSpace(m.taskID)
	if currentTaskID != "" {
		if currentAgentID, ok := m.taskAgent[currentTaskID]; ok {
			if m.agentPool.SetActive(currentAgentID) {
				m.activeAgent = currentAgentID
			}
		}
	}

	if strings.TrimSpace(m.activeAgent) == "" {
		agents := m.agentPool.List()
		if len(agents) > 0 {
			m.activeAgent = agents[0].ID
			m.agentPool.SetActive(m.activeAgent)
		}
	}
	m.refreshAgentsOverlayIfOpen()
}

func (m *model) setActiveAgentStatus(status agent.Status) {
	if m.agentPool == nil {
		return
	}
	if strings.TrimSpace(m.activeAgent) == "" {
		m.syncAgentsWithTasks()
	}
	if strings.TrimSpace(m.activeAgent) == "" {
		return
	}
	m.agentPool.SetStatus(m.activeAgent, status)
	m.refreshAgentsOverlayIfOpen()
}

func (m *model) refreshAgentsOverlayIfOpen() {
	if m.overlay != overlayAgents {
		return
	}
	current := m.overlayIndex
	m.openAgentsOverlay()
	if len(m.overlayItems) == 0 {
		m.overlayIndex = 0
		return
	}
	m.overlayIndex = clampInt(current, 0, len(m.overlayItems)-1)
}

func (m model) activeAgentSnapshot() (agent.Snapshot, bool) {
	if m.agentPool == nil {
		return agent.Snapshot{}, false
	}
	activeID := strings.TrimSpace(m.activeAgent)
	if activeID == "" {
		return m.agentPool.Active()
	}
	return m.agentPool.Get(activeID)
}

func (m *model) appendEntry(entry threadEntry) {
	m.entries = append(m.entries, entry)
	if len(m.entries) > 1000 {
		m.entries = m.entries[len(m.entries)-1000:]
	}
	m.refreshViewport(true)
}

func (m *model) appendSystemEntry(text string, isErr bool) {
	entry := threadEntry{
		Kind:      threadSystem,
		Text:      text,
		Timestamp: time.Now(),
		IsError:   isErr,
	}
	m.appendEntry(entry)
}

func (m *model) appendActivity(kind string, text string, success bool) {
	entry := activityEntry{
		Timestamp: time.Now(),
		Kind:      kind,
		Text:      text,
		Success:   success,
	}
	m.activity = append(m.activity, entry)
	if len(m.activity) > 500 {
		m.activity = m.activity[len(m.activity)-500:]
	}
}

func (m *model) startThinkingStatus() {
	m.thinkingStartedAt = time.Now()
	entry := threadEntry{
		Kind:      threadReasoning,
		Text:      m.renderThinkingStatusText(),
		Timestamp: time.Now(),
	}
	m.entries = append(m.entries, entry)
	m.thinking = true
	m.thinkingEntryIndex = len(m.entries) - 1
	m.thinkingFrame = 0
	m.refreshViewport(true)
}

func (m *model) setThinkingText(text string) {
	if m.thinkingEntryIndex < 0 || m.thinkingEntryIndex >= len(m.entries) {
		return
	}
	if m.entries[m.thinkingEntryIndex].Kind != threadReasoning {
		return
	}
	m.entries[m.thinkingEntryIndex].Text = strings.TrimSpace(text)
	m.refreshViewport(true)
}

func (m *model) advanceThinkingStatus() {
	m.thinkingFrame++
	m.setThinkingText(m.renderThinkingStatusText())
	m.refreshAgentsOverlayIfOpen()
}

func (m model) renderThinkingStatusText() string {
	base := "thinking"
	if m.backendClient != nil {
		name := strings.ToLower(strings.TrimSpace(m.backendClient.Label()))
		if name != "" {
			base = "thinking via " + name
		}
	}

	phase := m.thinkingFrame % 4
	dots := strings.Repeat(".", phase+1)
	elapsed := time.Since(m.thinkingStartedAt)
	if m.thinkingStartedAt.IsZero() {
		elapsed = 0
	}
	return fmt.Sprintf("%s%s (%s elapsed)", base, dots, formatElapsedDuration(elapsed))
}

func (m *model) handleStreamEvent(event backend.Event) bool {
	switch typed := event.(type) {
	case backend.TokenEvent:
		token := typed.Token
		if token == "" {
			return true
		}
		m.setActiveAgentStatus(agent.StatusThinking)
		if m.thinking {
			m.thinking = false
			m.setThinkingText("thinking complete")
			m.thinkingEntryIndex = -1
		}
		m.streamBuffer += token
		if m.streamingAssistantIndex < 0 {
			m.entries = append(m.entries, threadEntry{
				Kind:      threadAssistant,
				Text:      token,
				Timestamp: time.Now(),
			})
			m.streamingAssistantIndex = len(m.entries) - 1
		} else if m.streamingAssistantIndex < len(m.entries) {
			m.entries[m.streamingAssistantIndex].Text += token
		}
		m.refreshViewport(true)
		return true
	case backend.ToolCallEvent:
		name := strings.TrimSpace(typed.Name)
		if name == "" {
			name = "tool"
		}
		args := strings.TrimSpace(typed.Arguments)
		isResultEvent := strings.HasSuffix(strings.ToLower(name), "_result")
		baseName := name
		if isResultEvent {
			baseName = strings.TrimSuffix(name, "_result")
			if strings.TrimSpace(baseName) == "" {
				baseName = "tool"
			}
		}

		if strings.TrimSpace(typed.ID) != "" {
			if idx, ok := m.runningToolByID[typed.ID]; ok && idx < len(m.entries) {
				if isResultEvent {
					m.entries[idx].ToolStatus = "done"
					m.entries[idx].ToolResult = args
					if strings.TrimSpace(m.entries[idx].ToolName) == "" || m.entries[idx].ToolName == "tool" {
						m.entries[idx].ToolName = baseName
					}
					delete(m.runningToolByID, typed.ID)
					m.appendActivity("tool", baseName+" done", true)
					m.setActiveAgentStatus(agent.StatusThinking)
				} else {
					m.entries[idx].ToolName = baseName
					m.entries[idx].ToolArgs = args
					m.entries[idx].ToolStatus = "running"
					m.appendActivity("tool", baseName+" running", true)
					m.setActiveAgentStatus(agent.StatusTool)
				}
			} else {
				status := "running"
				toolArgs := args
				toolResult := ""
				if isResultEvent {
					status = "done"
					toolArgs = ""
					toolResult = args
				}
				m.entries = append(m.entries, threadEntry{
					Kind:       threadTool,
					ToolID:     typed.ID,
					ToolName:   baseName,
					ToolArgs:   toolArgs,
					ToolStatus: status,
					ToolResult: toolResult,
					Timestamp:  time.Now(),
				})
				if status == "running" {
					m.runningToolByID[typed.ID] = len(m.entries) - 1
					m.appendActivity("tool", baseName+" running", true)
					m.setActiveAgentStatus(agent.StatusTool)
				} else {
					m.appendActivity("tool", baseName+" done", true)
					m.setActiveAgentStatus(agent.StatusThinking)
				}
			}
			m.refreshViewport(true)
			return true
		}

		status := "done"
		if name == "shell" {
			status = "running"
		}
		if strings.Contains(strings.ToLower(name), "error") {
			status = "error"
		}
		if status == "running" {
			m.setActiveAgentStatus(agent.StatusTool)
		}

		m.entries = append(m.entries, threadEntry{
			Kind:       threadTool,
			ToolName:   baseName,
			ToolArgs:   args,
			ToolStatus: status,
			Timestamp:  time.Now(),
		})
		m.refreshViewport(true)
		m.appendActivity("tool", baseName+" "+status, status != "error")
		return true
	case backend.DoneEvent:
		m.setActiveAgentStatus(agent.StatusIdle)
		m.thinking = false
		if m.streamingAssistantIndex < 0 && m.thinkingEntryIndex >= 0 {
			m.setThinkingText("no assistant output returned")
		}
		m.thinkingEntryIndex = -1
		m.thinkingFrame = 0
		m.thinkingStartedAt = time.Time{}
		for _, idx := range m.runningToolByID {
			if idx >= 0 && idx < len(m.entries) {
				if m.entries[idx].ToolStatus == "running" {
					m.entries[idx].ToolStatus = "done"
				}
			}
		}
		for idx := range m.entries {
			if m.entries[idx].Kind == threadTool && m.entries[idx].ToolStatus == "running" {
				m.entries[idx].ToolStatus = "done"
			}
		}
		m.runningToolByID = make(map[string]int)

		assistant := strings.TrimSpace(m.streamBuffer)
		if assistant != "" {
			m.persistMessage("assistant", assistant)
		}
		m.streamBuffer = ""
		m.appendActivity("system", "stream complete", true)
		m.refreshViewport(true)
		return false
	case backend.ErrorEvent:
		m.setActiveAgentStatus(agent.StatusIdle)
		if m.thinkingEntryIndex >= 0 {
			m.setThinkingText("stream failed before assistant output")
		}
		m.thinking = false
		m.thinkingEntryIndex = -1
		m.thinkingFrame = 0
		m.thinkingStartedAt = time.Time{}
		errText := typed.Err.Error()
		m.appendSystemEntry("stream error: "+errText, true)
		m.appendActivity("error", "stream error: "+errText, false)
		m.persistMessage("error", errText)
		m.streamBuffer = ""
		for idx := range m.entries {
			if m.entries[idx].Kind == threadTool && m.entries[idx].ToolStatus == "running" {
				m.entries[idx].ToolStatus = "error"
			}
		}
		m.runningToolByID = make(map[string]int)
		m.refreshViewport(true)
		return false
	default:
		m.appendActivity("system", "unknown stream event", false)
		return false
	}
}

func (m *model) handleShellResult(result shellResultMsg) tea.Cmd {
	summary := summarizeShellOutput(result.output, result.exitCode)
	if result.err != nil {
		summary = summarizeShellOutput(result.output, result.exitCode)
	}

	updated := false
	for idx := range m.entries {
		if m.entries[idx].Kind == threadTool && m.entries[idx].ToolID == result.toolID {
			m.entries[idx].ToolStatus = "done"
			if result.err != nil {
				m.entries[idx].ToolStatus = "error"
			}
			m.entries[idx].ToolResult = summary
			updated = true
			break
		}
	}
	if !updated {
		m.entries = append(m.entries, threadEntry{
			Kind:       threadTool,
			ToolID:     result.toolID,
			ToolName:   "shell",
			ToolArgs:   result.command,
			ToolStatus: "done",
			ToolResult: summary,
			Timestamp:  time.Now(),
		})
	}

	if result.err != nil {
		m.appendActivity("tool", "shell error: "+result.command, false)
		m.persistMessage("tool", result.command+"\n"+summary)
	} else {
		m.appendActivity("tool", "shell done: "+result.command, true)
		m.persistMessage("tool", result.command+"\n"+summary)
	}
	if !m.streaming {
		m.setActiveAgentStatus(agent.StatusIdle)
	}
	m.refreshViewport(true)
	if !result.assist {
		return nil
	}

	assistPrompt := buildTerminalAssistPrompt(result.command, result.output, result.exitCode)
	if strings.TrimSpace(assistPrompt) == "" {
		return nil
	}
	if m.streaming {
		m.queuedPrompts = append(m.queuedPrompts, assistPrompt)
		m.appendActivity("system", fmt.Sprintf("queued terminal analysis (%d)", len(m.queuedPrompts)), true)
		return nil
	}
	m.appendActivity("system", "terminal analysis started", true)
	return m.startStreamCmd(assistPrompt)
}

func (m *model) updateSlashMenu() {
	if m.overlay != overlayNone {
		m.showSlashMenu = false
		m.slashItems = nil
		m.slashIndex = 0
		return
	}

	value := m.input.Value()
	if !strings.HasPrefix(value, "/") || strings.Contains(value, "\n") {
		m.showSlashMenu = false
		m.slashItems = nil
		m.slashIndex = 0
		return
	}

	query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "/")))
	items := make([]slashCommand, 0, len(m.commands))
	for _, command := range m.commands {
		target := strings.ToLower(strings.TrimPrefix(command.Command, "/"))
		if query == "" || strings.HasPrefix(target, query) || strings.Contains(strings.ToLower(command.Description), query) {
			items = append(items, command)
		}
	}

	if len(items) == 0 {
		m.showSlashMenu = false
		m.slashItems = nil
		m.slashIndex = 0
		return
	}

	m.showSlashMenu = true
	m.slashItems = items
	m.slashIndex = clampInt(m.slashIndex, 0, len(items)-1)
}

func (m *model) syncInputSize() {
	if m.width <= 0 {
		return
	}
	availableWidth := maxInt(minLayoutWidth, m.width)
	boxWidth := maxInt(20, availableWidth-4)
	frameWidth := theme.InputBoxFocused.GetHorizontalFrameSize()
	inputWidth := maxInt(12, boxWidth-frameWidth)
	m.input.SetWidth(inputWidth)

	contentWidth := maxInt(1, m.input.Width())
	lines := wrappedInputLineCount(m.input.Value(), contentWidth)
	maxHeight := m.input.MaxHeight
	if maxHeight <= 0 {
		maxHeight = 12
	}
	target := lines
	if strings.TrimSpace(m.input.Value()) != "" {
		// Keep one line of headroom so wrapped prompts don't push the first
		// visible line out of view while typing at the end of the buffer.
		target++
	}
	target = clampInt(target, 1, maxHeight)
	m.input.SetHeight(target)
}

func (m *model) resizeViewport() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	header := m.renderHeader()
	input := m.renderInputBar()
	layout := computeZoneLayout(m.width, m.height, lipgloss.Height(header), lipgloss.Height(input))
	mainWidth, _ := splitContentColumns(maxInt(minLayoutWidth, m.width))
	m.viewport.Width = maxInt(20, mainWidth-4)
	m.viewport.Height = maxInt(1, layout.ContentHeight-1)
}

func (m *model) refreshViewport(follow bool) {
	if m.viewport.Width <= 0 {
		return
	}
	m.planItems = derivePlanItems(m.entries)
	content := m.renderThreadContent(m.viewport.Width)
	m.viewport.SetContent(content)
	if follow && m.sticky {
		m.viewport.GotoBottom()
	}
}

func (m model) renderThreadContent(width int) string {
	if len(m.entries) == 0 {
		return theme.SystemMuted.Render("No messages yet. Type below to chat with Orb.")
	}

	rendered := make([]string, 0, len(m.entries))
	for _, entry := range m.entries {
		rendered = append(rendered, m.renderThreadEntry(entry, width))
	}
	return strings.Join(rendered, "\n\n")
}

func (m model) renderThreadEntry(entry threadEntry, width int) string {
	safeWidth := maxInt(24, width-2)
	timestamp := entry.Timestamp.Local().Format("15:04")

	switch entry.Kind {
	case threadUser:
		inner := maxInt(20, safeWidth-2)
		head := alignLeftRight(theme.UserLabel.Render("you"), theme.UserTimestamp.Render(timestamp), inner-2)
		body := theme.UserBody.Copy().Width(inner - 2).Render(strings.TrimSpace(entry.Text))
		box := theme.UserMessageBox.Copy().Width(inner).Render(head + "\n" + body)
		return indentLines(box, "  ")
	case threadAssistant:
		prefix := theme.AssistantMark.Render("◉") + "  " + theme.AssistantName.Render("orb")
		right := theme.AssistantTimestamp.Render(timestamp)
		ruleWidth := maxInt(4, safeWidth-ansi.StringWidth(prefix)-ansi.StringWidth(right)-4)
		header := prefix + "  " + theme.AssistantRule.Render(strings.Repeat("─", ruleWidth)) + "  " + right
		body := theme.AssistantBody.Copy().Width(maxInt(20, safeWidth-2)).Render(strings.TrimSpace(entry.Text))
		return indentLines(header+"\n\n"+body, "  ")
	case threadTool:
		inner := maxInt(20, safeWidth-2)
		status := theme.ToolStatusDone.Render("✓ done")
		if entry.ToolStatus == "running" {
			status = theme.ToolStatusRun.Render("○ running...")
		}
		if entry.ToolStatus == "error" {
			status = theme.ToolStatusErr.Render("✗ error")
		}

		toolName := strings.TrimSpace(entry.ToolName)
		if toolName == "" {
			toolName = "tool"
		}
		args := strings.TrimSpace(entry.ToolArgs)
		if args == "" {
			args = "{}"
		}

		line := alignLeftRight(theme.ToolCallName.Render(toolName), status, inner-2)
		argLine := theme.ToolArgs.Copy().Width(inner - 2).Render(args)
		parts := []string{
			theme.ToolCallLabel.Render("tool call"),
			line,
			argLine,
		}
		if strings.TrimSpace(entry.ToolResult) != "" {
			parts = append(parts, theme.AssistantRule.Render(strings.Repeat("─", maxInt(6, inner-2))))
			parts = append(parts, theme.ToolResult.Copy().Width(inner-2).Render(entry.ToolResult))
		}
		box := theme.ToolCallBox.Copy().Width(inner).Render(strings.Join(parts, "\n"))
		return indentLines(box, "  ")
	case threadReasoning:
		rule := theme.ReasoningRule.Render(strings.Repeat("╌", maxInt(8, safeWidth-2)))
		head := theme.ReasoningHead.Render("╌╌ thinking " + strings.Repeat("╌", maxInt(4, safeWidth-14)))
		body := theme.ReasoningBody.Copy().Width(maxInt(20, safeWidth-2)).Render(strings.TrimSpace(entry.Text))
		return indentLines(head+"\n\n"+body+"\n\n"+rule, "  ")
	default:
		line := entry.Timestamp.Local().Format("15:04:05") + " " + strings.TrimSpace(entry.Text)
		if entry.IsError {
			return indentLines(theme.SystemErr.Copy().Width(maxInt(20, safeWidth)).Render(line), "  ")
		}
		return indentLines(theme.SystemMuted.Copy().Width(maxInt(20, safeWidth)).Render(line), "  ")
	}
}

func (m model) renderHeader() string {
	availableWidth := maxInt(minLayoutWidth, m.width)

	logo := theme.HeaderLogoMark.Render(theme.HeaderMarkGlyph) + " " + theme.HeaderLogoText.Render("ORB")
	sessionName := truncateWithEllipsis(strings.TrimSpace(m.currentTaskName()), 24)
	left := logo + " " + theme.HeaderDivider.Render("│") + " " + theme.HeaderSession.Render(sessionName)

	rightParts := []string{
		theme.HeaderModel.Render("backend:" + string(m.backendID)),
		theme.HeaderModel.Render(m.currentModel),
		theme.HeaderModel.Render("mode:" + displayModeLabel(m.currentModel, m.currentMode)),
		theme.HeaderModel.Render("input:" + composeModeLabel(m.composeMode)),
		theme.HeaderBranch.Render("⎇ " + strings.TrimSpace(m.branch)),
	}

	authDot := theme.HeaderAuthErr.Render("●")
	if m.authConnected {
		authDot = theme.HeaderAuthOK.Render("●")
	}
	rightParts = append(rightParts, authDot)
	right := strings.Join(rightParts, "  ")

	line := alignLeftRight(left, right, maxInt(1, availableWidth-2))
	return theme.HeaderBar.Copy().Width(availableWidth).Render(line)
}

func (m model) renderInputBar() string {
	availableWidth := maxInt(minLayoutWidth, m.width)
	boxWidth := maxInt(20, availableWidth-4)

	inputBoxStyle := theme.InputBoxFocused
	if m.overlay != overlayNone {
		inputBoxStyle = theme.InputBoxBlurred
	}

	field := inputBoxStyle.Copy().Width(boxWidth).Render(m.input.View())
	parts := make([]string, 0, 3)
	if m.showSlashMenu {
		parts = append(parts, indentLines(m.renderSlashMenu(boxWidth), "  "))
	}
	parts = append(parts, indentLines(field, "  "))

	tokenText := theme.FooterMuted.Render(
		fmt.Sprintf("input:%s · %s tokens", composeModeLabel(m.composeMode), formatTokenEstimate(estimateTokens(m.entries))),
	)
	if len(m.queuedPrompts) > 0 {
		tokenText = theme.FooterMuted.Render(
			fmt.Sprintf(
				"input:%s · %s tokens · %d queued",
				composeModeLabel(m.composeMode),
				formatTokenEstimate(estimateTokens(m.entries)),
				len(m.queuedPrompts),
			),
		)
	}

	right := theme.FooterMuted.Render("tab cycle mode · ctrl+x keybinds ") + theme.FooterHelp.Render("[cmd+?]")
	if m.streaming {
		right = theme.FooterMuted.Render("enter queues · /steer interrupts · tab cycle mode · ") + theme.FooterHelp.Render("[cmd+?]")
	}
	footer := alignLeftRight(tokenText, right, maxInt(20, availableWidth-4))
	parts = append(parts, "  "+footer)

	body := strings.Join(parts, "\n")
	return theme.InputBar.Copy().Width(availableWidth).Render(body)
}

func (m model) renderSlashMenu(width int) string {
	if len(m.slashItems) == 0 {
		return ""
	}

	maxItems := minInt(6, len(m.slashItems))
	visible := m.slashItems[:maxItems]
	inner := maxInt(16, width-4)

	rows := make([]string, 0, len(visible))
	for idx, item := range visible {
		left := item.Command + "  " + item.Description
		row := alignLeftRight(left, item.Keybind, inner-2)
		if idx == m.slashIndex {
			rows = append(rows, theme.DropdownSelected.Copy().Width(inner-2).Render(row))
			continue
		}
		rows = append(rows, theme.DropdownRow.Copy().Width(inner-2).Render(row))
	}

	return theme.DropdownBox.Copy().Width(inner).Render(strings.Join(rows, "\n"))
}

func (m model) renderContent(contentHeight int) string {
	if m.overlay != overlayNone {
		overlayBox := m.renderOverlayBox(contentHeight)
		placed := lipgloss.Place(
			maxInt(minLayoutWidth, m.width),
			contentHeight,
			lipgloss.Center,
			lipgloss.Center,
			overlayBox,
		)
		return fillZone(maxInt(minLayoutWidth, m.width), contentHeight, theme.BG0, placed)
	}

	totalWidth := maxInt(minLayoutWidth, m.width)
	mainWidth, sidebarWidth := splitContentColumns(totalWidth)

	if len(m.entries) == 0 {
		centered := m.renderEmptyCenter(mainWidth, contentHeight)
		mainPane := fillZone(mainWidth, contentHeight, theme.BG0, centered)
		sidebar := m.renderSidebar(sidebarWidth, contentHeight)
		joined := lipgloss.JoinHorizontal(lipgloss.Top, mainPane, sidebar)
		return fillZone(totalWidth, contentHeight, theme.BG0, joined)
	}

	view := strings.TrimRight(m.viewport.View(), "\n")
	if strings.TrimSpace(view) == "" {
		view = theme.SystemMuted.Render("No messages yet. Type below to start.")
	}

	content := indentLines(view, "  ")
	if !m.sticky {
		indicator := alignLeftRight("", theme.FooterHelp.Render("↓ scroll to bottom"), maxInt(20, mainWidth-4))
		content += "\n" + "  " + indicator
	}

	mainPane := fillZone(mainWidth, contentHeight, theme.BG0, content)
	sidebar := m.renderSidebar(sidebarWidth, contentHeight)
	joined := lipgloss.JoinHorizontal(lipgloss.Top, mainPane, sidebar)
	return fillZone(totalWidth, contentHeight, theme.BG0, joined)
}

func (m model) renderSidebar(width int, height int) string {
	safeWidth := maxInt(24, width)
	safeHeight := maxInt(1, height)
	zone := theme.SidebarZone.Copy()
	padLeft := zone.GetPaddingLeft()
	padRight := zone.GetPaddingRight()
	padTop := zone.GetPaddingTop()
	padBottom := zone.GetPaddingBottom()
	hBorder := maxInt(0, zone.GetHorizontalFrameSize()-padLeft-padRight)
	vBorder := maxInt(0, zone.GetVerticalFrameSize()-padTop-padBottom)

	zoneWidth := maxInt(2, safeWidth-hBorder)
	zoneHeight := maxInt(2, safeHeight-vBorder)
	innerWidth := maxInt(12, zoneWidth-padLeft-padRight)
	innerHeight := maxInt(1, zoneHeight-padTop-padBottom)

	worktree := strings.TrimSpace(m.currentGitPath())
	if worktree == "" {
		worktree = "n/a"
	}

	lines := make([]string, 0, innerHeight)
	lines = append(lines, sidebarSectionHeader("WORKTREE", innerWidth))
	lines = append(lines, sidebarKeyValueRow("branch", strings.TrimSpace(m.branch), innerWidth))
	lines = append(lines, sidebarKeyValueRow("status", strings.TrimSpace(m.gitStatus), innerWidth))
	lines = append(lines, sidebarKeyValueRow("path", worktree, innerWidth))
	lines = append(lines, "")

	lines = append(lines, sidebarSectionHeader("PLAN", innerWidth))
	if len(m.planItems) == 0 {
		lines = append(lines, theme.SidebarMeta.Render(truncateAndPadANSI("○ No active plan yet", innerWidth)))
		lines = append(lines, theme.SidebarMeta.Render(truncateAndPadANSI("○ Ask Orb to make a plan", innerWidth)))
	} else {
		for idx, item := range m.planItems {
			if idx >= 10 {
				break
			}
			lines = append(lines, sidebarPlanRow(item, innerWidth))
		}
	}
	lines = append(lines, "")

	lines = append(lines, sidebarSectionHeader("SESSION", innerWidth))
	lines = append(lines, sidebarKeyValueRow("events", fmt.Sprintf("%d", len(m.activity)), innerWidth))
	agentName := "n/a"
	agentMode := "idle"
	if snapshot, ok := m.activeAgentSnapshot(); ok {
		agentName = strings.TrimSpace(snapshot.Name)
		if agentName == "" {
			agentName = snapshot.ID
		}
		agentMode = agentStatusLabel(snapshot.Status)
		if m.streaming && snapshot.Active {
			elapsed := formatElapsedDuration(time.Since(m.streamStartedAt))
			switch snapshot.Status {
			case agent.StatusThinking:
				agentMode = "thinking" + strings.Repeat(".", (m.thinkingFrame%3)+1) + " " + elapsed
			case agent.StatusTool:
				agentMode = "tool… " + elapsed
			}
		}
	}
	lines = append(lines, sidebarKeyValueRow("agent", agentName, innerWidth))
	lines = append(lines, sidebarKeyValueRow("status", agentMode, innerWidth))
	lines = append(lines, sidebarKeyValueRow("backend", string(m.backendID), innerWidth))
	lines = append(lines, sidebarKeyValueRow("model", m.currentModel, innerWidth))
	lines = append(lines, sidebarKeyValueRow("mode", displayModeLabel(m.currentModel, m.currentMode), innerWidth))
	lines = append(lines, sidebarKeyValueRow("input", composeModeLabel(m.composeMode), innerWidth))
	if len(m.queuedPrompts) > 0 {
		lines = append(lines, sidebarKeyValueRow("queued", fmt.Sprintf("%d", len(m.queuedPrompts)), innerWidth))
	}

	clamped := clampPlainLines(lines, innerWidth, innerHeight)
	pane := zone.Width(zoneWidth).Height(zoneHeight).Render(strings.Join(clamped, "\n"))
	return lipgloss.Place(
		safeWidth,
		safeHeight,
		lipgloss.Left,
		lipgloss.Top,
		pane,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceBackground(theme.BG1),
	)
}

func (m model) renderEmptyCenter(width int, height int) string {
	lines := []string{
		theme.SplashMark.Render(theme.HeaderMarkGlyph) + " " + theme.SplashWord.Render("ORB"),
		"",
		theme.SystemMuted.Render("new session ready"),
		theme.FooterMuted.Render("type a message below to start chatting"),
		theme.FooterMuted.Render("or use /resume to continue an older chat"),
		theme.FooterMuted.Render("tab cycles input modes: agent → plan → terminal"),
		"",
		theme.FooterHelp.Render("/resume") + theme.FooterMuted.Render("  /models  /help"),
	}
	content := strings.Join(lines, "\n")
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (m model) renderOverlayBox(contentHeight int) string {
	overlayWidth := clampInt((m.width*80)/100, 48, maxInt(48, m.width-6))
	overlayHeight := clampInt((contentHeight*75)/100, 10, maxInt(10, contentHeight-2))
	innerWidth := maxInt(20, overlayWidth-6)
	innerHeight := maxInt(3, overlayHeight-4)

	switch m.overlay {
	case overlayHelp:
		return m.renderHelpOverlay(overlayWidth, overlayHeight, innerWidth, innerHeight)
	case overlayAgents:
		return m.renderSelectionOverlay("agents", overlayWidth, overlayHeight, innerWidth, innerHeight)
	case overlaySessions:
		title := "sessions"
		if m.resumeOnly {
			title = "resume"
		}
		return m.renderSelectionOverlay(title, overlayWidth, overlayHeight, innerWidth, innerHeight)
	case overlayModels:
		return m.renderSelectionOverlay("models", overlayWidth, overlayHeight, innerWidth, innerHeight)
	case overlayCommandPalette:
		return m.renderSelectionOverlay("command palette", overlayWidth, overlayHeight, innerWidth, innerHeight)
	case overlayDiff:
		return m.renderTextOverlay("diff", overlayWidth, overlayHeight, innerWidth, innerHeight)
	case overlayGit:
		return m.renderTextOverlay("git", overlayWidth, overlayHeight, innerWidth, innerHeight)
	case overlayActivity:
		return m.renderTextOverlay("activity log", overlayWidth, overlayHeight, innerWidth, innerHeight)
	default:
		return ""
	}
}

func (m model) renderHelpOverlay(overlayWidth int, overlayHeight int, innerWidth int, innerHeight int) string {
	lines := make([]string, 0, 64)
	lines = append(lines, theme.OverlaySection.Render("Navigation"))
	for _, group := range m.keys.FullHelp() {
		for _, binding := range group {
			helpEntry := binding.Help()
			keyText := helpEntry.Key
			descText := helpEntry.Desc
			if strings.TrimSpace(keyText) == "" {
				continue
			}
			left := theme.OverlayKeyPill.Render(keyText)
			row := alignLeftRight(left, theme.OverlayRow.Render(descText), innerWidth)
			lines = append(lines, row)
		}
	}
	lines = append(lines, "")
	lines = append(lines, theme.OverlaySection.Render("Commands"))
	for _, command := range m.commands {
		left := theme.OverlayKeyPill.Render(command.Command)
		row := alignLeftRight(left, theme.OverlayRow.Render(command.Description+" · "+command.Keybind), innerWidth)
		lines = append(lines, row)
	}

	content := clampPlainLines(lines, innerWidth, innerHeight)
	title := theme.OverlayTitle.Render("─ help ─")
	body := title + "\n\n" + strings.Join(content, "\n")
	return theme.OverlayBox.Copy().Width(overlayWidth).Height(overlayHeight).Render(body)
}

func (m model) renderSelectionOverlay(title string, overlayWidth int, overlayHeight int, innerWidth int, innerHeight int) string {
	rows := make([]string, 0, innerHeight)
	headerLines := 0
	if m.overlay == overlayCommandPalette {
		filter := strings.TrimSpace(m.overlayFilter)
		if filter == "" {
			filter = "type to filter"
		}
		rows = append(rows, theme.OverlayMeta.Render("filter: "+filter))
		headerLines = 1
	}

	visibleCount := maxInt(1, innerHeight-headerLines)
	start := 0
	if m.overlayIndex >= visibleCount {
		start = m.overlayIndex - visibleCount + 1
	}
	if start < 0 {
		start = 0
	}

	if len(m.overlayItems) == 0 {
		rows = append(rows, theme.SystemMuted.Render("No items"))
	} else {
		end := minInt(len(m.overlayItems), start+visibleCount)
		for idx := start; idx < end; idx++ {
			item := m.overlayItems[idx]
			prefix := "○"
			if m.overlay == overlayAgents {
				prefix = ""
			}
			if m.overlay == overlaySessions && item.Value == m.taskID {
				prefix = "●"
			}
			left := item.Title
			if prefix != "" {
				left = prefix + " " + item.Title
			}
			if item.Description != "" {
				left += "  " + item.Description
			}
			meta := item.Meta
			row := alignLeftRight(left, meta, innerWidth)
			if idx == m.overlayIndex {
				rows = append(rows, theme.OverlayRowSelected.Copy().Width(innerWidth).Render(row))
				continue
			}
			rows = append(rows, theme.OverlayRow.Copy().Width(innerWidth).Render(row))
		}
	}

	content := clampPlainLines(rows, innerWidth, innerHeight)
	titleLine := theme.OverlayTitle.Render("─ " + title + " ─")
	body := titleLine + "\n\n" + strings.Join(content, "\n")
	return theme.OverlayBox.Copy().Width(overlayWidth).Height(overlayHeight).Render(body)
}

func (m model) renderTextOverlay(title string, overlayWidth int, overlayHeight int, innerWidth int, innerHeight int) string {
	lines := m.overlayLines
	if len(lines) == 0 {
		lines = []string{theme.SystemMuted.Render("No data")}
	}

	maxScroll := maxInt(0, len(lines)-innerHeight)
	scroll := clampInt(m.overlayScroll, 0, maxScroll)
	end := minInt(len(lines), scroll+innerHeight)
	visible := lines[scroll:end]
	content := clampPlainLines(visible, innerWidth, innerHeight)

	titleLine := theme.OverlayTitle.Render("─ " + title + " ─")
	body := titleLine + "\n\n" + strings.Join(content, "\n")
	return theme.OverlayBox.Copy().Width(overlayWidth).Height(overlayHeight).Render(body)
}

func (m *model) compactThread() string {
	if len(m.entries) <= 20 {
		return "session already compact"
	}
	kept := m.entries[len(m.entries)-20:]
	removed := len(m.entries) - len(kept)
	m.entries = append([]threadEntry(nil), kept...)
	m.refreshViewport(true)
	return fmt.Sprintf("session compacted: kept %d recent entries, removed %d", len(kept), removed)
}

func (m *model) exportCurrentSession() (string, error) {
	taskName := strings.TrimSpace(m.currentTaskName())
	if taskName == "" {
		taskName = "session"
	}
	safeName := strings.ToLower(strings.ReplaceAll(taskName, " ", "-"))
	safeName = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, safeName)
	safeName = strings.Trim(safeName, "-")
	if safeName == "" {
		safeName = "session"
	}

	cwd := m.currentGitPath()
	if strings.TrimSpace(cwd) == "" {
		cwd = m.repoBase
	}
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
	}

	fileName := fmt.Sprintf("orb-%s-%s.md", safeName, time.Now().Format("20060102-150405"))
	path := filepath.Join(cwd, fileName)

	lines := []string{
		"# Orb Session Export",
		"",
		"- Session: " + m.currentTaskName(),
		"- Branch: " + m.branch,
		"- Model: " + m.currentModel,
		"- Exported: " + time.Now().Format(time.RFC3339),
		"",
	}

	for _, entry := range m.entries {
		header := "## " + entry.Timestamp.Local().Format("15:04:05")
		switch entry.Kind {
		case threadUser:
			header += " user"
			lines = append(lines, header, "", entry.Text, "")
		case threadAssistant:
			header += " assistant"
			lines = append(lines, header, "", entry.Text, "")
		case threadTool:
			header += " tool"
			lines = append(lines, header, "", "- name: "+entry.ToolName, "- status: "+entry.ToolStatus, "- args: `"+entry.ToolArgs+"`")
			if strings.TrimSpace(entry.ToolResult) != "" {
				lines = append(lines, "- result: "+entry.ToolResult)
			}
			lines = append(lines, "")
		case threadReasoning:
			header += " reasoning"
			lines = append(lines, header, "", entry.Text, "")
		default:
			header += " system"
			lines = append(lines, header, "", entry.Text, "")
		}
	}

	content := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write export: %w", err)
	}
	return path, nil
}

func (m *model) startStreamCmd(prompt string) tea.Cmd {
	if m.backendClient == nil {
		m.appendSystemEntry("no backend selected", true)
		return nil
	}
	return startBackendStreamCmd(m.backendClient, prompt, m.currentGitPath())
}

func startBackendStreamCmd(activeBackend backend.Backend, prompt string, cwd string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		activeBackend.SetRuntimeWorkingDir(cwd)
		toolParams := []byte(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
		tools := []backend.ToolDefinition{
			{
				Type: "function",
				Function: backend.ToolFunctionDefinition{
					Name:        "echo_tool",
					Description: "Echoes text for Orb tool-call stream rendering.",
					Parameters:  toolParams,
				},
			},
		}
		messages := []backend.Message{
			{
				Role:    "system",
				Content: "You are Orb, a concise software engineering agent.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		}
		stream := activeBackend.Stream(ctx, messages, tools)
		return streamStartedMsg{stream: stream, cancel: cancel}
	}
}

func waitForStreamEventCmd(stream <-chan backend.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-stream
		if !ok {
			return streamEndedMsg{}
		}
		return streamEventMsg{event: event}
	}
}

func refreshGitCmd(path string) tea.Cmd {
	cleanPath := strings.TrimSpace(path)
	return func() tea.Msg {
		if cleanPath == "" {
			return gitRefreshedMsg{err: errors.New("no repository path configured")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), gitRefreshTimeout)
		defer cancel()

		snapshot, err := gitops.Refresh(ctx, cleanPath)
		return gitRefreshedMsg{snapshot: snapshot, err: err}
	}
}

func gitTickCmd() tea.Cmd {
	return tea.Tick(gitRefreshInterval, func(time.Time) tea.Msg {
		return gitTickMsg{}
	})
}

func thinkingTickCmd() tea.Cmd {
	return tea.Tick(220*time.Millisecond, func(time.Time) tea.Msg {
		return thinkingTickMsg{}
	})
}

func streamWatchTickCmd() tea.Cmd {
	return tea.Tick(streamWatchInterval, func(time.Time) tea.Msg {
		return streamWatchTickMsg{}
	})
}

func runShellCommandCmd(cwd string, toolID string, command string, assist bool) tea.Cmd {
	cleanCommand := strings.TrimSpace(command)
	cleanCWD := strings.TrimSpace(cwd)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		execCmd := exec.CommandContext(ctx, "zsh", "-c", cleanCommand)
		if cleanCWD != "" {
			execCmd.Dir = cleanCWD
		}
		execCmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1", "CLICOLOR=0", "CLICOLOR_FORCE=0")

		output, err := execCmd.CombinedOutput()
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}

		return shellResultMsg{
			toolID:   toolID,
			command:  cleanCommand,
			output:   string(output),
			exitCode: exitCode,
			err:      err,
			assist:   assist,
		}
	}
}

func splitContentColumns(totalWidth int) (int, int) {
	safeTotal := maxInt(minLayoutWidth, totalWidth)
	sidebarWidth := int(float64(safeTotal) * 0.30)
	sidebarWidth = clampInt(sidebarWidth, 24, 42)
	mainWidth := safeTotal - sidebarWidth
	if mainWidth < 36 {
		mainWidth = 36
		sidebarWidth = safeTotal - mainWidth
	}
	if sidebarWidth < 24 {
		sidebarWidth = 24
		mainWidth = safeTotal - sidebarWidth
	}
	return mainWidth, sidebarWidth
}

func derivePlanItems(entries []threadEntry) []planItem {
	const maxPlans = 12
	plans := make([]planItem, 0, maxPlans)

	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if entry.Kind != threadAssistant && entry.Kind != threadReasoning && entry.Kind != threadSystem {
			continue
		}

		lines := strings.Split(entry.Text, "\n")
		numberedLines := make([]string, 0, 4)
		for _, raw := range lines {
			line := strings.TrimSpace(raw)
			if line == "" {
				continue
			}
			lower := strings.ToLower(line)

			switch {
			case strings.HasPrefix(lower, "- [ ] "):
				plans = append(plans, planItem{Text: strings.TrimSpace(line[6:]), Done: false})
			case strings.HasPrefix(lower, "- [x] "), strings.HasPrefix(lower, "- [X] "):
				plans = append(plans, planItem{Text: strings.TrimSpace(line[6:]), Done: true})
			case hasNumberedPrefix(line):
				numberedLines = append(numberedLines, trimNumberedPrefix(line))
			}

			if len(plans) >= maxPlans {
				return dedupePlanItems(plans, maxPlans)
			}
		}

		if len(numberedLines) >= 2 {
			for _, item := range numberedLines {
				plans = append(plans, planItem{Text: strings.TrimSpace(item), Done: false})
				if len(plans) >= maxPlans {
					return dedupePlanItems(plans, maxPlans)
				}
			}
		}
	}

	return dedupePlanItems(plans, maxPlans)
}

func hasNumberedPrefix(line string) bool {
	clean := strings.TrimSpace(line)
	if clean == "" {
		return false
	}
	seenDigit := false
	for idx, r := range clean {
		if r >= '0' && r <= '9' {
			seenDigit = true
			continue
		}
		if r == '.' && seenDigit && idx+1 < len(clean) {
			return true
		}
		return false
	}
	return false
}

func trimNumberedPrefix(line string) string {
	clean := strings.TrimSpace(line)
	for idx, r := range clean {
		if r == '.' {
			return strings.TrimSpace(clean[idx+1:])
		}
	}
	return clean
}

func dedupePlanItems(items []planItem, limit int) []planItem {
	if len(items) == 0 {
		return []planItem{}
	}
	seen := make(map[string]bool, len(items))
	deduped := make([]planItem, 0, minInt(limit, len(items)))
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.Text))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, item)
		if len(deduped) >= limit {
			break
		}
	}
	return deduped
}

func colorizeDiffLines(lines []string) []string {
	if len(lines) == 0 {
		return []string{theme.SystemMuted.Render("working tree clean")}
	}

	maxLines := 240
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	styled := make([]string, 0, len(lines))
	for _, line := range lines {
		styled = append(styled, colorizeDiffLine(line))
	}
	return styled
}

func colorizeDiffLine(line string) string {
	if strings.HasPrefix(line, "@@") {
		return theme.DiffHunk.Render(line)
	}
	if strings.HasPrefix(line, "+") {
		return theme.DiffAdded.Render(line)
	}
	if strings.HasPrefix(line, "-") {
		return theme.DiffRemoved.Render(line)
	}
	return theme.DiffContext.Render(line)
}

func summarizeShellOutput(output string, exitCode int) string {
	clean := sanitizeTerminalText(output)
	trimmed := strings.TrimSpace(clean)
	if trimmed == "" {
		return fmt.Sprintf("[exit %d]", exitCode)
	}

	lines := strings.Split(trimmed, "\n")
	maxLines := 8
	if len(lines) > maxLines {
		preview := strings.Join(lines[:maxLines], " | ")
		return fmt.Sprintf("[exit %d] %s ... (+%d lines)", exitCode, preview, len(lines)-maxLines)
	}

	preview := strings.Join(lines, " | ")
	if len(preview) > 320 {
		preview = preview[:320] + "..."
	}
	return fmt.Sprintf("[exit %d] %s", exitCode, preview)
}

func sanitizeTerminalText(input string) string {
	clean := strings.ReplaceAll(input, "\r", "\n")
	clean = ansiOSCSequence.ReplaceAllString(clean, "")
	clean = ansiCSISequence.ReplaceAllString(clean, "")
	clean = ansiESCSequence.ReplaceAllString(clean, "")
	clean = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, clean)
	return clean
}

func alignLeftRight(left string, right string, width int) string {
	if width <= 0 {
		return ""
	}

	leftWidth := ansi.StringWidth(left)
	rightWidth := ansi.StringWidth(right)
	if leftWidth+rightWidth >= width {
		return truncateAndPadANSI(left+" "+right, width)
	}

	padding := width - leftWidth - rightWidth
	return left + strings.Repeat(" ", padding) + right
}

func sidebarSectionHeader(title string, width int) string {
	safeWidth := maxInt(1, width)
	cleanTitle := strings.TrimSpace(title)
	if cleanTitle == "" {
		return strings.Repeat(" ", safeWidth)
	}

	label := theme.SidebarTitle.Render(cleanTitle)
	ruleWidth := maxInt(0, safeWidth-ansi.StringWidth(cleanTitle)-1)
	if ruleWidth <= 0 {
		return truncateAndPadANSI(label, safeWidth)
	}
	return truncateAndPadANSI(label+" "+theme.SidebarDivider.Render(strings.Repeat("─", ruleWidth)), safeWidth)
}

func sidebarKeyValueRow(label string, value string, width int) string {
	safeWidth := maxInt(1, width)
	prefix := strings.TrimSpace(label)
	if prefix != "" {
		prefix += ": "
	}
	prefixWidth := ansi.StringWidth(prefix)
	if prefixWidth >= safeWidth {
		return theme.SidebarLabel.Render(truncateAndPadANSI(prefix, safeWidth))
	}

	valueWidth := maxInt(1, safeWidth-prefixWidth)
	renderedValue := truncateAndPadANSI(strings.TrimSpace(value), valueWidth)
	return theme.SidebarLabel.Render(prefix) + theme.SidebarValue.Render(renderedValue)
}

func sidebarPlanRow(item planItem, width int) string {
	safeWidth := maxInt(1, width)
	textWidth := maxInt(1, safeWidth-2)
	marker := theme.SidebarPlanTodo.Render("○")
	if item.Done {
		marker = theme.SidebarPlanDone.Render("●")
	}
	text := truncateAndPadANSI(strings.TrimSpace(item.Text), textWidth)
	return marker + " " + theme.SidebarValue.Render(text)
}

func agentStatusLabel(status agent.Status) string {
	switch status {
	case agent.StatusThinking:
		return "thinking"
	case agent.StatusTool:
		return "tool"
	default:
		return "idle"
	}
}

func renderAgentStatusBadge(status agent.Status) string {
	switch status {
	case agent.StatusThinking:
		return theme.ToolStatusRun.Render("◐ thinking")
	case agent.StatusTool:
		return theme.ToolStatusRun.Render("◉ tool")
	default:
		return theme.ToolStatusDone.Render("○ idle")
	}
}

func clampPlainLines(lines []string, width int, height int) []string {
	if width <= 0 || height <= 0 {
		return []string{}
	}

	trimmed := make([]string, 0, height)
	for _, line := range lines {
		trimmed = append(trimmed, truncateAndPadANSI(line, width))
		if len(trimmed) >= height {
			break
		}
	}
	for len(trimmed) < height {
		trimmed = append(trimmed, strings.Repeat(" ", width))
	}
	return trimmed
}

func indentLines(content string, indent string) string {
	if content == "" {
		return indent
	}
	lines := strings.Split(content, "\n")
	for idx := range lines {
		lines[idx] = indent + lines[idx]
	}
	return strings.Join(lines, "\n")
}

func truncateWithEllipsis(value string, maxLen int) string {
	clean := strings.TrimSpace(value)
	if maxLen <= 0 {
		return ""
	}
	if ansi.StringWidth(clean) <= maxLen {
		return clean
	}
	if maxLen == 1 {
		return "…"
	}
	return ansi.Truncate(clean, maxLen-1, "") + "…"
}

func formatElapsedDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	seconds := int(duration.Round(time.Second).Seconds())
	minutes := seconds / 60
	remainder := seconds % 60
	return fmt.Sprintf("%02d:%02d", minutes, remainder)
}

func relativeTimeFromNow(t time.Time) string {
	now := time.Now()
	delta := now.Sub(t)
	if delta < 0 {
		delta = -delta
	}

	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
	}
}

func estimateTokens(entries []threadEntry) int {
	totalChars := 0
	for _, entry := range entries {
		totalChars += len(entry.Text)
		totalChars += len(entry.ToolArgs)
		totalChars += len(entry.ToolResult)
	}
	if totalChars == 0 {
		return 0
	}
	return maxInt(1, totalChars/4)
}

func formatTokenEstimate(tokens int) string {
	if tokens < 1000 {
		return fmt.Sprintf("~%d", tokens)
	}
	whole := float64(tokens) / 1000.0
	return fmt.Sprintf("~%.1fk", whole)
}

func wrappedInputLineCount(value string, width int) int {
	safeWidth := maxInt(1, width)
	clean := strings.ReplaceAll(value, "\r", "")
	if clean == "" {
		return 1
	}

	logicalLines := strings.Split(clean, "\n")
	total := 0
	for _, line := range logicalLines {
		expanded := strings.ReplaceAll(line, "\t", "    ")
		visible := ansi.StringWidth(expanded)
		if visible <= 0 {
			total++
			continue
		}
		total += (visible-1)/safeWidth + 1
	}
	if total <= 0 {
		return 1
	}
	return total
}

func defaultSlashCommands() []slashCommand {
	return []slashCommand{
		{Command: "/new", Description: "Start a new task/session", Keybind: "ctrl+x n", Action: actionNewSession},
		{Command: "/agents", Description: "Open agent switcher", Keybind: "ctrl+x a", Action: actionAgents},
		{Command: "/resume", Description: "Resume an older chat session", Keybind: "ctrl+x s", Action: actionResume},
		{Command: "/sessions", Description: "Browse and switch sessions", Keybind: "ctrl+x s", Action: actionSessions},
		{Command: "/diff", Description: "Open diff viewer overlay", Keybind: "ctrl+x d", Action: actionDiff},
		{Command: "/git", Description: "Open git status overlay", Keybind: "ctrl+x g", Action: actionGit},
		{Command: "/log", Description: "Open activity log overlay", Keybind: "ctrl+x l", Action: actionLog},
		{Command: "/models", Description: "Switch backend, model, and mode", Keybind: "ctrl+x m", Action: actionModels},
		{Command: "/mode", Description: "Switch low/medium/high/xhigh", Keybind: "ctrl+x m", Action: actionModels},
		{Command: "/worktree", Description: "Create git worktree for task", Keybind: "ctrl+x w", Action: actionWorktree},
		{Command: "/compact", Description: "Summarize and compact session", Keybind: "ctrl+x c", Action: actionCompact},
		{Command: "/export", Description: "Export session to markdown", Keybind: "ctrl+x x", Action: actionExport},
		{Command: "/steer", Description: "Interrupt current stream and steer", Keybind: "/steer <msg>", Action: actionSteer},
		{Command: "/fast", Description: "Enable fast profile", Keybind: "quick mode", Action: actionFast},
		{Command: "/help", Description: "Show help overlay", Keybind: "cmd+?", Action: actionHelp},
		{Command: "/exit", Description: "Quit Orb", Keybind: "ctrl+x q", Action: actionExit},
	}
}

func reasoningModeOptions() []reasoningModeOption {
	return []reasoningModeOption{
		{Label: "low", Effort: "low", Description: "low reasoning effort"},
		{Label: "medium", Effort: "medium", Description: "balanced default"},
		{Label: "high", Effort: "high", Description: "deeper reasoning"},
		{Label: "xhigh", Effort: "xhigh", Description: "extra high reasoning"},
	}
}

func reasoningModeLabel(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	default:
		return "medium"
	}
}

func displayModeLabel(modelName string, effort string) string {
	if strings.EqualFold(strings.TrimSpace(modelName), fastModeModel) && strings.EqualFold(strings.TrimSpace(effort), "low") {
		return "fast"
	}
	return reasoningModeLabel(effort)
}

func defaultModelForBackend(id backend.ID) string {
	switch id {
	case backend.ClaudeID:
		return "sonnet"
	default:
		return "gpt-5.4"
	}
}

func composeModeLabel(mode composeMode) string {
	switch mode {
	case composeModePlan:
		return "plan"
	case composeModeTerminal:
		return "terminal"
	default:
		return "agent"
	}
}

func composeModePlaceholder(mode composeMode) string {
	switch mode {
	case composeModePlan:
		return "plan mode: describe the goal and constraints..."
	case composeModeTerminal:
		return "terminal mode: type a shell command (Orb will run and explain)..."
	default:
		return "type a message, / for commands, @ for files, ! for shell..."
	}
}

func buildPlanModePrompt(userPrompt string) string {
	request := strings.TrimSpace(userPrompt)
	if request == "" {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{
		"Plan mode is active.",
		"Create a concrete implementation plan only.",
		"Do not call tools or run commands.",
		"Return 5-10 concise numbered steps plus key risks and validations.",
		"",
		"User request:",
		request,
	}, "\n"))
}

func buildTerminalAssistPrompt(command string, output string, exitCode int) string {
	cleanCommand := strings.TrimSpace(command)
	if cleanCommand == "" {
		return ""
	}

	cleanOutput := strings.TrimSpace(sanitizeTerminalText(output))
	if len(cleanOutput) > 2400 {
		cleanOutput = cleanOutput[:2400] + "\n... (truncated)"
	}
	if cleanOutput == "" {
		cleanOutput = "(no output)"
	}

	return strings.TrimSpace(strings.Join([]string{
		"Terminal mode follow-up.",
		"Analyze the command result and reply with:",
		"1) what happened,",
		"2) likely issues (if any),",
		"3) the best next command.",
		"Be concise.",
		"",
		"Command:",
		cleanCommand,
		fmt.Sprintf("Exit code: %d", exitCode),
		"Output:",
		cleanOutput,
	}, "\n"))
}

func renderSplash(width int, height int, frame int) string {
	w := maxInt(minLayoutWidth, width)
	h := maxInt(10, height)

	pulse := frame % 8
	outerDot := theme.SplashOuter
	midRing := theme.SplashMiddle
	centerMark := theme.SplashMark
	switch pulse {
	case 1, 5:
		midRing = theme.SplashCenter
	case 2, 6:
		outerDot = theme.SplashMiddle
	case 3, 7:
		centerMark = theme.SplashCenter
	}

	lineTop := "  " + outerDot.Render("·") + "  " + midRing.Render("◌") + "  " + outerDot.Render("·")
	lineMid := midRing.Render("◌") + "       " + midRing.Render("◌")
	lineCenter := outerDot.Render("·") + "   " + centerMark.Render("◉") + " " + theme.SplashWord.Render("ORB") + "   " + outerDot.Render("·")

	splash := strings.Join([]string{
		lineTop,
		lineMid,
		lineCenter,
		lineMid,
		lineTop,
	}, "\n")

	placed := lipgloss.Place(
		w,
		h,
		lipgloss.Center,
		lipgloss.Center,
		splash,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceBackground(theme.BG0),
	)
	return fillZone(w, h, theme.BG0, placed)
}
