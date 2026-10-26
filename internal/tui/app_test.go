package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/engine"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/state"
)

func TestTabNavigation(t *testing.T) {
	ta := newTestApp(t)
	ta.press("4")
	if ta.tab != tabDevices {
		t.Fatal(ta.tab)
	}
	ta.press("tab")
	ta.press("tab")
	if ta.tab != tabColors {
		t.Fatal(ta.tab)
	}
	ta.press("tab") // wraps
	if ta.tab != tabDashboard {
		t.Fatal(ta.tab)
	}
	ta.press("shift+tab")
	if ta.tab != tabColors {
		t.Fatal(ta.tab)
	}
	ta.press("?")
	if !ta.helpOn {
		t.Fatal("? should toggle full help")
	}
}

func TestQuit(t *testing.T) {
	ta := newTestApp(t)
	if _, cmd := ta.Update(kmsg("q")); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("q should quit")
	}
	if _, cmd := ta.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c should quit")
	}
}

func TestOverlaysSwallowGlobalKeys(t *testing.T) {
	ta := newTestApp(t)
	ta.press("o")
	if !ta.picker.open {
		t.Fatal("o opens the picker")
	}
	ta.typeText("q1?") // text here, not commands
	if !ta.picker.open || ta.tab != tabDashboard || ta.helpOn {
		t.Fatal("typing in the picker must not trigger global keys")
	}
	if _, cmd := ta.Update(kmsg("q")); cmd != nil && cmd() == tea.Quit() {
		t.Fatal("q must not quit while typing")
	}
	ta.press("esc")
	if ta.picker.open {
		t.Fatal("esc closes the picker")
	}
	// Same inside the Colors editor.
	ta.press("6", "enter")
	if !ta.colors.editing {
		t.Fatal("enter edits")
	}
	ta.press("q")
	if ta.tab != tabColors || !ta.colors.editing {
		t.Fatal("q while editing a color must not quit or switch tabs")
	}
}

func TestOverrideFlowWritesStateFile(t *testing.T) {
	ta := newTestApp(t)
	ta.press("o")
	ta.typeText("orange")
	ta.press("enter")
	if ta.picker.open {
		t.Fatalf("picker should close on success: %q", ta.picker.err)
	}
	f, _ := ta.store.Load()
	if f.Override == nil || f.Override.State.Color == nil || f.Override.State.Color.Hex() != "#FF8C00" || f.Override.State.Brightness != 100 {
		t.Fatalf("%+v", f.Override)
	}
	if d := time.Until(f.Override.ExpiresAt); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("default override duration is 15m, got %v", d)
	}
	if !strings.HasPrefix(ta.status, "override set until ") || ta.statusE {
		t.Fatal(ta.status)
	}
	if ta.snap.Res.Source != engine.SourceOverride {
		t.Fatalf("the snapshot should pick the override up: %v", ta.snap.Res)
	}
}

func TestOverrideAcceptsKelvinAndOffAndBrightness(t *testing.T) {
	ta := newTestApp(t)
	for _, c := range []struct {
		text string
		bri  string
		chk  func(state.Override) bool
	}{
		{"3000k", "40", func(o state.Override) bool { return o.State.Kelvin == 3000 && o.State.Brightness == 40 }},
		{"off", "100", func(o state.Override) bool { return !o.State.On }},
	} {
		ta.press("o")
		ta.typeText(c.text)
		ta.picker.bri.SetValue(c.bri)
		ta.press("enter")
		f, _ := ta.store.Load()
		if f.Override == nil || !c.chk(*f.Override) {
			t.Fatalf("%s: %+v", c.text, f.Override)
		}
	}
}

func TestOverrideRejectsBadInput(t *testing.T) {
	ta := newTestApp(t)
	ta.press("o")
	ta.typeText("blorp")
	ta.press("enter")
	if !ta.picker.open || ta.picker.err == "" {
		t.Fatal("bad color should keep the picker open with an error")
	}
	ta.picker.color.SetValue("red")
	ta.picker.bri.SetValue("500")
	ta.press("enter")
	if !ta.picker.open || !strings.Contains(ta.picker.err, "1-100") {
		t.Fatal(ta.picker.err)
	}
	if f, _ := ta.store.Load(); f.Override != nil {
		t.Fatal("nothing should be written for invalid input")
	}
}

func TestOverrideInOfflineModeSaysNothingWillApplyIt(t *testing.T) {
	ta := newTestApp(t)
	ta.o.Mode = ModeOffline
	ta.press("o")
	ta.typeText("red")
	ta.press("enter")
	if !strings.Contains(ta.status, "no engine is running") {
		t.Fatal(ta.status)
	}
}

