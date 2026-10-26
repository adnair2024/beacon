package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/engine"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/pomo"
)

// Pomo tab keys.
type pomoKeys struct{ Toggle, Skip, Stop key.Binding }

var pk = pomoKeys{
	Toggle: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "start/pause")),
	Skip:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "skip")),
	Stop:   key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "stop")),
}

type pomoMsg struct {
	verb string
	line string
	err  error
}

// pomoStatus evaluates the stored timer at the UI's clock, so the countdown
// moves every tick without waiting for a state reload.
func (a *app) pomoStatus() (pomo.Status, error) {
	ps, err := pomo.SettingsFrom(a.cfg)
	if err != nil {
		return pomo.Status{}, err
	}
	return pomo.Evaluate(pomo.Load(a.snap.File), a.now, ps), nil
}

// pomoCmd runs a verb against the shared state file (as the CLI does).
func (a *app) pomoCmd(verb string) tea.Cmd {
	store, cfg := a.o.Store, a.cfg
	return func() tea.Msg {
		ps, err := pomo.SettingsFrom(cfg)
		if err != nil {
			return pomoMsg{verb: verb, err: err}
		}
		st, err := pomo.Do(store, verb, time.Now(), ps)
		return pomoMsg{verb: verb, line: st.String(), err: err}
	}
}

func (a *app) onPomo(m pomoMsg) tea.Cmd {
	if m.err != nil {
		a.setStatus(m.err.Error(), true)
	} else {
		a.setStatus(m.line, false)
	}
	return a.snapCmd()
}

func (a *app) updatePomo(k tea.KeyMsg) tea.Cmd {
	st, err := a.pomoStatus()
	if err != nil {
		a.setStatus(err.Error(), true)
		return nil
	}
	switch {
	case isKey(k, pk.Toggle):
		return a.pomoCmd(st.Toggle())
	case isKey(k, pk.Skip):
		return a.pomoCmd("skip")
	case isKey(k, pk.Stop):
		return a.pomoCmd("stop")
	}
	return nil
}

// phaseHex is the configured color for a phase.
func (a *app) phaseHex(phase string) string {
	src := map[string]string{pomo.Focus: a.cfg.Pomo.FocusColor, pomo.ShortBreak: a.cfg.Pomo.ShortBreakColor,
		pomo.LongBreak: a.cfg.Pomo.LongBreakColor}[phase]
	if c, err := color.Parse(src, a.cfg.UserPalette()); err == nil {
		return c.Hex()
	}
	return "#8A8A8A"
}

// dots renders the cycle: filled for focus blocks done, hollow for the rest.
func dots(done, total int) string {
	if total < 1 {
		return ""
	}
	return strings.Repeat("●", done) + strings.Repeat("○", max(0, total-done))
}

// ---- big digits ----

var glyphs = map[rune][5]string{
	'0': {"███", "█ █", "█ █", "█ █", "███"},
	'1': {" █ ", "██ ", " █ ", " █ ", "███"},
	'2': {"███", "  █", "███", "█  ", "███"},
	'3': {"███", "  █", "███", "  █", "███"},
	'4': {"█ █", "█ █", "███", "  █", "  █"},
	'5': {"███", "█  ", "███", "  █", "███"},
	'6': {"███", "█  ", "███", "█ █", "███"},
	'7': {"███", "  █", "  █", "  █", "  █"},
	'8': {"███", "█ █", "███", "█ █", "███"},
	'9': {"███", "█ █", "███", "  █", "███"},
	':': {" ", "█", " ", "█", " "},
}

// bigClock renders text such as "18:42" five rows tall.
func bigClock(text string) []string {
	rows := make([]string, 5)
	for i, r := range text {
		g, ok := glyphs[r]
		if !ok {
			continue
		}
		for y := range rows {
			if i > 0 {
				rows[y] += " "
			}
			rows[y] += g[y]
		}
	}
	return rows
}

func (a *app) pomoView(th theme, w int) string {
	st, err := a.pomoStatus()
	if err != nil {
		return th.errText().Render(err.Error())
	}
	center := func(s string) string { return lipgloss.PlaceHorizontal(w, lipgloss.Center, s) }
	if !st.Active() {
		return strings.Join([]string{
			th.title().Render("Pomodoro"), "",
			"No session running.",
			th.dim().Render(fmt.Sprintf("Press space to start a %s focus block.", pomo.Clock(a.pomoFocus()))),
			"", th.dim().Render("focus · short break · long break after every " + fmt.Sprint(a.cfg.Pomo.LongEvery) + " blocks"),
		}, "\n")
	}

	shown := st.Phase
	if st.State == pomo.Waiting {
		shown = st.Next
	}
	hex := a.phaseHex(shown)
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(hex))

	state := string(st.State)
	title := accent.Bold(true).Render(strings.ToUpper(pomo.Label(st.Phase))) + th.dim().Render(" · "+state)

	var lines []string
	lines = append(lines, center(title))
	if st.State == pomo.Waiting {
		// Phase-end banner: the next step is yours.
		lines = append(lines, "",
			center(accent.Bold(true).Render(strings.ToUpper(pomo.Label(st.Phase))+" FINISHED")),
			center(fmt.Sprintf("Press space to start the %s (%s)", pomo.Label(st.Next), pomo.Clock(st.Total))), "")
	} else {
		face := accent
		if st.State == pomo.Paused {
			face = th.dim()
		}
		for _, row := range bigClock(pomo.Clock(st.Remaining)) {
			lines = append(lines, center(face.Render(row)))
		}
		if strip := a.pomoStrip(th, st, hex, w); strip != "" {
			lines = append(lines, "", center(strip)) // the real bar, as the segmented strip shows it
		} else {
			bar := progress.New(progress.WithSolidFill(hex), progress.WithoutPercentage(), progress.WithWidth(min(w-4, 48)))
			lines = append(lines, "", center(bar.ViewAs(st.Progress())))
		}
	}
	lines = append(lines, center(accent.Render(dots(st.Dots(), st.LongEvery))+"  "+
		th.dim().Render(fmt.Sprintf("%d focus block%s done", st.Completed, plural(st.Completed)))))
	return strings.Join(lines, "\n")
}

// pomoFocus is the configured focus length, for the idle hint.
func (a *app) pomoFocus() time.Duration {
	if ps, err := pomo.SettingsFrom(a.cfg); err == nil {
		return ps.Focus
	}
	return 25 * time.Minute
}

// pomoStrip draws the segment bar exactly as the engine would send it, or ""
// when the device (or the config) doesn't use a bar.
func (a *app) pomoStrip(th theme, st pomo.Status, hex string, w int) string {
	caps := a.caps()
	off, n, ok := engine.BarLayout(a.cfg, caps)
	if !ok {
		return ""
	}
	c, err := color.Parse(hex, nil)
	if err != nil {
		return ""
	}
	dir := a.cfg.Pomo.ProgressDirection
	segs := engine.BarSegments(a.cfg, caps, st, c)
	cells := cellsFor(govee.State{On: true, Color: &c, Brightness: 100, Segments: segs}, caps)
	var labels []label
	if off > 0 {
		labels = append(labels, label{0, off - 1, "claude"})
	}
	if lit := st.Filled(n, dir); lit > 0 {
		labels = append(labels, label{off, off + lit - 1, "lit"})
		if lit < n {
			labels = append(labels, label{off + lit, off + n - 1, "dim"})
		}
	}
	return renderStrip(th, cells, labels, min(w-4, 50), 1)
}
