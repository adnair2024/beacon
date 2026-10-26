package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// picker is the override dialog (key o): a color/kelvin/on/off field with
// palette suggestions, and a brightness field.
type picker struct {
	open  bool
	color textinput.Model
	bri   textinput.Model
	field int // 0 color, 1 brightness
	err   string
}

func newPicker() picker {
	c := textinput.New()
	c.Prompt = "color      > "
	c.Placeholder = "orange, #FF8C00, 3000k, on, off"
	c.CharLimit = 32
	c.ShowSuggestions = true
	b := textinput.New()
	b.Prompt = "brightness > "
	b.CharLimit = 3
	b.SetValue("100")
	return picker{color: c, bri: b}
}

func (p *picker) show(names []string) tea.Cmd {
	p.open, p.field, p.err = true, 0, ""
	p.color.SetValue("")
	p.color.SetSuggestions(names)
	p.bri.SetValue("100")
	p.bri.Blur()
	return p.color.Focus()
}

func (p *picker) help() []key.Binding {
	return []key.Binding{ek.Confirm, ek.Field, ek.Palette, ek.Cancel}
}

func (p *picker) focus(f int) tea.Cmd {
	p.field = f
	if f == 0 {
		p.bri.Blur()
		return p.color.Focus()
	}
	p.color.Blur()
	return p.bri.Focus()
}

// update handles a key. If the user confirmed, it returns the state to apply.
func (p *picker) update(msg tea.KeyMsg, pal color.Palette) (tea.Cmd, *govee.State) {
	switch {
	case isKey(msg, ek.Cancel):
		p.open = false
		p.color.Blur()
		p.bri.Blur()
		return nil, nil
	case msg.String() == "up", msg.String() == "down":
		return p.focus(1 - p.field), nil
	case isKey(msg, ek.Confirm):
		n, err := strconv.Atoi(strings.TrimSpace(p.bri.Value()))
		if err != nil || n < 1 || n > 100 {
			p.err = "brightness must be 1-100"
			return nil, nil
		}
		st, err := govee.ParseState(p.color.Value(), n, pal)
		if err != nil {
			p.err = err.Error()
			return nil, nil
		}
		p.open = false
		p.color.Blur()
		p.bri.Blur()
		return nil, &st
	}
	var cmd tea.Cmd
	if p.field == 0 {
		p.color, cmd = p.color.Update(msg)
	} else {
		p.bri, cmd = p.bri.Update(msg)
	}
	p.err = ""
	return cmd, nil
}

func (p *picker) view(th theme) string {
	lines := []string{th.title().Render("Override"), p.color.View(), p.bri.View()}
	if p.err != "" {
		lines = append(lines, th.errText().Render(p.err))
	}
	return strings.Join(lines, "\n")
}
