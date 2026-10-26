package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adnair2024/beacon/internal/config"
)

func (m *colorsModel) find(section, label string) int {
	for i, r := range m.rows {
		if r.section == section && r.label == label {
			return i
		}
	}
	return -1
}

func (ta *testApp) selectRow(section, label string) *row {
	ta.t.Helper()
	i := ta.colors.find(section, label)
	if i < 0 {
		ta.t.Fatalf("no %s/%s row", section, label)
	}
	ta.colors.cursor = i
	return &ta.colors.rows[i]
}

// Every setting that changes how the light looks must be reachable here.
func TestEveryLookSettingHasARow(t *testing.T) {
	ta := newTestApp(t)
	ta.colors.cfg.Palette = map[string]string{"amber2": "#FFB300", "sky": "#00B0FF"}
	ta.colors.bind()
	c := ta.colors.cfg
	want := map[string]string{
		"Claude/working": c.Claude.Working, "Claude/ready": c.Claude.Ready, "Claude/waiting": c.Claude.Waiting,
		"Claude/mode": c.Claude.Mode, "Claude/split segments": "2", "Claude/brightness": "100",
		"Pomodoro/focus": c.Pomo.FocusColor, "Pomodoro/short break": c.Pomo.ShortBreakColor, "Pomodoro/long break": c.Pomo.LongBreakColor,
		"Pomodoro/progress": "bar", "Pomodoro/direction": "drain", "Pomodoro/unfilled dim %": "20",
		"Idle/action": "leave", "Idle/color": c.Idle.Color,
		"Palette/amber2": "#FFB300", "Palette/sky": "#00B0FF",
	}
	got := map[string]string{}
	for _, r := range ta.colors.rows {
		got[r.section+"/"+r.label] = r.get()
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s: row shows %q (present %v), config has %q", k, g, ok, v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected extra rows: %v", got)
	}
	// All six roles with an effect can have it cycled.
	withEffect := 0
	for _, r := range ta.colors.rows {
		if r.effect != nil {
			withEffect++
		}
	}
	if withEffect != 6 {
		t.Fatalf("6 roles have effects, rows say %d", withEffect)
	}
}

func TestChoiceRowsCycleBothWaysAndSave(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6")
	ta.selectRow("Claude", "mode")
	ta.press("enter")
	if ta.colors.cfg.Claude.Mode != "accent" || !ta.colors.dirty || ta.colors.mode != cmBrowse {
		t.Fatalf("enter cycles a choice in place: %q", ta.colors.cfg.Claude.Mode)
	}
	ta.press("e")
	ta.press("e")
	if ta.colors.cfg.Claude.Mode != "off" {
		t.Fatal(ta.colors.cfg.Claude.Mode)
	}
	ta.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ta.colors.cfg.Claude.Mode != "split" {
		t.Fatalf("left goes back: %q", ta.colors.cfg.Claude.Mode)
	}
	ta.selectRow("Pomodoro", "direction")
	ta.press("enter")
	ta.selectRow("Idle", "action")
	ta.press("enter", "enter") // leave -> off -> color
	// "color" with no idle color to show is refused when saving, and says why.
	ta.press("s")
	if len(ta.saved) != 0 || !ta.colors.statusE || !strings.Contains(ta.colors.status, "idle.color") {
		t.Fatalf("saved=%d status=%q", len(ta.saved), ta.colors.status)
	}
	ta.selectRow("Idle", "color")
	ta.press("enter")
	ta.colors.input.SetValue("teal")
	ta.press("enter")
	ta.press("s")
	if len(ta.saved) != 1 {
		t.Fatalf("saved nothing: %q", ta.colors.status)
	}
	s := ta.saved[0]
	if s.Claude.Mode != "split" || s.Pomo.ProgressDirection != "fill" || s.Idle.Action != "color" || s.Idle.Color != "teal" {
		t.Fatalf("%+v %+v %+v %+v", s.Claude.Mode, s.Pomo.ProgressDirection, s.Idle.Action, s.Idle.Color)
	}
}

