package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/state"
)

// setPomo writes a timer state built from the fixed test clock.
func (ta *testApp) setPomo(cfgMut func(*config.Config), build func(p state.Pomo, ps pomo.Settings) state.Pomo) {
	ta.t.Helper()
	if cfgMut != nil {
		cfgMut(&ta.cfg)
	}
	ps, err := pomo.SettingsFrom(ta.cfg)
	if err != nil {
		ta.t.Fatal(err)
	}
	p, err := pomo.Start(state.Pomo{}, t0, ps)
	if err != nil {
		ta.t.Fatal(err)
	}
	p = build(p, ps)
	if err := ta.store.Update(func(f *state.File) error { f.Pomo = &p; return nil }); err != nil {
		ta.t.Fatal(err)
	}
	ta.refresh()
}

func (ta *testApp) pomoNow() state.Pomo {
	ta.t.Helper()
	f, err := ta.store.Load()
	if err != nil {
		ta.t.Fatal(err)
	}
	return pomo.Load(f)
}

func TestPomoKeysDriveTheSharedState(t *testing.T) {
	ta := newTestApp(t)
	ta.press("2")
	ta.press(" ") // idle -> start
	if p := ta.pomoNow(); p.Phase != pomo.Focus || p.State != pomo.Running {
		t.Fatalf("%+v", p)
	}
	if !strings.Contains(ta.status, "focus ·") || ta.statusE {
		t.Fatal(ta.status)
	}
	ta.press(" ") // running -> pause
	if p := ta.pomoNow(); p.State != pomo.Paused {
		t.Fatalf("%+v", p)
	}
	ta.press(" ") // paused -> resume
	if p := ta.pomoNow(); p.State != pomo.Running {
		t.Fatalf("%+v", p)
	}
	ta.press("s")
	if p := ta.pomoNow(); p.Phase != pomo.ShortBreak || p.State != pomo.Running || p.CompletedFocus != 0 {
		t.Fatalf("skipping focus starts a break without credit: %+v", p)
	}
	ta.press("x")
	if p := ta.pomoNow(); p.Phase != pomo.Idle {
		t.Fatalf("%+v", p)
	}
	if ta.snap.File.Pomo == nil || ta.snap.File.Pomo.Phase != pomo.Idle {
		t.Fatal("the snapshot should be refreshed after each action")
	}
}

func TestPomoKeysIgnoredOnOtherTabs(t *testing.T) {
	ta := newTestApp(t)
	ta.press(" ", "x") // dashboard
	if f, _ := ta.store.Load(); f.Pomo != nil {
		t.Fatal("space on the dashboard must not start a timer")
	}
	ta.press("6", "s") // Colors: s is save there
	if len(ta.saved) != 1 {
		t.Fatal("s on Colors still saves")
	}
	if f, _ := ta.store.Load(); f.Pomo != nil {
		t.Fatal("s on Colors must not skip a timer")
	}
}

func TestPomoBadConfigShowsErrorInsteadOfActing(t *testing.T) {
	ta := newTestApp(t)
	ta.cfg.Pomo.Focus = "soon"
	ta.press("2")
	if v := ta.View(); !strings.Contains(v, "pomo.focus") {
		t.Fatal(v)
	}
	ta.press(" ")
	if f, _ := ta.store.Load(); f.Pomo != nil {
		t.Fatal("nothing should start with a broken config")
	}
}

func TestCountdownMovesWithTheClockWithoutAReload(t *testing.T) {
	ta := newTestApp(t)
	ta.setPomo(nil, func(p state.Pomo, ps pomo.Settings) state.Pomo { return p })
	ta.press("2")
	before := ta.View()
	ta.Update(tickMsg(t0.Add(11 * time.Minute)))
	after := ta.View()
	if before == after {
		t.Fatal("the countdown must follow the UI clock between state reloads")
	}
	st, _ := ta.pomoStatus()
	if st.Remaining != 25*time.Minute-11*time.Minute {
		t.Fatalf("%v", st.Remaining)
	}
}

func TestBigClockGlyphs(t *testing.T) {
	rows := bigClock("18:42")
	if len(rows) != 5 {
		t.Fatal(len(rows))
	}
	w := len([]rune(rows[0]))
	for i, r := range rows {
		if len([]rune(r)) != w {
			t.Fatalf("row %d is %d wide, row 0 is %d", i, len([]rune(r)), w)
		}
	}
	if w != 4*3+1+4 {
		t.Fatalf("4 digits (3 wide) + colon (1) + 4 gaps = 17, got %d", w)
	}
}

func TestDots(t *testing.T) {
	if dots(2, 4) != "●●○○" || dots(0, 4) != "○○○○" || dots(4, 4) != "●●●●" || dots(1, 0) != "" {
		t.Fatal(dots(2, 4), dots(0, 4), dots(4, 4))
	}
}

// ---- golden views ----

func TestGoldenPomoStates(t *testing.T) {
	cases := []struct {
		name  string
		cfg   func(*config.Config)
		build func(p state.Pomo, ps pomo.Settings) state.Pomo
		at    time.Duration
	}{
		{"pomo_idle", nil, nil, 0},
		{"pomo_running", nil, func(p state.Pomo, ps pomo.Settings) state.Pomo { return p }, 7 * time.Minute},
		{"pomo_paused", nil, func(p state.Pomo, ps pomo.Settings) state.Pomo {
			p, _ = pomo.Pause(p, t0.Add(10*time.Minute), ps)
			return p
		}, 3 * time.Hour},
		{"pomo_short_break", nil, func(p state.Pomo, ps pomo.Settings) state.Pomo {
			return pomo.Advance(p, t0.Add(27*time.Minute), ps)
		}, 27 * time.Minute},
		{"pomo_waiting", func(c *config.Config) { c.Pomo.AutoStartBreaks = false }, func(p state.Pomo, ps pomo.Settings) state.Pomo {
			return pomo.Advance(p, t0.Add(26*time.Minute), ps)
		}, 26 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ta := newTestApp(t)
			if c.build != nil {
				ta.setPomo(c.cfg, c.build)
			}
			ta.press("2")
			ta.now = t0.Add(c.at)
			golden(t, c.name, ta.View())
		})
	}
}

func TestGoldenDashboardWithPomo(t *testing.T) {
	ta := newTestApp(t)
	ta.setPomo(nil, func(p state.Pomo, ps pomo.Settings) state.Pomo {
		p.CompletedFocus = 2
		return p
	})
	ta.now = t0.Add(7 * time.Minute)
	golden(t, "dashboard_pomo", ta.View())
}
