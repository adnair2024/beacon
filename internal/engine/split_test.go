package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

var (
	orange = color.RGB{R: 255, G: 140}
	pink   = color.RGB{R: 255, G: 79, B: 163}
)

func splitCfg(mut func(*config.Config)) config.Config {
	c := config.Default()
	c.Claude.Mode = "split"
	if mut != nil {
		mut(&c)
	}
	return c
}

// resolveSplit builds inputs the real way (config + state + caps) and resolves.
func resolveSplit(t *testing.T, cfg config.Config, f state.File, sched *schedule.Profile, caps govee.Capabilities, at time.Time) (Result, Inputs) {
	t.Helper()
	in, err := BuildInputs(cfg, f, at, Env{Schedule: sched, Caps: caps})
	if err != nil {
		t.Fatal(err)
	}
	return Resolve(at, in), in
}

func withClaude(f state.File, ev claude.Event, at time.Time) state.File {
	claude.Record(&f.Claude, ev, claude.Input{SessionID: "s"}, at, time.Hour)
	return f
}

func segAt(st govee.State, i int) (govee.Segment, bool) {
	for _, s := range st.Segments {
		if s.Index == i {
			return s, true
		}
	}
	return govee.Segment{}, false
}

func TestSplitPaintsClaudeSegmentsAndLeavesTheRestAlone(t *testing.T) {
	f := withClaude(state.File{}, claude.EvWorking, t0)
	r, _ := resolveSplit(t, splitCfg(nil), f, nil, stripCaps(10), t0.Add(time.Second))
	st := r.Layer.State
	if r.Source != SourceClaude || r.Split != 2 || !st.On || st.Color != nil || st.Kelvin != 0 {
		t.Fatalf("with nothing below, only Claude's segments are set: %+v %+v", r, st)
	}
	if len(st.Segments) != 2 {
		t.Fatalf("%+v", st.Segments)
	}
	for i, s := range st.Segments {
		if s.Index != i || s.Color != orange || s.Brightness != 100 {
			t.Fatalf("segment %d: %+v", i, s)
		}
	}
	if !strings.HasSuffix(r.Reason, "(split)") {
		t.Fatal(r.Reason)
	}
}

func TestSplitOverThePomodoroBarKeepsTheBarOffClaudesSegments(t *testing.T) {
	cfg, pf := pomoFile(t, func(c *config.Config) { c.Claude.Mode = "split" }, nil)
	at := t0.Add(5 * 150 * time.Second) // half of the focus block
	f := withClaude(pf, claude.EvWorking, at)
	r, _ := resolveSplit(t, cfg, f, nil, stripCaps(10), at)
	st := r.Layer.State
	if r.Source != SourceClaude || st.Color == nil || *st.Color != pink || st.Brightness != 100 {
		t.Fatalf("the pomodoro's whole-strip color goes first: %+v", st)
	}
	if len(st.Segments) != 10 {
		t.Fatalf("2 Claude segments + an 8-segment bar = 10, got %d", len(st.Segments))
	}
	for i, s := range st.Segments {
		if s.Index != i {
			t.Fatalf("segments must be unique and ascending: %+v", st.Segments)
		}
	}
	for i := 0; i < 2; i++ {
		if st.Segments[i].Color != orange || st.Segments[i].Brightness != 100 {
			t.Fatalf("Claude's segment %d: %+v", i, st.Segments[i])
		}
	}
	// Half drained over 8 segments: 4 lit (indices 2-5), 4 dim (6-9).
	want := []int{100, 100, 100, 100, 20, 20, 20, 20}
	for i, w := range want {
		if got := st.Segments[2+i]; got.Brightness != w || got.Color != pink {
			t.Fatalf("bar segment %d: %+v want brightness %d", 2+i, got, w)
		}
	}
	if !strings.Contains(r.BelowReason, "pomodoro") {
		t.Fatal(r.BelowReason)
	}
}

