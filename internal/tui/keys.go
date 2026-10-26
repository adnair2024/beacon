package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Global keys.
type globalKeys struct {
	Quit, Next, Prev, Tab, Override, Clear, Help key.Binding
}

var gk = globalKeys{
	Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	Next:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab")),
	Prev:     key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev tab")),
	Tab:      key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6"), key.WithHelp("1-6", "tabs")),
	Override: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "override")),
	Clear:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear")),
	Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
}

// Colors tab keys.
type colorKeys struct {
	Up, Down, Move, Edit, Effect, Add, Delete, Save key.Binding
}

var ck = colorKeys{
	Up:   key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down: key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	// Move is for the help line only; Up and Down do the matching.
	Move:   key.NewBinding(key.WithKeys("up", "down", "k", "j"), key.WithHelp("↑/↓", "move")),
	Edit:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "edit")),
	Effect: key.NewBinding(key.WithKeys("e", "right", "l"), key.WithHelp("e", "effect/cycle")),
	Add:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add color")),
	Delete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Save:   key.NewBinding(key.WithKeys("s", "ctrl+s"), key.WithHelp("s", "save")),
}

// Keys shared by the editing overlays (colors editor, override picker).
type editKeys struct {
	Confirm, Cancel, Field, Palette key.Binding
}

var ek = editKeys{
	Confirm: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply")),
	Cancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	Field:   key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "switch field")),
	Palette: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next palette color")),
}

// Logs tab keys.
var logKeys = []key.Binding{
	key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", "scroll")),
	key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "follow")),
}

func isKey(msg tea.KeyMsg, b key.Binding) bool { return key.Matches(msg, b) }

// keyMap adapts bindings to bubbles/help (short form only; see fullHelp).
type keyMap struct{ short, full []key.Binding }

func (k keyMap) ShortHelp() []key.Binding  { return k.short }
func (k keyMap) FullHelp() [][]key.Binding { return [][]key.Binding{k.full} }
