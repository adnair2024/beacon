package tui

import "github.com/charmbracelet/lipgloss"

// detectDark resolves the configured theme to dark/light. For "auto" it asks
// the terminal for its background color (an OSC 11 query). That must happen
// once, before Bubble Tea takes over the terminal: during rendering it would
// stall the first frame, and a late reply would be read as keystrokes. A
// terminal that never answers costs a one-time delay here (termenv's timeout).
func detectDark(mode string) bool {
	switch mode {
	case "dark":
		return true
	case "light":
		return false
	}
	return lipgloss.HasDarkBackground()
}
