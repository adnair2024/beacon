package tui

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

// Schedule tab keys.
type schedKeys struct {
	Move, Add, Edit, Delete, Days, Preview, Enable key.Binding
}

var sk = schedKeys{
	Move:    key.NewBinding(key.WithKeys("up", "down", "k", "j"), key.WithHelp("↑/↓", "move")),
	Add:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
	Edit:    key.NewBinding(key.WithKeys("e", "enter"), key.WithHelp("e", "edit")),
	Delete:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Days:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "days")),
	Preview: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "preview 5s")),
	Enable:  key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "enable/disable")),
}

type schedMode int

const (
	schedBrowse schedMode = iota
	schedEdit
	schedDelete
	schedDays
)

// Field order in the entry editor.
const (
	fTime = iota
	fName
	fPower
	fLight
	fBright
	numFields
)

var fieldLabels = [numFields]string{"time       ", "name       ", "power      ", "light      ", "brightness "}
var fieldHints = [numFields]string{"HH:MM, 24-hour", "optional", "on, off, or blank to inherit", "color, #RRGGBB, 3000k, or blank to inherit", "1-100, or blank to inherit"}

type (
	schedLoadedMsg struct {
		prof   schedule.Profile
		exists bool
		err    error
	}
	schedSavedMsg struct {
		prof schedule.Profile
		what string
		err  error
	}
)

// schedModel is the Schedule tab. Its changes go straight to the JSON file
// (atomically); there is no separate save step. The daemon or TUI engine
// watches the file and applies the result.
type schedModel struct {
	dir, name string
	prof      schedule.Profile
	exists    bool
	loaded    bool
	err       error // from loading the file

	cursor  int
	mode    schedMode
	fields  [numFields]textinput.Model
	field   int
	editIdx int // -1 when adding
	dayIdx  int
	status  string
	statusE bool
	editErr string
}

func newSched(dir, name string) *schedModel {
	m := &schedModel{dir: dir, name: name, editIdx: -1}
	for i := range m.fields {
		t := textinput.New()
		t.Prompt = fieldLabels[i] + "> "
		t.CharLimit = 40
		t.Width = 28
		m.fields[i] = t
	}
	return m
}

func (m *schedModel) capturing() bool { return m.mode != schedBrowse }

func loadSchedCmd(dir, name string) tea.Cmd {
	return func() tea.Msg {
		if dir == "" {
			return schedLoadedMsg{prof: schedule.Default(), err: fmt.Errorf("no schedule directory")}
		}
		p, exists, err := schedule.Load(dir, name)
		return schedLoadedMsg{prof: p, exists: exists, err: err}
	}
}

func (m *schedModel) setStatus(s string, isErr bool) { m.status, m.statusE = s, isErr }

func (m *schedModel) help() []key.Binding {
	switch m.mode {
	case schedEdit:
		return []key.Binding{ek.Confirm, ek.Field, ek.Cancel}
	case schedDelete:
		return []key.Binding{key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes, delete")), ek.Cancel}
	case schedDays:
		return []key.Binding{key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "move")),
			key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")), ek.Cancel}
	}
	return []key.Binding{sk.Move, sk.Add, sk.Edit, sk.Delete, sk.Days, sk.Preview, sk.Enable}
}

// rows is the timeline in time order. Entries are kept sorted on disk once
// the editor has touched them, so row i is Entries[i] after Sorted().
func (m *schedModel) rows(pal color.Palette) []schedule.Row {
	rows, _ := m.prof.Timeline(pal)
	return rows
}

// update handles messages for the tab. env supplies what only the app knows.
func (m *schedModel) update(msg tea.Msg, env schedEnv) tea.Cmd {
	switch msg := msg.(type) {
	case schedLoadedMsg:
		if m.capturing() {
			return nil // never clobber an edit in progress
		}
		m.err, m.loaded = msg.err, true
		if msg.err == nil {
			m.prof, m.exists = msg.prof.Sorted(), msg.exists
			m.cursor = min(m.cursor, max(0, len(m.prof.Entries)-1))
		}
	case schedSavedMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return nil
		}
		m.prof, m.exists, m.err = msg.prof.Sorted(), true, nil
		m.setStatus("saved: "+msg.what, false)
		return env.refresh
	case tea.KeyMsg:
		switch m.mode {
		case schedEdit:
			return m.updateEdit(msg, env)
		case schedDelete:
			return m.updateDelete(msg, env)
		case schedDays:
			return m.updateDays(msg, env)
		}
		return m.updateBrowse(msg, env)
	}
	return nil
}

