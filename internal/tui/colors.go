package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/effect"
)

type rowKind int

const (
	kindColor  rowKind = iota // a color (name, #hex, rgb()), optionally with an effect
	kindChoice                // one of a few words
	kindInt                   // a number in a range
)

// row is one editable setting, bound to the config through closures so that
// map entries (the palette) and plain fields are handled the same way.
type row struct {
	section string
	label   string
	kind    rowKind
	get     func() string
	set     func(string)
	effect  *string  // kindColor: the role's effect, if it has one
	options []string // kindChoice
	min     int      // kindInt
	max     int
	hint    string
	palette string // set for palette rows: the color's name
}

// saveFunc persists a config; injected so tests don't touch disk.
type saveFunc func(path string, cfg config.Config) error

type savedMsg struct {
	path string
	err  error
}

type colorMode int

const (
	cmBrowse colorMode = iota
	cmEdit             // editing the selected row's value
	cmName             // naming a new palette color
	cmDelete           // confirming a palette delete
)

var paletteName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// colorsModel is the Colors tab: every setting that changes how the light
// looks (colors, effects, idle, the Claude and pomodoro look, and the named
// palette), with a live preview, saved to the config file.
type colorsModel struct {
	cfg     config.Config // a working copy; the app adopts it once saved
	path    string
	save    saveFunc
	rows    []row
	cursor  int
	mode    colorMode
	editing bool // kept for callers/tests: true in cmEdit and cmName
	input   textinput.Model
	palIdx  int
	dirty   bool
	status  string
	statusE bool
	start   time.Time
}

func newColors(cfg config.Config, path string, save saveFunc, now time.Time) *colorsModel {
	m := &colorsModel{cfg: cfg, path: path, save: save, start: now}
	m.input = textinput.New()
	m.input.CharLimit = 32
	m.bind()
	return m
}

func (m *colorsModel) colorRow(section, label string, field *string, eff *string, hint string) row {
	return row{section: section, label: label, kind: kindColor, get: func() string { return *field },
		set: func(v string) { *field = v }, effect: eff, hint: hint}
}

func (m *colorsModel) choiceRow(section, label string, field *string, hint string, options ...string) row {
	return row{section: section, label: label, kind: kindChoice, get: func() string { return *field },
		set: func(v string) { *field = v }, options: options, hint: hint}
}

func (m *colorsModel) intRow(section, label string, field *int, lo, hi int, hint string) row {
	return row{section: section, label: label, kind: kindInt, min: lo, max: hi, hint: hint,
		get: func() string { return strconv.Itoa(*field) },
		set: func(v string) { *field, _ = strconv.Atoi(v) }}
}