func TestBarReservesClaudesSegmentsEvenWithNoSession(t *testing.T) {
	cfg, f := pomoFile(t, func(c *config.Config) { c.Claude.Mode = "split" }, nil)
	at := t0.Add(10 * time.Second)
	r, in := resolveSplit(t, cfg, f, nil, stripCaps(10), at)
	segs := r.Layer.State.Segments
	if r.Source != SourcePomo || len(segs) != 10 {
		t.Fatalf("%+v", segs)
	}
	for i := 0; i < 2; i++ { // the reserved segments are painted in the phase color
		if segs[i].Index != i || segs[i].Color != pink || segs[i].Brightness != 100 {
			t.Fatalf("reserved segment %d: %+v", i, segs[i])
		}
	}
	// The bar itself starts after them: 8 segments, all lit at the very start.
	if segs[2].Index != 2 || segs[2].Brightness != 100 || segs[9].Index != 9 || segs[9].Brightness != 100 {
		t.Fatalf("the bar uses segments 2-9 so it doesn't reflow when a session starts: %+v", segs)
	}
	// 8 segments over 25 minutes: the first boundary is 187.5 s in.
	if next, ok := NextChange(at, in); !ok || !next.Equal(t0.Add(187500*time.Millisecond)) {
		t.Fatalf("%v %v", next, ok)
	}
	// Not in split mode, the bar gets all ten.
	cfg.Claude.Mode = "override"
	r, _ = resolveSplit(t, cfg, f, nil, stripCaps(10), at)
	if len(r.Layer.State.Segments) != 10 || r.Layer.State.Segments[0].Index != 0 {
		t.Fatalf("%+v", r.Layer.State.Segments)
	}
}

func TestSplitOverAScheduleBlock(t *testing.T) {
	at := monday(10, 0) // Focus: 4800 K at 90%
	f := withClaude(state.File{}, claude.EvWaiting, at)
	p := schedule.Default()
	r, _ := resolveSplit(t, splitCfg(nil), f, &p, stripCaps(10), at.Add(time.Second))
	st := r.Layer.State
	if st.Kelvin != 4800 || st.Brightness != 90 || len(st.Segments) != 2 {
		t.Fatalf("the schedule's whole-strip look stays, Claude is painted on top: %+v", st)
	}
	if st.Segments[0].Color != (color.RGB{R: 41, G: 121, B: 255}) { // waiting blue
		t.Fatalf("%+v", st.Segments[0])
	}
}

func TestSplitWhenTheLayerBelowWantsTheLightOff(t *testing.T) {
	at := monday(17, 0) // after Leave: the schedule says off
	f := withClaude(state.File{}, claude.EvWorking, at)
	p := schedule.Default()
	r, _ := resolveSplit(t, splitCfg(nil), f, &p, stripCaps(10), at)
	st := r.Layer.State
	if !st.On || len(st.Segments) != 10 {
		t.Fatalf("Claude needs the strip on, and the rest stays dark: %+v", st)
	}
	for _, s := range st.Segments[2:] {
		if s.Color != (color.RGB{}) {
			t.Fatalf("segment %d should be dark: %+v", s.Index, s)
		}
	}
	if st.Segments[0].Color != orange {
		t.Fatal("and Claude's own segments are lit")
	}
}

func TestSplitReadyHoldThenYields(t *testing.T) {
	cfg, pf := pomoFile(t, func(c *config.Config) { c.Claude.Mode = "split" }, nil)
	at := t0.Add(time.Minute)
	f := withClaude(pf, claude.EvReady, at)
	r, _ := resolveSplit(t, cfg, f, nil, stripCaps(10), at.Add(5*time.Second))
	if r.Source != SourceClaude || r.Layer.State.Segments[0].Color != (color.RGB{G: 200, B: 83}) {
		t.Fatalf("ready is shown (green) during the hold: %+v", r)
	}
	r, _ = resolveSplit(t, cfg, f, nil, stripCaps(10), at.Add(11*time.Second))
	if r.Source != SourcePomo || len(r.Layer.State.Segments) != 10 || r.Layer.State.Segments[0].Color != pink {
		t.Fatalf("after the hold the pomodoro has the whole strip back, Claude's segments included: %v", r)
	}
}

func TestSplitFallsBackToOverrideWhenItCannotSplit(t *testing.T) {
	f := withClaude(state.File{}, claude.EvWorking, t0)
	at := t0.Add(time.Second)
	for name, caps := range map[string]govee.Capabilities{
		"no segments":            govee.DefaultCapabilities(),
		"no capabilities":        {},
		"Claude would own all":   stripCaps(2), // split_segments = 2 leaves nothing for the rest
		"fewer than it reserves": stripCaps(1),
	} {
		r, _ := resolveSplit(t, splitCfg(nil), f, nil, caps, at)
		if r.Source != SourceClaude || r.Split != 0 || len(r.Layer.State.Segments) != 0 ||
			r.Layer.State.Color == nil || *r.Layer.State.Color != orange || strings.Contains(r.Reason, "split") {
			t.Errorf("%s: should behave exactly like override mode: %+v", name, r)
		}
	}
}

