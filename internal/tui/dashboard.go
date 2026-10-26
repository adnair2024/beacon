package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/adnair2024/beacon/internal/engine"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/modes/schedule"
)

func (a *app) dashboardView(th theme, w int) string {
	s := a.snap
	if s.Err != nil {
		return th.errText().Render(s.Err.Error())
	}
	if s.At.IsZero() {
		return th.dim().Render("loading…")
	}

	// What the strip says it is.
	text := string(s.Res.Source)
	switch {
	case s.Res.Source == engine.SourceNone && s.Placed:
		text = "left as is"
	case s.Res.Source == engine.SourceNone:
		text = "idle"
	case s.Claude.Status != "" && s.Res.Source == engine.SourceClaude:
		text = "claude · " + s.Claude.Status
	}
	cells := cellsFor(s.Show, a.caps())
	labels := []label{{0, len(cells) - 1, text}}
	if k := s.Res.Split; k > 0 && k < len(cells) { // split mode: Claude's segments, then whatever is below
		below, _, _ := strings.Cut(s.Res.BelowReason, ":")
		labels = []label{{0, k - 1, "claude"}, {k, len(cells) - 1, strings.TrimSpace(below)}}
	}
	strip := renderStrip(th, cells, labels, min(w, 60), 2)

	now := "Now: " + s.Res.Reason
	if s.Res.Source == engine.SourceNone {
		now = "Now: nothing active · the light is left alone"
	}

	desc := describeState(s.Show)
	if s.Res.Source == engine.SourceNone && !s.Placed {
		desc = "no light state recorded yet"
	}
	lines := []string{strip, "", now, th.dim().Render(desc), ""}

	// Claude.
	claudeLine := th.dim().Render("no sessions")
	if s.Claude.Status != "" {
		claudeLine = fmt.Sprintf("%s %s  %s", th.dot(a.claudeHex(s.Claude.Status)), s.Claude.Status,
			th.dim().Render(fmt.Sprintf("%d session%s · mode %s", s.Claude.Live, plural(s.Claude.Live), a.cfg.Claude.Mode)))
	}
	lines = append(lines,
		fmt.Sprintf("%-10s%s", "Claude", claudeLine),
		fmt.Sprintf("%-10s%s", "Pomo", a.pomoSummary(th)),
		fmt.Sprintf("%-10s%s", "Sched.", a.schedSummary(th)),
	)
	if n := s.Sched.Next; n != nil && s.Sched.Enabled {
		lines = append(lines, fmt.Sprintf("%-10s%s", "Next", th.dim().Render(fmt.Sprintf("%s at %s (in %s)",
			n.Entry.Name, n.Starts.Local().Format("15:04"), schedule.HumanIn(n.Starts.Sub(s.At))))))
	}

	if o := s.File.Override; o != nil && (o.ExpiresAt.IsZero() || s.At.Before(o.ExpiresAt)) {
		until := "until cleared"
		if !o.ExpiresAt.IsZero() {
			until = fmt.Sprintf("until %s (%s left)", o.ExpiresAt.Local().Format("15:04"),
				o.ExpiresAt.Sub(s.At).Round(time.Second))
		}
		lines = append(lines, "", fmt.Sprintf("%-10s%s %s", "Override", describeState(o.State), th.dim().Render(until+" · c to clear")))
	}

	lines = append(lines, "", th.dim().Render(a.engineLine()))
	return strings.Join(lines, "\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// engineLine says who is driving the light.
func (a *app) engineLine() string {
	names := make([]string, len(a.o.Targets))
	for i, d := range a.o.Targets {
		names[i] = d.Name
	}
	switch a.o.Mode {
	case ModeEngine:
		return "Driving " + strings.Join(names, ", ") + " from this window; quitting stops the engine but leaves the light as it is."
	case ModeViewer:
		return "Viewing: " + a.o.Note + " is driving the light. Overrides you set here are sent through the state file."
	default:
		reason := a.o.Note
		if reason == "" {
			reason = "no device"
		}
		return "Offline: " + reason + ". Showing what the engine would do; nothing is sent to a light."
	}
}

func (a *app) devicesView(th theme) string {
	if len(a.o.Targets) == 0 {
		return th.dim().Render("No devices resolved.\n" + a.o.Note + "\n\nRun `beacon devices list` and add [[devices]] to the config.")
	}
	var b strings.Builder
	b.WriteString(th.title().Render("Devices") + "\n")
	for _, d := range a.o.Targets {
		fmt.Fprintf(&b, "\n%s %s  %s  %s\n  %s\n", th.dot(a.snap.accentHexOr("#8A8A8A")), d.Name,
			th.dim().Render(d.SKU), th.dim().Render(d.Backend), d.Caps)
	}
	b.WriteString("\n" + th.dim().Render("Capabilities come from the Govee API (cached in the config by `beacon devices refresh`)."))
	return b.String()
}

// pomoSummary is the dashboard's one-line pomodoro status.
func (a *app) pomoSummary(th theme) string {
	st, err := a.pomoStatus()
	switch {
	case err != nil:
		return th.errText().Render(err.Error())
	case !st.Active():
		return th.dim().Render("idle")
	}
	shown := st.Phase
	if st.State == pomo.Waiting {
		shown = st.Next
	}
	return fmt.Sprintf("%s %s  %s", th.dot(a.phaseHex(shown)), st.String(), th.dim().Render(dots(st.Dots(), st.LongEvery)))
}

// schedSummary is the dashboard's one-line schedule status.
func (a *app) schedSummary(th theme) string {
	s := a.snap
	switch {
	case s.SchedErr != nil:
		return th.errText().Render(s.SchedErr.Error())
	case s.Prof == nil:
		return th.dim().Render("unavailable")
	case !s.Sched.Enabled:
		return th.dim().Render("disabled")
	}
	cur, next := s.Sched.Current, s.Sched.Next
	switch {
	case cur != nil && cur.Entry.Power != nil && !*cur.Entry.Power:
		return fmt.Sprintf("%s  %s", cur.Label(), th.dim().Render("off for the day"))
	case cur != nil && next != nil && next.Starts.Local().YearDay() == cur.Starts.Local().YearDay():
		return fmt.Sprintf("%s → %s", cur.Label(), next.Label())
	case cur != nil:
		return cur.Label()
	case next != nil:
		return th.dim().Render("starts " + next.Label())
	}
	return th.dim().Render("no enabled days")
}