// bind (re)builds the rows over m.cfg. Call it again after the palette changes.
func (m *colorsModel) bind() {
	c := &m.cfg
	rows := []row{
		m.colorRow("Claude", "working", &c.Claude.Working, &c.Effects.ClaudeWorking, "the light while Claude is working"),
		m.colorRow("Claude", "ready", &c.Claude.Ready, &c.Effects.ClaudeReady, "shown for ready_hold after Claude finishes"),
		m.colorRow("Claude", "waiting", &c.Claude.Waiting, &c.Effects.ClaudeWaiting, "Claude needs you (permission or input)"),
		m.choiceRow("Claude", "mode", &c.Claude.Mode, "override: Claude takes the strip · accent: a 2 s pulse on changes · split: Claude owns some segments · off: ignore hooks", "override", "accent", "split", "off"),
		m.intRow("Claude", "split segments", &c.Claude.SplitSegments, 1, 64, "split mode: how many segments, from segment 0, belong to Claude"),
		m.intRow("Claude", "brightness", &c.Claude.Brightness, 1, 100, "brightness percent of Claude's colors"),
		m.colorRow("Pomodoro", "focus", &c.Pomo.FocusColor, &c.Effects.PomoFocus, "a focus block"),
		m.colorRow("Pomodoro", "short break", &c.Pomo.ShortBreakColor, &c.Effects.PomoShortBreak, "a short break"),
		m.colorRow("Pomodoro", "long break", &c.Pomo.LongBreakColor, &c.Effects.PomoLongBreak, "the long break after every Nth focus block"),
		m.choiceRow("Pomodoro", "progress", &c.Pomo.Progress, "bar: a segment bar on segmented strips · solid: one color", "bar", "solid"),
		m.choiceRow("Pomodoro", "direction", &c.Pomo.ProgressDirection, "drain: the bar empties as time runs out · fill: it fills up", "drain", "fill"),
		m.intRow("Pomodoro", "unfilled dim %", &c.Pomo.ProgressDim, 0, 100, "brightness of the unfilled part of the bar (0 = off)"),
		m.choiceRow("Idle", "action", &c.Idle.Action, "what the light does when nothing else wants it: leave it · turn it off · show the idle color", "leave", "off", "color"),
		m.colorRow("Idle", "color", &c.Idle.Color, nil, "used when the idle action is \"color\""),
	}
	names := make([]string, 0, len(c.Palette))
	for n := range c.Palette {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		n := n
		rows = append(rows, row{section: "Palette", label: n, kind: kindColor, palette: n,
			get: func() string { return c.Palette[n] }, set: func(v string) { c.Palette[n] = v },
			hint: "a named color you can use anywhere a color is accepted (schedule entries, the roles above)"})
	}
	m.rows = rows
	m.cursor = min(m.cursor, len(rows)-1)
}

func (m *colorsModel) capturing() bool { return m.mode != cmBrowse }

