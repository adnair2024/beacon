package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/state"
)

// Golden output at 80x24 with a fixed clock. Color is off in tests (no TTY),
// so these pin down layout and text; the strip's colors are covered by
// cellsFor tests.

func TestGoldenDashboardWorking(t *testing.T) {
	ta := newTestApp(t)
	ta.hook(claude.EvWorking, "a")
	ta.hook(claude.EvWorking, "b")
	golden(t, "dashboard_working", ta.View())
}

func TestGoldenDashboardIdle(t *testing.T) {
	golden(t, "dashboard_idle", newTestApp(t).View())
}

func TestGoldenDashboardOverrideViewer(t *testing.T) {
	ta := newTestApp(t)
	ta.o.Mode, ta.o.Note = ModeViewer, "daemon (pid 4242)"
	ta.hook(claude.EvWaiting, "a")
	ta.store.Update(func(f *state.File) error {
		f.Override = &state.Override{State: ta.snap.Show, ExpiresAt: t0.Add(15 * 60 * 1e9)}
		return nil
	})
	ta.refresh()
	golden(t, "dashboard_override_viewer", ta.View())
}

func TestGoldenDashboardOffline(t *testing.T) {
	ta := newTestApp(t)
	ta.o.Mode, ta.o.Note, ta.o.Targets = ModeOffline, "no Govee API key", nil
	golden(t, "dashboard_offline", ta.View())
}

func TestGoldenPomoAndSchedule(t *testing.T) {
	ta := newTestApp(t)
	ta.press("2")
	golden(t, "pomo", ta.View())
	ta.press("3")
	golden(t, "schedule", ta.View())
}

func TestGoldenDevices(t *testing.T) {
	ta := newTestApp(t)
	ta.press("4")
	golden(t, "devices", ta.View())
}

func TestGoldenLog(t *testing.T) {
	ta := newTestApp(t)
	ta.press("5")
	lines := []string{
		`time=2026-10-25T10:00:00Z level=INFO msg="tui engine starting" devices="desk (FAKE-STRIP)"`,
		`time=2026-10-25T10:00:01Z level=INFO msg=layer source=claude why="claude working"`,
		`time=2026-10-25T10:00:09Z level=ERROR msg=apply err="cloud: desk: cloud: rate limited, retry in 2s"`,
	}
	ta.Update(logMsg{text: strings.Join(lines, "\n")})
	golden(t, "log", ta.View())
}

func TestGoldenColors(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6")
	golden(t, "colors", ta.View())
	ta.press("e", "j")
	golden(t, "colors_effect_dirty", ta.View())
}

func TestGoldenPickerAndHelp(t *testing.T) {
	ta := newTestApp(t)
	ta.press("o")
	ta.typeText("blorp")
	ta.press("enter")
	golden(t, "picker_error", ta.View())
	ta.press("esc", "?")
	golden(t, "help_full", ta.View())
}

var _ = tea.Quit

// The minimum supported size is where layout bugs hide.
func TestGoldenMinimumSize(t *testing.T) {
	ta := newTestApp(t)
	ta.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	ta.hook(claude.EvWorking, "a")
	golden(t, "min_dashboard", ta.View())
	ta.press("6", "?")
	golden(t, "min_colors_help", ta.View())
}

// Split mode on the dashboard: Claude's segments labelled apart from the rest.
func TestGoldenDashboardSplit(t *testing.T) {
	ta := newTestApp(t)
	ta.cfg.Claude.Mode = "split"
	ta.setPomo(nil, func(p state.Pomo, ps pomo.Settings) state.Pomo { return p })
	ta.hook(claude.EvWorking, "a")
	if ta.snap.Res.Split != 2 || len(ta.snap.Show.Segments) != 10 {
		t.Fatalf("%+v", ta.snap.Res)
	}
	golden(t, "dashboard_split", ta.View())
}