// schedEnv is what the tab needs from the app.
type schedEnv struct {
	pal     color.Palette
	store   *state.Store
	refresh tea.Cmd
}

func (m *schedModel) save(env schedEnv, p schedule.Profile, what string) tea.Cmd {
	dir, name, pal := m.dir, m.name, env.pal
	return func() tea.Msg {
		err := schedule.Save(dir, name, p, pal)
		return schedSavedMsg{prof: p, what: what, err: err}
	}
}

func (m *schedModel) updateBrowse(k tea.KeyMsg, env schedEnv) tea.Cmd {
	n := len(m.prof.Entries)
	switch {
	case k.String() == "up" || k.String() == "k":
		if n > 0 {
			m.cursor = (m.cursor + n - 1) % n
		}
	case k.String() == "down" || k.String() == "j":
		if n > 0 {
			m.cursor = (m.cursor + 1) % n
		}
	case isKey(k, sk.Add):
		return m.openEdit(-1)
	case isKey(k, sk.Edit):
		if n > 0 {
			return m.openEdit(m.cursor)
		}
	case isKey(k, sk.Delete):
		if n > 0 {
			m.mode = schedDelete
		}
	case isKey(k, sk.Days):
		m.mode, m.dayIdx = schedDays, 0
	case isKey(k, sk.Enable):
		p := m.prof
		p.SetEnabled(!p.IsEnabled())
		return m.save(env, p, map[bool]string{true: "schedule enabled", false: "schedule disabled"}[p.IsEnabled()])
	case isKey(k, sk.Preview):
		return m.preview(env)
	}
	return nil
}

// preview lights the selected row for five seconds by setting a short manual
// override, which the engine reverts on its own when it expires.
func (m *schedModel) preview(env schedEnv) tea.Cmd {
	rows := m.rows(env.pal)
	if m.cursor >= len(rows) {
		return nil
	}
	r := rows[m.cursor]
	if r.Ignored {
		m.setStatus("that entry never takes effect (it comes after a power-off)", true)
		return nil
	}
	st, store := r.State, env.store
	return func() tea.Msg {
		exp := time.Now().Add(5 * time.Second)
		err := store.Update(func(f *state.File) error {
			f.Override = &state.Override{State: st, ExpiresAt: exp}
			return nil
		})
		return overrideMsg{verb: "preview", exp: exp, err: err}
	}
}

func (m *schedModel) openEdit(idx int) tea.Cmd {
	m.mode, m.editIdx, m.field, m.editErr = schedEdit, idx, 0, ""
	var e schedule.Entry
	if idx >= 0 {
		e = m.prof.Entries[idx]
	}
	vals := [numFields]string{e.At, e.Name, "", "", ""}
	if e.Power != nil {
		vals[fPower] = map[bool]string{true: "on", false: "off"}[*e.Power]
	}
	switch {
	case e.Color != "":
		vals[fLight] = e.Color
	case e.Kelvin != 0:
		vals[fLight] = fmt.Sprintf("%dk", e.Kelvin)
	}
	if e.Brightness != 0 {
		vals[fBright] = strconv.Itoa(e.Brightness)
	}
	for i := range m.fields {
		m.fields[i].SetValue(vals[i])
		m.fields[i].Blur()
	}
	return m.fields[0].Focus()
}

// entryFromFields parses the editor fields. Validation of the whole profile
// (duplicate times, colors, ranges) happens afterwards, with the real rules.
func (m *schedModel) entryFromFields() (schedule.Entry, error) {
	v := func(i int) string { return strings.TrimSpace(m.fields[i].Value()) }
	e := schedule.Entry{At: v(fTime), Name: v(fName)}
	switch strings.ToLower(v(fPower)) {
	case "":
	case "on", "true", "yes":
		t := true
		e.Power = &t
	case "off", "false", "no":
		f := false
		e.Power = &f
	default:
		return e, fmt.Errorf("power: use on, off, or leave blank")
	}
	if l := v(fLight); l != "" {
		if strings.HasSuffix(strings.ToLower(l), "k") {
			if k, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(l), "k")); err == nil {
				e.Kelvin = k
				goto bright
			}
		}
		e.Color = l
	}
