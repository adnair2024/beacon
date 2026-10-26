package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/state"
)

func pomoFile(t *testing.T, mutate func(*config.Config), steps func(state.Pomo, pomo.Settings) state.Pomo) (config.Config, state.File) {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	ps, err := pomo.SettingsFrom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pomo.Start(state.Pomo{}, t0, ps)
	if err != nil {
		t.Fatal(err)
	}
	if steps != nil {
		p = steps(p, ps)
	}
	return cfg, state.File{Pomo: &p}
}

func resolveAt(t *testing.T, cfg config.Config, f state.File, at time.Time) (Result, Inputs) {
	t.Helper()
	in, err := BuildInputs(cfg, f, at, Env{})
	if err != nil {
		t.Fatal(err)
	}
	return Resolve(at, in), in
}

func TestPomoRunningShowsPhaseColorAndWakesAtBoundary(t *testing.T) {
	cfg, f := pomoFile(t, nil, nil)
	at := t0.Add(10 * time.Minute)
	r, in := resolveAt(t, cfg, f, at)
	if r.Source != SourcePomo || r.Layer.State.Color.Hex() != "#FF4FA3" || r.Layer.State.Brightness != 100 {
		t.Fatal(r)
	}
	if !strings.Contains(r.Reason, "focus · 15:00 left") {
		t.Fatal(r.Reason)
	}
	if next, ok := NextChange(at, in); !ok || !next.Equal(t0.Add(25*time.Minute)) {
		t.Fatalf("daemon should wake exactly when the phase ends: %v %v", next, ok)
	}
	// Past the boundary, BuildInputs catches up by itself: now a short break.
	r, _ = resolveAt(t, cfg, f, t0.Add(26*time.Minute))
	if r.Layer.State.Color.Hex() != "#1DE9B6" {
		t.Fatalf("short break is teal: %v", r.Layer.State.Color.Hex())
	}
}

func TestPomoLayerPriority(t *testing.T) {
	cfg, f := pomoFile(t, nil, nil)
	at := t0.Add(time.Minute)
	// Above schedule and idle...
	in, _ := BuildInputs(cfg, f, at, Env{})
	in.Schedule = &Schedule{Layer: sched}
	if r := Resolve(at, in); r.Source != SourcePomo {
		t.Fatal(r)
	}
	// ...below claude when a session is working...
	claude.Record(&f.Claude, claude.EvWorking, claude.Input{SessionID: "a"}, at, time.Hour)
	if r, _ := resolveAt(t, cfg, f, at); r.Source != SourceClaude {
		t.Fatal(r)
	}
	// ...and below a manual override.
	f.Override = &state.Override{}
	if r, _ := resolveAt(t, cfg, f, at); r.Source != SourceOverride {
		t.Fatal(r)
	}
}

func TestPomoPausedIsDimmedWithNoBoundary(t *testing.T) {
	cfg, f := pomoFile(t, nil, func(p state.Pomo, ps pomo.Settings) state.Pomo {
		p, _ = pomo.Pause(p, t0.Add(5*time.Minute), ps)
		return p
	})
	r, in := resolveAt(t, cfg, f, t0.Add(time.Hour))
	if r.Source != SourcePomo || r.Layer.State.Brightness != 20 || r.Layer.State.Color.Hex() != "#FF4FA3" {
		t.Fatal(r)
	}
	if _, ok := NextChange(t0.Add(time.Hour), in); ok {
		t.Fatal("a paused timer has nothing to wake for")
	}
}

func TestPomoBreakColors(t *testing.T) {
	cfg, f := pomoFile(t, func(c *config.Config) { c.Pomo.LongEvery = 1 }, func(p state.Pomo, ps pomo.Settings) state.Pomo {
		return pomo.Advance(p, t0.Add(26*time.Minute), ps)
	})
	if r, _ := resolveAt(t, cfg, f, t0.Add(26*time.Minute)); r.Layer.State.Color.Hex() != "#9C27FF" {
		t.Fatalf("long_every=1 makes every break long (purple): %v", r.Layer.State.Color.Hex())
	}
}