func TestNumberRowsAreRangeChecked(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6")
	ta.selectRow("Pomodoro", "unfilled dim %")
	ta.press("enter")
	if !ta.colors.editing || ta.colors.input.Value() != "20" {
		t.Fatal("number rows open an editor with the current value")
	}
	for _, bad := range []string{"150", "-1", "abc", ""} {
		ta.colors.input.SetValue(bad)
		ta.press("enter")
		if !ta.colors.editing || !ta.colors.statusE || ta.colors.cfg.Pomo.ProgressDim != 20 {
			t.Errorf("%q should be refused and leave the setting alone", bad)
		}
	}
	ta.colors.input.SetValue("35")
	ta.press("enter")
	if ta.colors.cfg.Pomo.ProgressDim != 35 || ta.colors.editing {
		t.Fatal(ta.colors.cfg.Pomo.ProgressDim)
	}
	ta.selectRow("Claude", "brightness")
	ta.press("enter")
	ta.colors.input.SetValue("0") // 1-100 for Claude's brightness
	ta.press("enter")
	if ta.colors.cfg.Claude.Brightness != 100 || !ta.colors.editing {
		t.Fatal("Claude brightness can't be 0")
	}
	ta.press("esc")
	ta.selectRow("Claude", "split segments")
	ta.press("enter")
	ta.colors.input.SetValue("3")
	ta.press("enter")
	if ta.colors.cfg.Claude.SplitSegments != 3 {
		t.Fatal(ta.colors.cfg.Claude.SplitSegments)
	}
}

func TestIdleColorCanBeSetAndCleared(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6")
	ta.selectRow("Idle", "color")
	ta.press("enter")
	ta.colors.input.SetValue("teal")
	ta.press("enter")
	if ta.colors.cfg.Idle.Color != "teal" {
		t.Fatal(ta.colors.cfg.Idle.Color)
	}
	ta.press("enter")
	ta.colors.input.SetValue("")
	ta.press("enter")
	if ta.colors.cfg.Idle.Color != "" || ta.colors.editing {
		t.Fatal("clearing the idle color is allowed")
	}
	// An empty value for a real role is not.
	ta.selectRow("Claude", "working")
	ta.press("enter")
	ta.colors.input.SetValue("")
	ta.press("enter")
	if !ta.colors.editing || ta.colors.cfg.Claude.Working == "" {
		t.Fatal("a role needs a color")
	}
}

func TestAddingAPaletteColorEndToEnd(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6", "a")
	if ta.colors.mode != cmName {
		t.Fatal("a asks for a name")
	}
	// Bad names are refused with a reason and the prompt stays open.
	for name, want := range map[string]string{"Has Space": "lowercase", "": "lowercase", "-x": "lowercase"} {
		ta.colors.input.SetValue(name)
		ta.press("enter")
		if ta.colors.mode != cmName || !strings.Contains(ta.colors.status, want) {
			t.Errorf("%q: mode %d status %q", name, ta.colors.mode, ta.colors.status)
		}
	}
	ta.colors.input.SetValue("  Sunset ") // trimmed and lowercased
	ta.press("enter")
	if ta.colors.mode != cmEdit || ta.colors.cur().palette != "sunset" || ta.colors.input.Value() != "#FFFFFF" {
		t.Fatalf("after the name, its value opens for editing: mode %d row %+v", ta.colors.mode, ta.colors.cur().label)
	}
	ta.colors.input.SetValue("#FF5E3A")
	ta.press("enter")
	if ta.colors.cfg.Palette["sunset"] != "#FF5E3A" || ta.colors.mode != cmBrowse || !ta.colors.dirty {
		t.Fatalf("%v", ta.colors.cfg.Palette)
	}
	// It can now be used by name, immediately, in a role.
	ta.selectRow("Claude", "working")
	ta.press("enter")
	ta.colors.input.SetValue("sunset")
	ta.press("enter")
	if ta.colors.cfg.Claude.Working != "sunset" || ta.colors.editing {
		t.Fatal("a palette name is a valid color")
	}
	ta.press("s")
	if len(ta.saved) != 1 || ta.saved[0].Palette["sunset"] != "#FF5E3A" || ta.saved[0].Claude.Working != "sunset" {
		t.Fatalf("%+v", ta.saved)
	}
	// Duplicates are refused.
	ta.press("a")
	ta.colors.input.SetValue("sunset")
	ta.press("enter")
	if ta.colors.mode != cmName || !strings.Contains(ta.colors.status, "already exists") {
		t.Fatal(ta.colors.status)
	}
}