func TestOverrideStillBeatsSplit(t *testing.T) {
	f := withClaude(state.File{Override: &state.Override{State: govee.State{On: true, Kelvin: 3000}}}, claude.EvWorking, t0)
	r, _ := resolveSplit(t, splitCfg(nil), f, nil, stripCaps(10), t0.Add(time.Second))
	if r.Source != SourceOverride {
		t.Fatal(r)
	}
}

// The waiting pulse: Claude's segments alternate full / 30% every 2 s for a
// minute, and the loop is woken for each step.
func TestSplitWaitingPulse(t *testing.T) {
	cfg := splitCfg(nil)
	f := withClaude(state.File{}, claude.EvWaiting, t0)
	bri := func(at time.Duration) int {
		r, _ := resolveSplit(t, cfg, f, nil, stripCaps(10), t0.Add(at))
		return r.Layer.State.Segments[0].Brightness
	}
	for _, c := range []struct {
		at   time.Duration
		want int
	}{
		{time.Second, 100}, {2 * time.Second, PulseDim}, {3500 * time.Millisecond, PulseDim},
		{4 * time.Second, 100}, {56 * time.Second, 100}, {58 * time.Second, PulseDim}, {59 * time.Second, PulseDim},
		{61 * time.Second, 100}, {10 * time.Minute, 100}, // pulsing stops after a minute
	} {
		if got := bri(c.at); got != c.want {
			t.Errorf("at %v: brightness %d, want %d", c.at, got, c.want)
		}
	}
	_, in := resolveSplit(t, cfg, f, nil, stripCaps(10), t0.Add(2500*time.Millisecond))
	if next, ok := NextChange(t0.Add(2500*time.Millisecond), in); !ok || !next.Equal(t0.Add(4*time.Second)) {
		t.Fatalf("wake at the next 2 s step: %v %v", next, ok)
	}
	_, in = resolveSplit(t, cfg, f, nil, stripCaps(10), t0.Add(59*time.Second))
	if next, ok := NextChange(t0.Add(59*time.Second), in); !ok || !next.Equal(t0.Add(60*time.Second)) {
		t.Fatalf("the last step ends the pulse: %v %v", next, ok)
	}
	_, in = resolveSplit(t, cfg, f, nil, stripCaps(10), t0.Add(2*time.Minute))
	if next, ok := NextChange(t0.Add(2*time.Minute), in); ok {
		t.Fatalf("no more wake-ups once it has stopped: %v", next)
	}
	// Working doesn't pulse.
	fw := withClaude(state.File{}, claude.EvWorking, t0)
	r, _ := resolveSplit(t, cfg, fw, nil, stripCaps(10), t0.Add(3*time.Second))
	if r.Layer.State.Segments[0].Brightness != 100 {
		t.Fatal("only waiting pulses")
	}
	// A device that can't set segment brightness can't pulse either.
	caps := stripCaps(10)
	caps.SegBrightness = false
	r, in = resolveSplit(t, cfg, f, nil, caps, t0.Add(3*time.Second))
	if r.Layer.State.Segments[0].Brightness != 0 {
		t.Fatalf("brightness can't be sent to this device: %+v", r.Layer.State.Segments[0])
	}
	if _, ok := NextChange(t0.Add(3*time.Second), in); ok {
		t.Fatal("nothing to pulse, nothing to wake for")
	}
}

// End to end through the fake light: what would be sent for Claude + a bar.
func TestSplitAndBarReachTheLightInOrder(t *testing.T) {
	cfg, pf := pomoFile(t, func(c *config.Config) { c.Claude.Mode = "split" }, nil)
	l := newFakeStrip()
	apply := func(f state.File, at time.Time) {
		t.Helper()
		in, err := BuildInputs(cfg, f, at, Env{Caps: stripCaps(10)})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Apply(context.Background(), Resolve(at, in).Layer.State); err != nil {
			t.Fatal(err)
		}
	}
	at := t0.Add(time.Second)
	apply(pf, at)
	pomoOnly := len(l.Log())
	apply(withClaude(pf, claude.EvWorking, at), at.Add(time.Second))
	// Claude arriving repaints its two (reserved, previously pink) segments in
	// one batched color request; the bar is untouched.
	if got := strings.Join(l.Log()[pomoOnly:], "|"); got != "segments 0-1 #FF8C00" {
		t.Fatalf("%v", got)
	}
	// Claude's session ends and the hold expires: its segments must go back to
	// the phase color, not stay orange.
	before := len(l.Log())
	apply(pf, at.Add(time.Minute))
	if got := strings.Join(l.Log()[before:], "|"); got != "segments 0-1 #FF4FA3" {
		t.Fatalf("Claude's segments should return to the phase color, and only they: %q", got)
	}
	_ = pomo.Focus
}
