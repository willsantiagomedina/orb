package theme

import "github.com/charmbracelet/lipgloss"

// Core palette.
var (
	BG0 = lipgloss.Color("#0a0a0e")
	BG1 = lipgloss.Color("#0f0f14")
	BG2 = lipgloss.Color("#14141b")
	BG3 = lipgloss.Color("#1c1c26")

	Grey0 = lipgloss.Color("#2c2c35")
	Grey1 = lipgloss.Color("#3d3d52")
	Grey2 = lipgloss.Color("#7a7a8e")
	Grey3 = lipgloss.Color("#b0b0c4")
	Grey4 = lipgloss.Color("#a8a8c4")
	White = lipgloss.Color("#e8e8f4")

	Purple0 = lipgloss.Color("#2f1c47")
	Purple1 = lipgloss.Color("#6d28d9")
	Purple2 = lipgloss.Color("#9b59f5")
	Purple3 = lipgloss.Color("#c084fc")

	Success = lipgloss.Color("#34d399")
	Danger  = lipgloss.Color("#f87171")
	Warning = lipgloss.Color("#f59e0b")
	Info    = lipgloss.Color("#60a5fa")
)

// Base zones.
var (
	App = lipgloss.NewStyle().Background(BG0).Foreground(Grey3)

	HeaderBar = lipgloss.NewStyle().
			Background(BG2).
			Foreground(White).
			Padding(0, 1).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(Grey0)

	HeaderLogoMark = lipgloss.NewStyle().Foreground(Purple2)
	HeaderLogoText = lipgloss.NewStyle().Foreground(White).Bold(true)
	HeaderVersion  = lipgloss.NewStyle().Foreground(Grey1)
	HeaderDivider  = lipgloss.NewStyle().Foreground(Grey0)
	HeaderSep      = lipgloss.NewStyle().Foreground(Grey0)
	HeaderSession  = lipgloss.NewStyle().Foreground(Grey3)
	HeaderModel    = lipgloss.NewStyle().Foreground(Grey3)
	HeaderMode     = lipgloss.NewStyle().Foreground(Grey2)
	HeaderBranch   = lipgloss.NewStyle().Foreground(Purple3)
	HeaderAuthOK   = lipgloss.NewStyle().Foreground(Success)
	HeaderAuthErr  = lipgloss.NewStyle().Foreground(Danger)

	ViewportZone = lipgloss.NewStyle().
			Background(BG0).
			Foreground(Grey3)

	ContentZone = lipgloss.NewStyle().
			Background(BG0).
			Foreground(Grey3)

	InputBar = lipgloss.NewStyle().
			Background(BG1).
			Foreground(Grey3).
			Padding(0, 0).
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(Grey0)

	InputBoxFocused = lipgloss.NewStyle().
			Background(BG1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Purple2).
			Padding(0, 1)

	InputBoxBlurred = lipgloss.NewStyle().
			Background(BG1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Purple1).
			Padding(0, 1)

	InputPrompt = lipgloss.NewStyle().Foreground(Purple2).Bold(true)
	InputText   = lipgloss.NewStyle().Foreground(Grey4)
	InputHint   = lipgloss.NewStyle().Foreground(Grey1)

	FooterMuted = lipgloss.NewStyle().Foreground(Grey1)
	FooterHelp  = lipgloss.NewStyle().Foreground(Purple2)

	InputModePill = lipgloss.NewStyle().Background(Purple0).Foreground(Purple3).Padding(0, 1).Bold(true)
	InputModeText = lipgloss.NewStyle().Foreground(Grey2)

	SidebarFrame = lipgloss.NewStyle().
			Background(BG1).
			Foreground(Grey3).
			Padding(1, 1).
			Border(lipgloss.NormalBorder(), true, true, true, true).
			BorderForeground(Grey0)
	SidebarZone = lipgloss.NewStyle().
			Background(BG1).
			Foreground(Grey3).
			Padding(1, 1).
			Border(lipgloss.NormalBorder(), true, true, true, true).
			BorderForeground(Grey0)
	SidebarTitle    = lipgloss.NewStyle().Foreground(Grey3).Background(BG1).Bold(true)
	SidebarIcon     = lipgloss.NewStyle().Foreground(Purple2).Background(BG1)
	SidebarDivider  = lipgloss.NewStyle().Foreground(Grey0).Background(BG1)
	SidebarLabel    = lipgloss.NewStyle().Foreground(Grey1).Background(BG1)
	SidebarValue    = lipgloss.NewStyle().Foreground(Grey3).Background(BG1)
	SidebarText     = lipgloss.NewStyle().Foreground(Grey3).Background(BG1)
	SidebarMeta     = lipgloss.NewStyle().Foreground(Grey1).Background(BG1)
	SidebarPlanTodo = lipgloss.NewStyle().Foreground(Warning).Background(BG1)
	SidebarPlanDone = lipgloss.NewStyle().Foreground(Success).Background(BG1)

	ProgressFill  = lipgloss.NewStyle().Foreground(Purple2).Background(BG1)
	ProgressEmpty = lipgloss.NewStyle().Foreground(Grey1).Background(BG1)

	ShortcutCard = lipgloss.NewStyle().
			Background(BG2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Grey1).
			Padding(0, 2)
	ShortcutKey  = lipgloss.NewStyle().Background(Purple0).Foreground(Purple3).Padding(0, 1)
	ShortcutDesc = lipgloss.NewStyle().Foreground(Grey2)
)