func TestDeletingPaletteColors(t *testing.T) {
	ta := newTestApp(t)
	ta.colors.cfg.Palette = map[string]string{"mine": "#010203", "used": "#040506"}
	ta.colors.cfg.Claude.Ready = "used"
	ta.colors.bind()
	ta.press("6")

	ta.selectRow("Claude", "working")
	ta.press("d")
	if ta.colors.mode != cmBrowse || !ta.colors.statusE {
		t.Fatal("only palette colors can be deleted")
	}

	ta.selectRow("Palette", "mine")
	ta.press("d", "n")
	if _, ok := ta.colors.cfg.Palette["mine"]; !ok {
		t.Fatal("n cancels")
	}
	ta.press("d", "y")
	if _, ok := ta.colors.cfg.Palette["mine"]; ok || ta.colors.find("Palette", "mine") >= 0 {
		t.Fatal("y deletes it, and its row")
	}

	// A color a role still uses can be deleted from the list, but saving is
	// refused (the role would point at nothing) with a message naming it.
	ta.selectRow("Palette", "used")
	ta.press("d", "y")
	ta.press("s")
	if len(ta.saved) != 0 || !ta.colors.statusE || !strings.Contains(ta.colors.status, "claude.ready") {
		t.Fatalf("saved=%d status=%q", len(ta.saved), ta.colors.status)
	}
}

func TestEmptyPaletteSaysHowToAddOne(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6")
	if !strings.Contains(ta.View(), "press a to add") && !strings.Contains(ta.colors.view(ta.theme(), ta.now, 40), "press a to add") {
		t.Fatal("an empty palette should explain how to add a color")
	}
}

// The list is longer than the body, so it scrolls: the selected row must
// always be visible and the whole frame must still fit.
func TestColorsListScrollsAndKeepsTheCursorVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 18}} {
		ta := newTestApp(t)
		ta.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		ta.colors.cfg.Palette = map[string]string{"one": "#111111", "two": "#222222", "three": "#333333"}
		ta.colors.bind()
		ta.press("6")
		for i := range ta.colors.rows {
			ta.colors.cursor = i
			v := ta.View()
			label := ta.colors.rows[i].label
			if !strings.Contains(v, "▸ ") || !strings.Contains(v, label) {
				t.Fatalf("%dx%d row %d (%s) is not visible:\n%s", size[0], size[1], i, label, v)
			}
			if n := len(strings.Split(v, "\n")); n > size[1] {
				t.Fatalf("%dx%d row %d: frame is %d rows", size[0], size[1], i, n)
			}
		}
	}
	ta := newTestApp(t)
	ta.press("6")
	if v := ta.View(); !strings.Contains(v, "↓ more") {
		t.Fatalf("a list longer than the window says there's more:\n%s", v)
	}
}

func TestRowsPointAtTheWorkingCopyNotTheRunningConfig(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6")
	ta.selectRow("Pomodoro", "progress")
	ta.press("enter")
	if ta.colors.cfg.Pomo.Progress != "solid" || ta.cfg.Pomo.Progress != "bar" {
		t.Fatal("edits stay in the working copy until saved")
	}
	ta.press("s")
	if ta.cfg.Pomo.Progress != "solid" {
		t.Fatal("after a successful save the app adopts them")
	}
}

var _ = fmt.Sprint
var _ config.Config