bright:
	if b := v(fBright); b != "" {
		n, err := strconv.Atoi(b)
		if err != nil || n < 1 || n > 100 {
			return e, fmt.Errorf("brightness: use 1-100, or leave blank")
		}
		e.Brightness = n
	}
	return e, nil
}

func (m *schedModel) focusField(i int) tea.Cmd {
	m.field = (i + numFields) % numFields
	var cmd tea.Cmd
	for j := range m.fields {
		if j == m.field {
			cmd = m.fields[j].Focus()
		} else {
			m.fields[j].Blur()
		}
	}
	return cmd
}

func (m *schedModel) updateEdit(k tea.KeyMsg, env schedEnv) tea.Cmd {
	switch {
	case isKey(k, ek.Cancel):
		m.mode, m.editErr = schedBrowse, ""
		return nil
	case k.String() == "down" || k.String() == "tab":
		return m.focusField(m.field + 1)
	case k.String() == "up" || k.String() == "shift+tab":
		return m.focusField(m.field - 1)
	case isKey(k, ek.Confirm):
		if m.field < numFields-1 { // enter walks forward; on the last field it saves
			return m.focusField(m.field + 1)
		}
		e, err := m.entryFromFields()
		if err != nil {
			m.editErr = err.Error()
			return nil
		}
		p := m.prof
		p.Entries = append([]schedule.Entry(nil), p.Entries...)
		what := "added " + e.At
		if m.editIdx >= 0 {
			p.Entries[m.editIdx] = e
			what = "updated " + e.At
		} else {
			p.Entries = append(p.Entries, e)
		}
		p = p.Sorted()
		if errs := p.Validate(env.pal); len(errs) > 0 {
			m.editErr = errs[0].Error()
			return nil
		}
		m.mode, m.editErr = schedBrowse, ""
		return m.save(env, p, what)
	}
	var cmd tea.Cmd
	m.fields[m.field], cmd = m.fields[m.field].Update(k)
	m.editErr = ""
	return cmd
}

func (m *schedModel) updateDelete(k tea.KeyMsg, env schedEnv) tea.Cmd {
	m.mode = schedBrowse
	if k.String() != "y" {
		return nil
	}
	p := m.prof
	e := p.Entries[m.cursor]
	p.Entries = append(append([]schedule.Entry(nil), p.Entries[:m.cursor]...), p.Entries[m.cursor+1:]...)
	return m.save(env, p, "deleted "+e.At)
}

func (m *schedModel) updateDays(k tea.KeyMsg, env schedEnv) tea.Cmd {
	switch k.String() {
	case "esc", "t", "q":
		m.mode = schedBrowse
	case "left", "h":
		m.dayIdx = (m.dayIdx + 6) % 7
	case "right", "l":
		m.dayIdx = (m.dayIdx + 1) % 7
	case " ", "enter":
		day := schedule.AllDays[m.dayIdx]
		var days []string
		on := false
		for _, d := range schedule.AllDays {
			has := false
			for _, x := range m.prof.Days {
				has = has || x == d
			}
			if d == day {
				has = !has
				on = has
			}
			if has {
				days = append(days, d)
			}
		}
		p := m.prof
		p.Days = days
		return m.save(env, p, fmt.Sprintf("%s %s", day, map[bool]string{true: "on", false: "off"}[on]))
	}
	return nil
}

// ---- view ----

