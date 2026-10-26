package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// theme is the single source of truth for styles. The accent follows the
// light's current color, so the UI is literally the lamp.
type theme struct {
	accent lipgloss.Color
	dark   bool
}

// newTheme builds the theme. Whether the terminal is dark is decided once at
// startup (detectDark); styles never query the terminal.
func newTheme(accentHex string, dark bool) theme {
	return theme{accent: lipgloss.Color(accentHex), dark: dark}
}

func (t theme) muted() lipgloss.Color {
	if t.dark {
		return lipgloss.Color("#8A8A8A")
	}
	return lipgloss.Color("#6A6A6A")
}

func (t theme) frame() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.accent).Padding(0, 2)
}
func (t theme) title() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(t.accent) }
func (t theme) dim() lipgloss.Style   { return lipgloss.NewStyle().Foreground(t.muted()) }
func (t theme) errText() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5252"))
}
func (t theme) okText() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#00C853"))
}
func (t theme) selected() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(t.accent)
}
func (t theme) tabActive() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(t.accent)
}
func (t theme) tabIdle() lipgloss.Style { return lipgloss.NewStyle().Foreground(t.muted()) }

// dot is a colored status bullet.
func (t theme) dot(hex string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("●")
}

// block is a w-by-h rectangle of the given color.
func (t theme) block(hex string, w, h int) string {
	return lipgloss.NewStyle().Background(lipgloss.Color(hex)).Width(w).Height(h).Render("")
}

// off is the color of a switched-off segment.
func (t theme) offHex() string {
	if t.dark {
		return "#2A2A2A"
	}
	return "#D0D0D0"
}
