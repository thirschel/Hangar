package keys

import (
	"github.com/charmbracelet/bubbles/key"
)

type KeyName int

const (
	KeyUp KeyName = iota
	KeyDown
	KeyEnter
	KeyNew
	KeyKill
	KeyQuit
	KeyReview
	KeyPush
	KeySubmit

	KeyTab        // Tab is a special keybinding for switching between panes.
	KeySubmitName // SubmitName is a special keybinding for submitting the name of a new instance.

	KeyCheckout
	KeyResume
	KeyPrompt // New key for entering a prompt
	KeyHelp   // Key for showing help screen
	KeyBrowse // Key for browsing Copilot sessions

	// Diff keybindings
	KeyShiftUp
	KeyShiftDown

	// Reorder keybindings
	KeyMoveUp
	KeyMoveDown

	// Sidebar mode cycling
	KeyModeCycle
	KeyModeCycleBack

	// Search / filter
	KeySearch
	KeyStatusFilter
	KeySearchApply  // menu hint: enter applies the search
	KeySearchCancel // menu hint: esc clears the search

	// Multi-agent grid view
	KeyMark
	KeyGrid
	// Grid in-view hints (display-only; the keys are handled inline by the grid view)
	KeyGridFocus
	KeyGridType
	KeyGridColumns
	KeyGridRelease
	KeyGridExit
)

// GlobalKeyStringsMap is a global, immutable map string to keybinding.
var GlobalKeyStringsMap = map[string]KeyName{
	"up":         KeyUp,
	"k":          KeyUp,
	"down":       KeyDown,
	"j":          KeyDown,
	"shift+up":   KeyShiftUp,
	"shift+down": KeyShiftDown,
	"J":          KeyMoveDown,
	"K":          KeyMoveUp,
	"s":          KeyModeCycle,
	"S":          KeyModeCycleBack,
	"/":          KeySearch,
	"f":          KeyStatusFilter,
	"N":          KeyPrompt,
	"enter":      KeyEnter,
	"o":          KeyEnter,
	"n":          KeyNew,
	"D":          KeyKill,
	"q":          KeyQuit,
	"tab":        KeyTab,
	"c":          KeyCheckout,
	"r":          KeyResume,
	"p":          KeySubmit,
	"?":          KeyHelp,
	"b":          KeyBrowse,
	"m":          KeyMark,
	"g":          KeyGrid,
}

// GlobalkeyBindings is a global, immutable map of KeyName tot keybinding.
var GlobalkeyBindings = map[KeyName]key.Binding{
	KeyUp: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	KeyDown: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	KeyShiftUp: key.NewBinding(
		key.WithKeys("shift+up"),
		key.WithHelp("shift+↑", "scroll"),
	),
	KeyShiftDown: key.NewBinding(
		key.WithKeys("shift+down"),
		key.WithHelp("shift+↓", "scroll"),
	),
	KeyEnter: key.NewBinding(
		key.WithKeys("enter", "o"),
		key.WithHelp("↵/o", "open"),
	),
	KeyNew: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new"),
	),
	KeyKill: key.NewBinding(
		key.WithKeys("D"),
		key.WithHelp("D", "kill"),
	),
	KeyHelp: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	KeyBrowse: key.NewBinding(
		key.WithKeys("b"),
		key.WithHelp("b", "browse sessions"),
	),
	KeyQuit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
	KeySubmit: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "push branch"),
	),
	KeyPrompt: key.NewBinding(
		key.WithKeys("N"),
		key.WithHelp("N", "new with prompt"),
	),
	KeyCheckout: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "checkout"),
	),
	KeyTab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch tab"),
	),
	KeyResume: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "resume"),
	),

	KeyMoveUp: key.NewBinding(
		key.WithKeys("K"),
		key.WithHelp("K", "move up"),
	),
	KeyMoveDown: key.NewBinding(
		key.WithKeys("J"),
		key.WithHelp("J", "move down"),
	),

	KeyModeCycle: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "sort"),
	),
	KeyModeCycleBack: key.NewBinding(
		key.WithKeys("S"),
		key.WithHelp("S", "sort back"),
	),
	KeySearch: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search"),
	),
	KeyStatusFilter: key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "filter status"),
	),

	// -- Special keybindings --

	KeySearchApply: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "apply"),
	),
	KeySearchCancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "clear"),
	),

	// -- Multi-agent grid view --

	KeyMark: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "mark"),
	),
	KeyGrid: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("g", "grid view"),
	),
	KeyGridFocus: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("↑↓←→/tab", "focus"),
	),
	KeyGridType: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "type"),
	),
	KeyGridColumns: key.NewBinding(
		key.WithKeys("[", "]"),
		key.WithHelp("[ ]", "per row"),
	),
	KeyGridRelease: key.NewBinding(
		key.WithKeys("ctrl+q"),
		key.WithHelp("ctrl+q", "release"),
	),
	KeyGridExit: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "exit"),
	),

	// -- Special keybindings --

	KeySubmitName: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "submit name"),
	),
}
