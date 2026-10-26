package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/state"
)

const (
	minW, minH = 60, 18
	frameEvery = 100 * time.Millisecond
	snapEvery  = 5  // ticks between state reloads (~500 ms)
	logEvery   = 10 // ticks between log reloads while the Log tab is open
)

// Mode says who drives the light.
type Mode int

const (
	// ModeEngine: this TUI runs the engine loop and drives the light.
	ModeEngine Mode = iota
	// ModeViewer: a daemon (or another TUI) drives; this one reads state and
	// sends commands through the state file.
	ModeViewer
	// ModeOffline: nothing drives the light (no device or key); the TUI
	// still shows what the engine would do and can edit settings.
	ModeOffline
)

// Options configures the TUI.
type Options struct {
	Cfg        config.Config
	ConfigPath string
	Store      *state.Store
	Targets    []govee.Device
	Mode       Mode
	Note       string // viewer: "daemon pid N"; offline: why
	LogPath    string
	SchedDir   string // where schedule JSON lives
	Dry        bool
	Dark       bool // terminal has a dark background; set by Run via detectDark
	// Engine runs the engine loop until ctx ends; onErr reports apply
	// failures. Only set in ModeEngine.
	Engine func(ctx context.Context, onErr func(error)) error
	Save   saveFunc         // defaults to config.Save
	Now    func() time.Time // defaults to time.Now
}

type tab int

const (
	tabDashboard tab = iota
	tabPomo
	tabSchedule
	tabDevices
	tabLog
	tabColors
	numTabs
)

var tabNames = [numTabs]string{"Dashboard", "Pomo", "Schedule", "Devices", "Log", "Colors"}

// Messages.
type (
	tickMsg time.Time
	snapMsg struct{ s snapshot }
	errMsg  struct{ err error }
	// engineDoneMsg: the engine goroutine ended (nil err = clean shutdown).
	engineDoneMsg struct{ err error }
	// (pomoMsg is defined in pomo.go)
	// overrideMsg reports a set/clear of the manual override.
	overrideMsg struct {
		verb string
		exp  time.Time
		err  error
	}
)

type app struct {
	o        Options
	cfg      config.Config
	tab      tab
	w, h     int
	now      time.Time
	ticks    int
	snap     snapshot
	snapBusy bool
	colors   *colorsModel
	logs     *logsModel
	sched    *schedModel
	picker   picker
	help     help.Model
	helpOn   bool
	status   string
	statusE  bool
}

func newApp(o Options) *app {
	if o.Save == nil {
		o.Save = config.Save
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	now := o.Now()
	return &app{
		o: o, cfg: o.Cfg, now: now, help: help.New(), picker: newPicker(), logs: newLogs(),
		colors: newColors(o.Cfg, o.ConfigPath, o.Save, now),
		sched:  newSched(o.SchedDir, o.Cfg.Schedule.Profile),
	}
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(tick(), a.snapCmd(), tailLog(a.o.LogPath), loadSchedCmd(a.o.SchedDir, a.cfg.Schedule.Profile))
}

func tick() tea.Cmd { return tea.Tick(frameEvery, func(t time.Time) tea.Msg { return tickMsg(t) }) }

func (a *app) snapCmd() tea.Cmd {
	a.snapBusy = true
	cfg, store, dir, caps := a.cfg, a.o.Store, a.o.SchedDir, a.caps()
	return func() tea.Msg { return snapMsg{takeSnapshot(cfg, store, dir, caps, time.Now())} }
}

func (a *app) setStatus(msg string, isErr bool) { a.status, a.statusE = msg, isErr }

func (a *app) capturing() bool {
	return a.picker.open || (a.tab == tabColors && a.colors.capturing()) || (a.tab == tabSchedule && a.sched.capturing())
}

func (a *app) caps() govee.Capabilities {
	if len(a.o.Targets) > 0 {
		return a.o.Targets[0].Caps
	}
	return govee.Capabilities{}
}

// chrome is the fixed rows around the body: border (2), header, tabs, two
// rules, and the status line. The footer's height varies and is added on top.
const chrome = 7

func (a *app) bodyHeight() int { return max(5, a.h-chrome-1) }
func (a *app) innerWidth() int { return max(20, a.w-6) }

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		a.logs.resize(a.innerWidth(), a.bodyHeight())
		return a, nil
	case tickMsg:
		a.now = time.Time(msg)
		a.ticks++
		cmds := []tea.Cmd{tick()}
		if a.ticks%snapEvery == 0 && !a.snapBusy {
			cmds = append(cmds, a.snapCmd())
		}
		if a.tab == tabLog && a.ticks%logEvery == 0 {
			cmds = append(cmds, tailLog(a.o.LogPath))
		}
		return a, tea.Batch(cmds...)
	case snapMsg:
		a.snap, a.snapBusy = msg.s, false
		return a, nil
	case logMsg:
		a.logs.set(msg)
		return a, nil
	case savedMsg:
		if msg.err == nil {
			a.cfg = a.colors.cfg
		}
		return a, a.colors.update(msg, a.now)
	case errMsg:
		a.setStatus("light: "+msg.err.Error(), true)
		return a, nil
	case engineDoneMsg:
		switch {
		case msg.err == nil:
		case errors.Is(msg.err, state.ErrEngineRunning):
			a.o.Mode, a.o.Note = ModeViewer, "another beacon took over"
			a.setStatus("another beacon is driving the light; this one is now a viewer", false)
		default:
			a.o.Mode, a.o.Note = ModeOffline, msg.err.Error()
			a.setStatus("engine stopped: "+msg.err.Error(), true)
		}
		return a, nil
	case overrideMsg:
		return a, a.onOverride(msg)
	case pomoMsg:
		return a, a.onPomo(msg)
	case schedLoadedMsg, schedSavedMsg:
		return a, a.sched.update(msg, a.schedEnv())
	case tea.KeyMsg:
		return a, a.handleKey(msg)
	}
	return a, a.logs.update(msg) // mouse wheel etc.
}