func (m *schedModel) view(th theme, w int, info schedule.Info, pal color.Palette) string {
	if !m.loaded {
		return th.dim().Render("loading…")
	}
	if m.err != nil {
		return th.errText().Render(m.err.Error()) + "\n\n" + th.dim().Render("Fix the file by hand, or delete it to go back to the built-in default.")
	}
	if m.mode == schedEdit {
		return m.editView(th)
	}
	enabled := "enabled"
	if !m.prof.IsEnabled() {
		enabled = th.errText().Render("DISABLED")
	}
	tz := m.prof.Timezone
	if tz == "" {
		tz = "system time zone"
	}
	// Show the short form (schedules/workday.json); `beacon schedule path`
	// prints the full location.
	short := path.Join(filepath.Base(m.dir), m.name+".json") // always "/": it's a hint, not a path to open
	src := th.dim().Render(short)
	if !m.exists {
		src = th.dim().Render("built-in default; the first change creates " + short)
	}
	head := []string{
		th.title().Render(m.name) + th.dim().Render(" · ") + enabled + th.dim().Render(" · "+tz),
		m.daysLine(th),
		src,
		"",
	}

	rows := m.rows(pal)
	if len(rows) == 0 {
		head = append(head, th.dim().Render("No entries. Press a to add one."))
	}
	active := ""
	if info.Current != nil {
		active = info.Current.Entry.At
	}
	for i, r := range rows {
		hex := th.offHex()
		if r.State.On {
			hex = cellsFor(r.State, govee.Capabilities{})[0].c.Hex()
		}
		sw := lipgloss.NewStyle().Background(lipgloss.Color(hex)).Render("  ")
		cur := "  "
		if i == m.cursor {
			cur = "▸ "
		}
		note := ""
		switch {
		case r.Ignored:
			note = th.dim().Render("  ignored (after a power-off)")
		case r.Entry.At == active && m.prof.IsEnabled():
			note = th.okText().Render("  ● now")
		}
		body := fmt.Sprintf("%-5s %-13s %-12s", r.Entry.At, trunc(r.Entry.Name, 13), trunc(describeRow(r), 12))
		if i == m.cursor {
			body = th.selected().Render(body)
		}
		line := cur + sw + " " + body
		head = append(head, line+note)
	}
	switch m.mode {
	case schedDelete:
		if m.cursor < len(rows) {
			head = append(head, "", th.errText().Render(fmt.Sprintf("Delete %s %q? y/n", rows[m.cursor].Entry.At, rows[m.cursor].Entry.Name)))
		}
	}
	if m.status != "" {
		st := th.okText()
		if m.statusE {
			st = th.errText()
		}
		head = append(head, "", st.Render(m.status))
	}
	return strings.Join(head, "\n")
}

func (m *schedModel) daysLine(th theme) string {
	var parts []string
	for i, d := range schedule.AllDays {
		on := false
		for _, x := range m.prof.Days {
			on = on || x == d
		}
		label := strings.ToUpper(d[:1]) + d[1:]
		switch {
		case m.mode == schedDays && i == m.dayIdx && on:
			parts = append(parts, th.selected().Render("["+label+"]"))
		case m.mode == schedDays && i == m.dayIdx:
			parts = append(parts, th.dim().Render("["+label+"]"))
		case on:
			parts = append(parts, " "+label+" ")
		default:
			parts = append(parts, th.dim().Render(" "+strings.Repeat("·", len(label))+" "))
		}
	}
	return strings.Join(parts, "")
}

func (m *schedModel) editView(th theme) string {
	title := "Add entry"
	if m.editIdx >= 0 {
		title = "Edit entry"
	}
	lines := []string{th.title().Render(title)}
	for i := range m.fields {
		lines = append(lines, m.fields[i].View())
	}
	lines = append(lines, "", th.dim().Render(fieldHints[m.field]))
	if m.editErr != "" {
		lines = append(lines, th.errText().Render(m.editErr))
	}
	return strings.Join(lines, "\n")
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// describeRow summarizes what an entry changes.
func describeRow(r schedule.Row) string {
	e := r.Entry
	var parts []string
	switch {
	case e.Power != nil && !*e.Power:
		parts = append(parts, "off")
	case e.Color != "":
		parts = append(parts, e.Color)
	case e.Kelvin != 0:
		parts = append(parts, fmt.Sprintf("%dK", e.Kelvin))
	}
	if e.Brightness != 0 {
		parts = append(parts, fmt.Sprintf("%d%%", e.Brightness))
	}
	if len(parts) == 0 {
		return "(no change)"
	}
	return strings.Join(parts, " ")
}