// When a phase ends and waits on the user: three flashes, then a steady dim
// glow in the color of what a keypress will start.
func TestPomoWaitingFlashesThreeTimesThenGlows(t *testing.T) {
	cfg, f := pomoFile(t, func(c *config.Config) { c.Pomo.AutoStartBreaks = false }, nil)
	ended := t0.Add(25 * time.Minute)
	bri := func(at time.Time) (int, string) {
		r, _ := resolveAt(t, cfg, f, at)
		if r.Source != SourcePomo {
			t.Fatalf("waiting still counts as an active pomodoro: %v", r)
		}
		return r.Layer.State.Brightness, r.Layer.State.Color.Hex()
	}
	step := FlashStep
	want := []int{100, flashDim, 100, flashDim, 100, flashDim} // 3 on/off pairs
	for i, w := range want {
		at := ended.Add(time.Duration(i)*step + step/2)
		if got, hex := bri(at); got != w || hex != "#1DE9B6" {
			t.Fatalf("step %d: brightness %d color %s, want %d teal (the break it offers)", i, got, hex, w)
		}
	}
	if got, _ := bri(ended.Add(6*step + time.Millisecond)); got != 20 {
		t.Fatalf("after the flashes the light settles at 20%%, got %d", got)
	}
	if got, _ := bri(ended.Add(time.Hour)); got != 20 {
		t.Fatalf("and stays there: %d", got)
	}
	r, in := resolveAt(t, cfg, f, ended.Add(step/2))
	if !strings.Contains(r.Reason, "flashing") {
		t.Fatal(r.Reason)
	}
	// The daemon must wake at every flash step, then stop waking.
	for i := 0; i < 6; i++ {
		at := ended.Add(time.Duration(i)*step + step/2)
		next, ok := NextChange(at, in)
		if !ok || !next.Equal(ended.Add(time.Duration(i+1)*step)) {
			t.Fatalf("step %d: next wake %v, want %v", i, next, ended.Add(time.Duration(i+1)*step))
		}
	}
	if _, ok := NextChange(ended.Add(7*step), in); ok {
		t.Fatal("no more wakeups after the flash")
	}
}

func TestPomoFlashCanBeDisabled(t *testing.T) {
	cfg, f := pomoFile(t, func(c *config.Config) { c.Pomo.AutoStartBreaks = false; c.Pomo.FlashOnPhaseEnd = false }, nil)
	ended := t0.Add(25 * time.Minute)
	r, in := resolveAt(t, cfg, f, ended.Add(FlashStep+time.Millisecond))
	if r.Layer.State.Brightness != 20 || strings.Contains(r.Reason, "flashing") {
		t.Fatal(r)
	}
	if _, ok := NextChange(ended, in); ok {
		t.Fatal("no flash, nothing to wake for")
	}
}

func TestPomoBadConfigIsReported(t *testing.T) {
	cfg, f := pomoFile(t, nil, nil)
	cfg.Pomo.Focus = "soon"
	if _, err := BuildInputs(cfg, f, t0, Env{}); err == nil || !strings.Contains(err.Error(), "pomo.focus") {
		t.Fatal(err)
	}
	cfg, f = pomoFile(t, nil, nil)
	cfg.Pomo.FocusColor = "nope"
	if _, err := BuildInputs(cfg, f, t0, Env{}); err == nil || !strings.Contains(err.Error(), "focus_color") {
		t.Fatal(err)
	}
}

func TestIdlePomoContributesNothing(t *testing.T) {
	cfg := config.Default()
	f := state.File{Pomo: &state.Pomo{Phase: pomo.Idle}}
	if r, _ := resolveAt(t, cfg, f, t0); r.Source != SourceNone {
		t.Fatal(r)
	}
}