func (a *app) onOverride(m overrideMsg) tea.Cmd {
	switch {
	case m.err != nil:
		a.setStatus(m.err.Error(), true)
	case m.verb == "clear":
		a.setStatus("override cleared", false)
	case m.verb == "preview":
		a.setStatus("previewing for 5 seconds", false)
	default:
		s := "override set"
		if !m.exp.IsZero() {
			s += " until " + m.exp.Local().Format("15:04")
		}
		if a.o.Mode == ModeOffline {
			s += " (no engine is running, so nothing will apply it)"
		}
		a.setStatus(s, false)
	}
	return a.snapCmd()
}

func (a *app) handleKey(k tea.KeyMsg) tea.Cmd {
	if k.String() == "ctrl+c" {
		return tea.Quit
	}
	if a.picker.open {
		cmd, st := a.picker.update(k, a.cfg.UserPalette())
		if st != nil {
			return a.setOverride(*st)
		}
		return cmd
	}
	if a.tab == tabColors && a.colors.capturing() {
		return a.colors.update(k, a.now)
	}
	if a.tab == tabSchedule && a.sched.capturing() {
		return a.sched.update(k, a.schedEnv())
	}
	switch {
	case isKey(k, gk.Quit):
		return tea.Quit
	case isKey(k, gk.Next):
		a.tab = (a.tab + 1) % numTabs
	case isKey(k, gk.Prev):
		a.tab = (a.tab + numTabs - 1) % numTabs
	case isKey(k, gk.Tab):
		a.tab = tab(k.String()[0] - '1')
	case isKey(k, gk.Help):
		a.helpOn = !a.helpOn
	case isKey(k, gk.Override):
		return a.picker.show(paletteNames(a.cfg))
	case isKey(k, gk.Clear):
		return a.clearOverride()
	default:
		switch a.tab {
		case tabPomo:
			return a.updatePomo(k)
		case tabSchedule:
			return a.sched.update(k, a.schedEnv())
		case tabColors:
			return a.colors.update(k, a.now)
		case tabLog:
			return a.logs.update(k)
		}
	}
	switch a.tab {
	case tabLog:
		return tailLog(a.o.LogPath)
	case tabSchedule:
		if !a.sched.capturing() { // pick up edits made by hand or by another process
			return loadSchedCmd(a.o.SchedDir, a.cfg.Schedule.Profile)
		}
	}
	return nil
}

func (a *app) schedEnv() schedEnv {
	return schedEnv{pal: a.cfg.UserPalette(), store: a.o.Store, refresh: a.snapCmd()}
}

// setOverride writes a manual override to the state file. Whoever drives
// the light (this TUI or a daemon) notices the change and applies it.
func (a *app) setOverride(st govee.State) tea.Cmd {
	d, err := time.ParseDuration(a.cfg.Override.DefaultDuration)
	if err != nil || d <= 0 {
		d = 15 * time.Minute
	}
	exp := time.Now().Add(d)
	store := a.o.Store
	return func() tea.Msg {
		err := store.Update(func(f *state.File) error {
			f.Override = &state.Override{State: st, ExpiresAt: exp}
			return nil
		})
		return overrideMsg{verb: "set", exp: exp, err: err}
	}
}