// Message styles.
var (
	UserMessageBox = lipgloss.NewStyle().
			Background(BG2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Grey1).
			Padding(0, 1)
	UserLabel     = lipgloss.NewStyle().Foreground(Grey3).Bold(true)
	UserTimestamp = lipgloss.NewStyle().Foreground(Grey1)
	UserBody      = lipgloss.NewStyle().Foreground(Grey4)

	AssistantMark      = lipgloss.NewStyle().Foreground(Purple2)
	AssistantName      = lipgloss.NewStyle().Foreground(Purple3).Bold(true)
	AssistantRule      = lipgloss.NewStyle().Foreground(Grey0)
	AssistantTimestamp = lipgloss.NewStyle().Foreground(Grey1)
	AssistantBody      = lipgloss.NewStyle().Foreground(Grey3)

	ToolCallBox = lipgloss.NewStyle().
			Background(BG2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Purple1).
			Padding(0, 1)
	ToolCallArrow = lipgloss.NewStyle().Foreground(Purple2).Bold(true)
	ToolCallName  = lipgloss.NewStyle().Foreground(White).Bold(true)
	ToolStatusRun  = lipgloss.NewStyle().Foreground(Warning)
	ToolStatusDone = lipgloss.NewStyle().Foreground(Success)
	ToolStatusErr  = lipgloss.NewStyle().Foreground(Danger)
	ToolArgs       = lipgloss.NewStyle().Foreground(Grey2)
	ToolResult     = lipgloss.NewStyle().Foreground(Grey2)

	ReasoningRule = lipgloss.NewStyle().Foreground(Grey0)
	ReasoningHead = lipgloss.NewStyle().Foreground(Grey2).Italic(true)
	ReasoningBody = lipgloss.NewStyle().Foreground(Grey1).Italic(true)

	SystemMuted = lipgloss.NewStyle().Foreground(Grey2)
	SystemErr   = lipgloss.NewStyle().Foreground(Danger)
)

// Overlay styles.
var (
	OverlayBackdrop = lipgloss.NewStyle().Background(BG0)
	OverlayBox      = lipgloss.NewStyle().
			Background(BG2).
			Foreground(Grey3).
			Border(lipgloss.NormalBorder()).
			BorderForeground(Purple2).
			Padding(1, 2)
	OverlayTitle       = lipgloss.NewStyle().Foreground(Purple3).Bold(true)
	OverlaySection     = lipgloss.NewStyle().Foreground(Purple3).Bold(true)
	OverlayRowSelected = lipgloss.NewStyle().Background(BG3).Foreground(White)
	OverlayRow         = lipgloss.NewStyle().Foreground(Grey3)
	OverlayMeta        = lipgloss.NewStyle().Foreground(Grey1)
	OverlayKeyPill     = lipgloss.NewStyle().Background(Purple0).Foreground(Purple3).Padding(0, 1)
)

// Dropdown styles for slash/file menus.
var (
	DropdownBox = lipgloss.NewStyle().
			Background(BG3).
			Border(lipgloss.NormalBorder()).
			BorderForeground(Purple1).
			Padding(0, 1)
	DropdownSelected = lipgloss.NewStyle().Background(Purple0).Foreground(Purple3)
	DropdownRow      = lipgloss.NewStyle().Foreground(Grey3)
)

// Diff styles.
var (
	DiffAdded   = lipgloss.NewStyle().Background(lipgloss.Color("#0d2a1e")).Foreground(Success)
	DiffRemoved = lipgloss.NewStyle().
			Background(lipgloss.Color("#2a0d0d")).
			Foreground(Danger)
	DiffHunk    = lipgloss.NewStyle().Foreground(Info).Bold(true)
	DiffContext = lipgloss.NewStyle().Foreground(Grey2)
)

// Splash styles.
var (
	SplashOuter  = lipgloss.NewStyle().Foreground(Purple0)
	SplashMiddle = lipgloss.NewStyle().Foreground(Purple1)
	SplashCenter = lipgloss.NewStyle().Foreground(Purple3)
	SplashMark   = lipgloss.NewStyle().Foreground(Purple2)
	SplashWord   = lipgloss.NewStyle().Foreground(White).Bold(true)
)