func (m *colorsModel) help() []key.Binding {
	switch m.mode {
	case cmEdit, cmName:
		return []key.Binding{ek.Confirm, ek.Palette, ek.Cancel}
	case cmDelete:
		return []key.Binding{key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes, delete")), ek.Cancel}
	}
	return []key.Binding{ck.Move, ck.Edit, ck.Effect, ck.Save, ck.Add, ck.Delete}
}

// paletteNames lists built-in and user names for tab-cycling, sorted.
func paletteNames(cfg config.Config) []string {
	set := map[string]bool{}
	for n := range color.Default() {
		set[n] = true
	}
	for n := range cfg.UserPalette() {
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *colorsModel) rgb(hex string) color.RGB {
	c, err := color.Parse(hex, m.cfg.UserPalette())
	if err != nil {
		return color.RGB{}
	}
	return c
}

func (m *colorsModel) setStatus(msg string, isErr bool) { m.status, m.statusE = msg, isErr }

func (m *colorsModel) cur() *row { return &m.rows[m.cursor] }

func (m *colorsModel) update(msg tea.Msg, now time.Time) tea.Cmd {
	switch msg := msg.(type) {
	case savedMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else {
			m.dirty = false
			m.setStatus("saved "+msg.path, false)
		}
	case tea.KeyMsg:
		switch m.mode {
		case cmEdit, cmName:
			return m.updateInput(msg)
		case cmDelete:
			return m.updateDelete(msg)
		}
		return m.updateBrowse(msg, now)
	}
	return nil
}

func (m *colorsModel) move(d int, now time.Time) {
	m.cursor = (m.cursor + d + len(m.rows)) % len(m.rows)
	m.start = now
}

func (m *colorsModel) updateBrowse(msg tea.KeyMsg, now time.Time) tea.Cmd {
	switch {
	case isKey(msg, ck.Up):
		m.move(-1, now)
	case isKey(msg, ck.Down):
		m.move(1, now)
	case isKey(msg, ck.Edit):
		r := m.cur()
		if r.kind == kindChoice {
			m.cycle(r, 1)
			return nil
		}
		m.mode, m.editing, m.palIdx = cmEdit, true, -1
		m.input.Prompt = r.label + "> "
		m.input.SetValue(r.get())
		m.input.CursorEnd()
		m.setStatus("", false)
		return m.input.Focus()
	case isKey(msg, ck.Effect):
		r := m.cur()
		switch {
		case r.kind == kindChoice:
			m.cycle(r, 1)
		case r.effect != nil:
			k, _ := effect.Parse(*r.effect)
			*r.effect = string(effect.Next(k))
			m.dirty = true
			m.start = now
		}
	case msg.String() == "left" && m.cur().kind == kindChoice:
		m.cycle(m.cur(), -1)
	case isKey(msg, ck.Add):
		m.mode, m.editing = cmName, true
		m.input.Prompt = "new color name> "
		m.input.SetValue("")
		m.setStatus("", false)
		return m.input.Focus()
	case isKey(msg, ck.Delete):
		if m.cur().palette != "" {
			m.mode = cmDelete
		} else {
			m.setStatus("only palette colors can be deleted", true)
		}
	case isKey(msg, ck.Save):
		return m.doSave()
	}
	return nil
}

// cycle moves a choice row through its options.
func (m *colorsModel) cycle(r *row, d int) {
	cur := 0
	for i, o := range r.options {
		if o == r.get() {
			cur = i
		}
	}
	r.set(r.options[(cur+d+len(r.options))%len(r.options)])
	m.dirty = true
}

func (m *colorsModel) leaveInput() {
	m.mode, m.editing = cmBrowse, false
	m.input.Blur()
}

func (m *colorsModel) updateInput(msg tea.KeyMsg) tea.Cmd {
	switch {
	case isKey(msg, ek.Cancel):
		m.leaveInput()
		m.setStatus("", false)
		return nil
	case isKey(msg, ek.Palette):
		if m.mode == cmEdit && m.cur().kind == kindColor {
			names := paletteNames(m.cfg)
			m.palIdx = (m.palIdx + 1) % len(names)
			m.input.SetValue(names[m.palIdx])
			m.input.CursorEnd()
		}
		return nil
	case isKey(msg, ek.Confirm):
		if m.mode == cmName {
			return m.confirmName()
		}
		return m.confirmValue()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *colorsModel) confirmValue() tea.Cmd {
	r := m.cur()
	v := strings.TrimSpace(m.input.Value())
	switch r.kind {
	case kindColor:
		// An empty idle color is allowed: it just means "none set".
		if _, err := color.Parse(v, m.cfg.UserPalette()); err != nil && !(v == "" && r.label == "color" && r.section == "Idle") {
			m.setStatus(err.Error(), true)
			return nil
		}
	case kindInt:
		n, err := strconv.Atoi(v)
		if err != nil || n < r.min || n > r.max {
			m.setStatus(fmt.Sprintf("%s must be a whole number from %d to %d", r.label, r.min, r.max), true)
			return nil
		}
	}
	r.set(v)
	m.dirty = true
	m.leaveInput()
	m.setStatus("", false)
	return nil
}

// confirmName accepts the new palette color's name and goes straight on to
// editing its value, starting from white.
func (m *colorsModel) confirmName() tea.Cmd {
	name := strings.ToLower(strings.TrimSpace(m.input.Value()))
	switch _, exists := m.cfg.Palette[name]; {
	case !paletteName.MatchString(name):
		m.setStatus("use lowercase letters, digits, - and _ (up to 32)", true)
		return nil
	case exists:
		m.setStatus(fmt.Sprintf("%q already exists; select it and press enter to change it", name), true)
		return nil
	}
	if m.cfg.Palette == nil {
		m.cfg.Palette = map[string]string{}
	}
	m.cfg.Palette[name] = "#FFFFFF"
	m.dirty = true
	m.bind()
	for i, r := range m.rows {
		if r.palette == name {
			m.cursor = i
		}
	}
	r := m.cur()
	m.mode, m.editing, m.palIdx = cmEdit, true, -1
	m.input.Prompt = r.label + "> "
	m.input.SetValue(r.get())
	m.input.CursorEnd()
	m.setStatus("", false)
	return m.input.Focus()
}

func (m *colorsModel) updateDelete(msg tea.KeyMsg) tea.Cmd {
	m.mode = cmBrowse
	if msg.String() != "y" {
		return nil
	}
	delete(m.cfg.Palette, m.cur().palette)
	m.dirty = true
	m.bind()
	m.setStatus("deleted (save to keep the change)", false)
	return nil
}

func (m *colorsModel) doSave() tea.Cmd {
	if errs := m.cfg.Validate(); len(errs) > 0 {
		m.setStatus(errs[0].Error(), true)
		return nil
	}
	cfg, path, save := m.cfg, m.path, m.save
	return func() tea.Msg { return savedMsg{path: path, err: save(path, cfg)} }
}

// view draws the tab in at most h rows: a scrolling window of settings kept
// around the cursor, then a live preview and the selected setting's hint.
func (m *colorsModel) view(th theme, now time.Time, h int) string {
	title := "Colors"
	if m.dirty {
		title += " *"
	}

	// Lay the rows out under their section headings, remembering where the
	// cursor's line lands so the window can follow it.
	var lines []string
	curLine, last := 0, ""
	for i, r := range m.rows {
		if r.section != last {
			lines = append(lines, th.dim().Render("  "+r.section))
			last = r.section
		}
		val := r.get()
		if val == "" {
			val = "—"
		}
		text := fmt.Sprintf("%-16s %-10s", r.label, val)
		if r.effect != nil {
			text += fmt.Sprintf(" %-8s", *r.effect)
		}
		marker := "   "
		if r.kind == kindColor {
			if v := r.get(); v != "" {
				marker = lipgloss.NewStyle().Background(lipgloss.Color(m.rgb(v).Hex())).Render("  ") + " "
			}
		}
		line := "  " + marker + text
		if i == m.cursor {
			curLine = len(lines)
			line = th.selected().Render("▸ ") + marker + th.selected().Render(text)
		}
		lines = append(lines, line)
	}
	if len(m.cfg.Palette) == 0 {
		lines = append(lines, th.dim().Render("  Palette"), th.dim().Render("    (none yet: press a to add a named color)"))
	}

	// Footer: preview/hint, the prompt if one is open, and any status.
	r := m.cur()
	var foot []string
	switch r.kind {
	case kindColor:
		kind := effect.Solid
		if r.effect != nil {
			kind, _ = effect.Parse(*r.effect)
		}
		f := effect.Frame(kind, m.rgb(r.get()), 100, now.Sub(m.start))
		pv := f.Color.Scale(f.Brightness)
		foot = append(foot, lipgloss.JoinHorizontal(lipgloss.Center, th.block(pv.Hex(), 10, 2), "  ",
			r.label+"\n"+th.dim().Render(fmt.Sprintf("%s · %s · %d%%", f.Color.Hex(), kind, f.Brightness))))
	default:
		foot = append(foot, th.dim().Render(r.hint))
	}
	if r.kind == kindColor && r.hint != "" {
		foot = append(foot, th.dim().Render(r.hint))
	}
	switch m.mode {
	case cmEdit, cmName:
		foot = append(foot, m.input.View())
	case cmDelete:
		foot = append(foot, th.errText().Render(fmt.Sprintf("Delete the palette color %q? y/n", r.palette)))
	}
	switch {
	case m.status != "" && m.statusE:
		foot = append(foot, th.errText().Render(m.status))
	case m.status != "":
		foot = append(foot, th.okText().Render(m.status))
	}
	footH := 0
	for _, f := range foot {
		footH += strings.Count(f, "\n") + 1
	}

	// Window the rows to what's left.
	avail := max(3, h-1-footH)
	start := 0
	if len(lines) > avail {
		start = min(max(0, curLine-avail/2), len(lines)-avail)
	}
	end := min(len(lines), start+avail)
	window := lines[start:end]
	if start > 0 {
		window[0] = th.dim().Render("  ↑ more")
	}
	if end < len(lines) {
		window[len(window)-1] = th.dim().Render("  ↓ more")
	}
	return strings.Join(append(append([]string{th.title().Render(title)}, window...), foot...), "\n")
}