func (a *app) clearOverride() tea.Cmd {
	store := a.o.Store
	return func() tea.Msg {
		return overrideMsg{verb: "clear", err: store.Update(func(f *state.File) error {
			f.Override = nil
			return nil
		})}
	}
}

// ---- view ----

func (a *app) theme() theme {
	accent := "#8A8A8A"
	if a.snap.At != (time.Time{}) {
		accent = a.snap.accentHex()
	}
	return newTheme(accent, a.o.Dark)
}

func (a *app) helpKeys(w int) keyMap {
	var local []key.Binding
	switch {
	case a.picker.open:
		return keyMap{short: a.picker.help(), full: a.picker.help()}
	case a.tab == tabColors:
		local = a.colors.help()
		if a.colors.capturing() {
			return keyMap{short: local, full: local}
		}
	case a.tab == tabLog:
		local = logKeys
	case a.tab == tabPomo:
		local = []key.Binding{pk.Toggle, pk.Skip, pk.Stop}
	case a.tab == tabSchedule:
		local = a.sched.help()
		if a.sched.capturing() {
			return keyMap{short: local, full: local}
		}
	}
	full := append(append([]key.Binding{}, local...), gk.Tab, gk.Next, gk.Prev, gk.Override, gk.Clear, gk.Help, gk.Quit)
	// Short help: drop the least important bindings until it fits the width.
	// The help widget's own truncation is unreliable, so measure instead.
	drop := []key.Binding{gk.Clear, gk.Override}
	short := append(append([]key.Binding{}, local...), gk.Override, gk.Clear, gk.Help, gk.Quit)
	fits := func() bool { return lipgloss.Width(help.New().ShortHelpView(short)) <= w }
	for _, d := range drop {
		if fits() {
			break
		}
		short = without(short, d)
	}
	for !fits() && len(short) > 2 { // then trim local keys from the end
		short = append(short[:len(short)-3:len(short)-3], gk.Help, gk.Quit)
		if len(short) < 2 {
			break
		}
	}
	return keyMap{short: short, full: full}
}

func without(bs []key.Binding, drop key.Binding) []key.Binding {
	out := make([]key.Binding, 0, len(bs))
	for _, b := range bs {
		if b.Help().Key != drop.Help().Key {
			out = append(out, b)
		}
	}
	return out
}

func (a *app) View() string {
	if a.w > 0 && (a.w < minW || a.h < minH) {
		return fmt.Sprintf("terminal too small (%dx%d), need at least %dx%d", a.w, a.h, minW, minH)
	}
	th := a.theme()
	w := a.innerWidth()
	rule := th.dim().Render(strings.Repeat("─", w))

	a.help.Width = w
	a.help.ShowAll = a.helpOn
	keys := a.helpKeys(w)
	foot := a.help.View(keys)
	if a.helpOn {
		foot = fullHelp(th, keys.full, w)
	}
	// The footer grows with full help or a wrapped line; the body gives way
	// so the frame never outgrows the terminal.
	bh := max(3, a.h-chrome-(strings.Count(foot, "\n")+1))

	var body string
	switch {
	case a.picker.open:
		body = a.picker.view(th)
	default:
		switch a.tab {
		case tabDashboard:
			body = a.dashboardView(th, w)
		case tabPomo:
			body = a.pomoView(th, w)
		case tabSchedule:
			body = a.sched.view(th, w, a.snap.Sched, a.cfg.UserPalette())
		case tabDevices:
			body = a.devicesView(th)
		case tabLog:
			body = a.logs.view(th, bh)
		case tabColors:
			body = a.colors.view(th, a.now, bh)
		}
	}
	// Wrap to the frame width *before* clamping the height, or the frame
	// wraps long lines afterwards and the result grows past the terminal.
	body = lipgloss.NewStyle().Width(w).Height(bh).MaxHeight(bh).Render(body)

	status := ""
	one := lipgloss.NewStyle().MaxWidth(w) // a status line is one row; long errors are cut
	switch {
	case a.status != "" && a.statusE:
		status = one.Render(th.errText().Render(a.status))
	case a.status != "":
		status = one.Render(th.okText().Render(a.status))
	}

	out := strings.Join([]string{a.header(th, w), a.tabBar(th, w), rule, body, rule, status, foot}, "\n")
	return th.frame().Width(max(a.w-2, minW-2)).Render(out)
}