func TestClearOverride(t *testing.T) {
	ta := newTestApp(t)
	ta.store.Update(func(f *state.File) error { f.Override = &state.Override{}; return nil })
	ta.press("c")
	if f, _ := ta.store.Load(); f.Override != nil {
		t.Fatal("c should clear the override")
	}
	if ta.status != "override cleared" {
		t.Fatal(ta.status)
	}
}

func TestEngineMessages(t *testing.T) {
	ta := newTestApp(t)
	ta.Update(errMsg{errors.New("cloud: rate limited, retry in 2s")})
	if !ta.statusE || !strings.Contains(ta.status, "rate limited") {
		t.Fatal(ta.status)
	}
	ta.Update(engineDoneMsg{state.ErrEngineRunning})
	if ta.o.Mode != ModeViewer {
		t.Fatal("losing the engine race should downgrade to viewer")
	}
	ta.Update(engineDoneMsg{errors.New("boom")})
	if ta.o.Mode != ModeOffline || !ta.statusE {
		t.Fatal(ta.o.Mode, ta.status)
	}
	ta.o.Mode = ModeEngine
	ta.Update(engineDoneMsg{nil}) // clean shutdown changes nothing
	if ta.o.Mode != ModeEngine {
		t.Fatal("clean engine exit is not an error")
	}
}

func TestSnapshotReflectsStateLikeTheEngine(t *testing.T) {
	ta := newTestApp(t)
	ta.hook(claude.EvWorking, "a")
	ta.hook(claude.EvWaiting, "b")
	if s := ta.snap; s.Res.Source != engine.SourceClaude || s.Claude.Status != "waiting" || s.Claude.Live != 2 ||
		s.Show.Color.Hex() != "#2979FF" {
		t.Fatalf("%+v %+v", s.Res, s.Claude)
	}
	ta.store.Update(func(f *state.File) error {
		c := *ta.snap.Show.Color
		c.R = 1
		f.Override = &state.Override{State: ta.snap.Show}
		return nil
	})
	ta.refresh()
	if ta.snap.Res.Source != engine.SourceOverride {
		t.Fatal("override outranks claude")
	}
}

func TestSnapshotLeavesLightAloneUsesLastApplied(t *testing.T) {
	ta := newTestApp(t)
	st := config.Default() // just to have a value; any applied state works
	_ = st
	ta.hook(claude.EvWorking, "a")
	applied := ta.snap.Show
	ta.store.Update(func(f *state.File) error {
		claude.Record(&f.Claude, claude.SessionEnd, claude.Input{SessionID: "a"}, t0, time.Hour)
		f.LastApplied = &applied
		return nil
	})
	ta.refresh()
	if ta.snap.Res.Source != engine.SourceNone || !ta.snap.Placed || ta.snap.Show.Color == nil {
		t.Fatalf("with no active layer the dashboard should show the last applied state: %+v", ta.snap)
	}
}

func TestSnapshotReportsBrokenConfigAsError(t *testing.T) {
	ta := newTestApp(t)
	ta.cfg.Claude.Working = "not-a-color"
	ta.refresh()
	if ta.snap.Err == nil || !strings.Contains(ta.snap.Err.Error(), "claude.working") {
		t.Fatal(ta.snap.Err)
	}
	if !strings.Contains(ta.View(), "claude.working") {
		t.Fatal("the dashboard should show the config error")
	}
}

func TestTooSmallTerminal(t *testing.T) {
	ta := newTestApp(t)
	ta.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if v := ta.View(); !strings.Contains(v, "terminal too small") || strings.Contains(v, "beacon") {
		t.Fatal(v)
	}
}

func TestHeaderShowsModeAndDevice(t *testing.T) {
	ta := newTestApp(t)
	v := ta.View()
	if !strings.Contains(v, "beacon · engine") || !strings.Contains(v, "desk") || !strings.Contains(v, "FAKE-STRIP") {
		t.Fatal(v)
	}
	ta.o.Mode, ta.o.Dry = ModeViewer, true
	if v := ta.View(); !strings.Contains(v, "viewer, dry-run") {
		t.Fatal(v)
	}
}

func TestLogTab(t *testing.T) {
	ta := newTestApp(t)
	ta.press("5")
	ta.Update(logMsg{text: "line one\nline two"})
	if v := ta.View(); !strings.Contains(v, "line two") {
		t.Fatal(v)
	}
	ta.Update(logMsg{err: errors.New("permission denied")})
	if v := ta.View(); !strings.Contains(v, "permission denied") {
		t.Fatal(v)
	}
}

