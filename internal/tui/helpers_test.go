package tui

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee/fake"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	time.Local = time.UTC // golden output formats clock times
	os.Exit(m.Run())
}

type testApp struct {
	*app
	t        *testing.T
	store    *state.Store
	saved    []config.Config
	schedDir string
}

// newTestApp builds an app at a fixed clock, 80x24, with one fake strip and
// a snapshot already loaded.
func newTestApp(t *testing.T) *testApp {
	t.Helper()
	ta := &testApp{t: t, store: state.Open(t.TempDir()), schedDir: filepath.Join(t.TempDir(), "schedules")}
	cfg := config.Default()
	a := newApp(Options{
		Cfg: cfg, ConfigPath: "/cfg/config.toml", Store: ta.store, SchedDir: ta.schedDir, Mode: ModeEngine, Dark: true,
		Targets: fake.Devices()[:1], LogPath: filepath.Join(t.TempDir(), "beacon.log"),
		Save: func(p string, c config.Config) error { ta.saved = append(ta.saved, c); return nil },
		Now:  func() time.Time { return t0 },
	})
	ta.app = a
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ta.refresh()
	return ta
}

func (ta *testApp) refresh() {
	ta.snap = takeSnapshot(ta.cfg, ta.store, ta.schedDir, ta.caps(), t0.Add(time.Second))
	ta.now = t0.Add(time.Second)
}

func (ta *testApp) hook(ev claude.Event, sid string) {
	ta.t.Helper()
	err := ta.store.Update(func(f *state.File) error {
		claude.Record(&f.Claude, ev, claude.Input{SessionID: sid}, t0, 30*time.Minute)
		return nil
	})
	if err != nil {
		ta.t.Fatal(err)
	}
	ta.refresh()
}

func kmsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// press sends keys and runs any command they return (with a short timeout,
// so blink timers and the like can't hang the test), feeding results back.
func (ta *testApp) press(keys ...string) {
	ta.t.Helper()
	for _, k := range keys {
		_, cmd := ta.Update(kmsg(k))
		ta.run(cmd)
	}
}

func (ta *testApp) typeText(s string) {
	for _, r := range s {
		ta.press(string(r))
	}
}

func (ta *testApp) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				ta.run(c)
			}
			return
		}
		switch msg.(type) {
		case tickMsg:
			return
		}
		_, next := ta.Update(msg)
		ta.run(next)
	case <-time.After(150 * time.Millisecond):
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden file:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