func (a *app) modeTag() string {
	tag := map[Mode]string{ModeEngine: "engine", ModeViewer: "viewer", ModeOffline: "offline"}[a.o.Mode]
	if a.o.Dry {
		tag += ", dry-run"
	}
	return tag
}

func (a *app) header(th theme, w int) string {
	left := th.title().Render("beacon") + th.dim().Render(" · "+a.modeTag())
	right := th.dim().Render("no device")
	if len(a.o.Targets) > 0 {
		d := a.o.Targets[0]
		right = fmt.Sprintf("%s %s  %s  %s", th.dot(a.snap.accentHexOr("#8A8A8A")), d.Name, th.dim().Render(d.SKU), th.dim().Render(d.Backend))
		if n := len(a.o.Targets); n > 1 {
			right += th.dim().Render(fmt.Sprintf(" +%d", n-1))
		}
	}
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

var shortTabNames = [numTabs]string{"Dash", "Pomo", "Sched", "Dev", "Log", "Colors"}

func (a *app) tabBar(th theme, w int) string {
	names := tabNames
	// Compact names when the full bar wouldn't fit (e.g. 60 columns).
	total := int(numTabs) - 1
	for i, n := range names {
		total += len(fmt.Sprintf("%d %s", i+1, n)) + 2
	}
	if total > w {
		names = shortTabNames
	}
	parts := make([]string, numTabs)
	for i, n := range names {
		s := fmt.Sprintf("%d %s", i+1, n)
		if tab(i) == a.tab {
			parts[i] = th.tabActive().Render("[" + s + "]")
		} else {
			parts[i] = th.tabIdle().Render(" " + s + " ")
		}
	}
	return strings.Join(parts, " ")
}

func (s snapshot) accentHexOr(fallback string) string {
	if s.At == (time.Time{}) {
		return fallback
	}
	return s.accentHex()
}

// describeState is a short human description of a light state.
func describeState(st govee.State) string {
	if !st.On {
		return "off"
	}
	var parts []string
	switch {
	case st.Color != nil:
		parts = append(parts, st.Color.Hex())
	case st.Kelvin != 0:
		parts = append(parts, fmt.Sprintf("%dK", st.Kelvin))
	default:
		parts = append(parts, "on")
	}
	if st.Brightness != 0 {
		parts = append(parts, fmt.Sprintf("%d%%", st.Brightness))
	}
	return strings.Join(parts, " · ")
}

func (a *app) claudeHex(status string) string {
	src := map[string]string{"working": a.cfg.Claude.Working, "ready": a.cfg.Claude.Ready, "waiting": a.cfg.Claude.Waiting}[status]
	if c, err := color.Parse(src, a.cfg.UserPalette()); err == nil {
		return c.Hex()
	}
	return "#8A8A8A"
}

// fullHelp lays all bindings out in aligned columns, using the fewest rows
// that fit the width. (bubbles' own full help breaks when columns don't fit.)
func fullHelp(th theme, bs []key.Binding, w int) string {
	if len(bs) == 0 {
		return ""
	}
	build := func(rows int) (string, int) {
		cols := (len(bs) + rows - 1) / rows
		lines := make([]string, rows)
		total := 0
		for c := 0; c < cols; c++ {
			kw, dw := 0, 0
			var cell []key.Help
			for r := 0; r < rows && c*rows+r < len(bs); r++ {
				h := bs[c*rows+r].Help()
				cell = append(cell, h)
				kw, dw = max(kw, lipgloss.Width(h.Key)), max(dw, lipgloss.Width(h.Desc))
			}
			cw := kw + 1 + dw
			for r := 0; r < rows; r++ {
				text := strings.Repeat(" ", cw)
				if r < len(cell) {
					text = cell[r].Key + strings.Repeat(" ", kw-lipgloss.Width(cell[r].Key)+1) +
						th.dim().Render(cell[r].Desc) + strings.Repeat(" ", dw-lipgloss.Width(cell[r].Desc))
				}
				if c > 0 {
					lines[r] += "   "
				}
				lines[r] += text
			}
			total += cw
			if c > 0 {
				total += 3
			}
		}
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.Join(lines, "\n"), total
	}
	for rows := 1; rows <= len(bs); rows++ {
		if out, total := build(rows); total <= w || rows == len(bs) {
			return out
		}
	}
	return ""
}