// ---- Colors tab (carried over from the original screen) ----

func TestColorsEditAndSave(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6", "enter")
	ta.colors.input.SetValue("#112233")
	ta.press("enter")
	if ta.colors.editing || ta.colors.cfg.Claude.Working != "#112233" || !ta.colors.dirty {
		t.Fatal(ta.colors.editing, ta.colors.cfg.Claude.Working, ta.colors.dirty)
	}
	if ta.cfg.Claude.Working == "#112233" {
		t.Fatal("unsaved edits must not leak into the running config")
	}
	ta.press("s")
	if len(ta.saved) != 1 || ta.saved[0].Claude.Working != "#112233" || ta.colors.dirty {
		t.Fatal(ta.saved, ta.colors.dirty)
	}
	if ta.cfg.Claude.Working != "#112233" {
		t.Fatal("after saving, the app adopts the new config")
	}
}

func TestColorsBadColorStaysInEditor(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6", "enter")
	ta.colors.input.SetValue("nonsense")
	ta.press("enter")
	if !ta.colors.editing || !ta.colors.statusE || ta.colors.cfg.Claude.Working != "#FF8C00" {
		t.Fatal(ta.colors.editing, ta.colors.statusE, ta.colors.cfg.Claude.Working)
	}
}

func TestColorsEscCancels(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6", "enter")
	ta.colors.input.SetValue("#000000")
	ta.press("esc")
	if ta.colors.editing || ta.colors.cfg.Claude.Working != "#FF8C00" || ta.colors.dirty {
		t.Fatal("esc should discard")
	}
}

func TestColorsPaletteTabCycles(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6", "enter", "tab")
	if ta.colors.input.Value() != "amber" { // first sorted built-in name
		t.Fatal(ta.colors.input.Value())
	}
	ta.cfg.Palette = map[string]string{"aaa": "#010101"}
	ta.colors.cfg.Palette = ta.cfg.Palette
	ta.colors.palIdx = -1
	ta.press("tab")
	if ta.colors.input.Value() != "aaa" {
		t.Fatalf("user palette names should join the cycle: %q", ta.colors.input.Value())
	}
}

func TestColorsCycleEffect(t *testing.T) {
	ta := newTestApp(t)
	ta.press("6", "e")
	if ta.colors.cfg.Effects.ClaudeWorking != "pulse" || !ta.colors.dirty {
		t.Fatal(ta.colors.cfg.Effects.ClaudeWorking)
	}
	ta.press("j", "e")
	if ta.colors.cfg.Effects.ClaudeReady != "pulse" {
		t.Fatal(ta.colors.cfg.Effects.ClaudeReady)
	}
}

func TestColorsSaveErrorSurfaced(t *testing.T) {
	ta := newTestApp(t)
	ta.o.Save = func(string, config.Config) error { return errors.New("disk full") }
	ta.colors.save = ta.o.Save
	ta.press("6", "s")
	if !ta.colors.statusE || ta.colors.status != "disk full" {
		t.Fatal(ta.colors.status)
	}
	if ta.cfg.Claude.Working != config.Default().Claude.Working {
		t.Fatal("a failed save must not change the running config")
	}
}

func TestColorsSaveRefusesInvalidConfig(t *testing.T) {
	ta := newTestApp(t)
	ta.colors.cfg.Claude.Mode = "bogus"
	ta.press("6", "s")
	if !ta.colors.statusE || len(ta.saved) != 0 {
		t.Fatal(ta.colors.status, ta.saved)
	}
}

// The frame must never outgrow the terminal, whatever is open.
func TestFrameFitsTheTerminalOnEveryScreen(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 18}, {100, 40}} {
		ta := newTestApp(t)
		ta.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		ta.hook(claude.EvWorking, "a")
		ta.Update(logMsg{text: strings.Repeat("a long log line that will wrap around the viewport width\n", 80)})
		check := func(what string) {
			t.Helper()
			lines := strings.Split(ta.View(), "\n")
			if len(lines) > size[1] {
				t.Errorf("%dx%d %s: %d lines", size[0], size[1], what, len(lines))
			}
			for _, l := range lines {
				if w := len([]rune(l)); w > size[0] {
					t.Errorf("%dx%d %s: line wider than the terminal (%d): %q", size[0], size[1], what, w, l)
					break
				}
			}
		}
		for i := 1; i <= int(numTabs); i++ {
			ta.press(string(rune('0' + i)))
			check(tabNames[i-1])
			ta.press("?")
			check(tabNames[i-1] + " + help")
			ta.press("?")
		}
		ta.press("1", "o")
		check("picker")
	}
}
