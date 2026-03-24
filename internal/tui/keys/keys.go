package keys

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines Orb's OpenCode-style key bindings.
type KeyMap struct {
	Leader      key.Binding
	NewSession  key.Binding
	Agents      key.Binding
	Sessions    key.Binding
	Diff        key.Binding
	Git         key.Binding
	Log         key.Binding
	ModelSwitch key.Binding
	Thinking    key.Binding
	Worktree    key.Binding
	Compact     key.Binding
	Export      key.Binding
	OpenEditor  key.Binding
	Help        key.Binding
	Quit        key.Binding

	CommandPalette key.Binding
	ComposeMode    key.Binding
	ScrollBottom   key.Binding
	ScrollUpHalf   key.Binding
	ScrollDownHalf key.Binding
	Dismiss        key.Binding
	Send           key.Binding
}

// Default returns the default key map.
func Default() KeyMap {
	return KeyMap{
		Leader: key.NewBinding(
			key.WithKeys("ctrl+x"),
			key.WithHelp("ctrl+x", "leader"),
		),
		NewSession: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("ctrl+x n", "new session"),
		),
		Agents: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("ctrl+x a", "agents"),
		),
		Sessions: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("ctrl+x s", "sessions"),
		),
		Diff: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("ctrl+x d", "diff"),
		),
		Git: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("ctrl+x g", "git status"),
		),
		Log: key.NewBinding(
			key.WithKeys("l"),
			key.WithHelp("ctrl+x l", "activity log"),
		),
		ModelSwitch: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("ctrl+x m", "switch model"),
		),
		Thinking: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("ctrl+x t", "toggle thinking"),
		),
		Worktree: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("ctrl+x w", "worktree"),
		),
		Compact: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("ctrl+x c", "compact"),
		),
		Export: key.NewBinding(
			key.WithKeys("x"),
			key.WithHelp("ctrl+x x", "export"),
		),
		OpenEditor: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("ctrl+x e", "open editor"),
		),
		Help: key.NewBinding(
			key.WithKeys("ctrl+x h", "alt+?"),
			key.WithHelp("cmd+?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+x q", "ctrl+c"),
			key.WithHelp("ctrl+x q", "quit"),
		),
		CommandPalette: key.NewBinding(
			key.WithKeys("ctrl+p"),
			key.WithHelp("ctrl+p", "command palette"),
		),
		ComposeMode: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "cycle compose mode"),
		),
		ScrollBottom: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G", "scroll bottom"),
		),
		ScrollUpHalf: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "scroll up"),
		),
		ScrollDownHalf: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "scroll down"),
		),
		Dismiss: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "dismiss"),
		),
		Send: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "send"),
		),
	}
}

// FullHelp returns grouped keybindings for rendering.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Leader, k.CommandPalette, k.Help, k.Quit},
		{k.NewSession, k.Agents, k.Sessions, k.Diff, k.Git, k.Log},
		{k.ModelSwitch, k.Thinking, k.Worktree, k.Compact, k.Export, k.OpenEditor},
		{k.ComposeMode, k.ScrollBottom, k.ScrollUpHalf, k.ScrollDownHalf, k.Send, k.Dismiss},
	}
}
