package engine

import (
	"context"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/state"
)

// A strip like the real one: 10 segments with per-segment brightness.
func stripCaps(n int) govee.Capabilities {
	return govee.Capabilities{Power: true, RGB: true, Kelvin: &govee.Range{Min: 2700, Max: 6500},
		Brightness: &govee.Range{Min: 1, Max: 100}, Segments: true, SegmentCount: n, SegBrightness: true}
}

func barAt(t *testing.T, cfgMut func(*config.Config), caps govee.Capabilities, steps func(state.Pomo, pomo.Settings) state.Pomo, at time.Time) (Result, Inputs) {
	t.Helper()
	cfg, f := pomoFile(t, cfgMut, steps)
	in, err := BuildInputs(cfg, f, at, Env{Caps: caps})
	if err != nil {
		t.Fatal(err)
	}
	return Resolve(at, in), in
}

func segBri(st govee.State) []int {
	out := make([]int, len(st.Segments))
	for i, s := range st.Segments {
		out[i] = s.Brightness
	}
	return out
}

func eq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPomodoroIsABarOnASegmentedStrip(t *testing.T) {
	at := t0.Add(5 * 150 * time.Second) // half the 25 minutes
	r, _ := barAt(t, nil, stripCaps(10), nil, at)
	st := r.Layer.State
	if r.Source != SourcePomo || len(st.Segments) != 10 {
		t.Fatalf("%v %+v", r, st)
	}
	// Half drained: segments 0-4 lit at 100, 5-9 dim at the default 20%.
	if got := segBri(st); !eq(got, []int{100, 100, 100, 100, 100, 20, 20, 20, 20, 20}) {
		t.Fatalf("%v", got)
	}
	pink := color.RGB{R: 255, G: 79, B: 163}
	if st.Color == nil || *st.Color != pink || st.Brightness != 100 {
		t.Fatalf("whole strip stays at full brightness in the phase color: %+v", st)
	}
	for _, s := range st.Segments {
		if s.Color != pink {
			t.Fatalf("segment %d: %+v", s.Index, s)
		}
	}
}

func TestBarFollowsTheDeviceNotTheModel(t *testing.T) {
	at := t0.Add(5 * 150 * time.Second)
	r, _ := barAt(t, nil, stripCaps(8), nil, at)
	if len(r.Layer.State.Segments) != 8 {
		t.Fatalf("segment count comes from the capabilities: %d", len(r.Layer.State.Segments))
	}
	// A device without segments gets the plain solid color.
	r, _ = barAt(t, nil, govee.DefaultCapabilities(), nil, at)
	if len(r.Layer.State.Segments) != 0 || r.Layer.State.Brightness != 100 || r.Layer.State.Color == nil {
		t.Fatalf("%+v", r.Layer.State)
	}
	// Segment support the user switched off is just "no segments".
	r, _ = barAt(t, nil, govee.Capabilities{}, nil, at)
	if len(r.Layer.State.Segments) != 0 {
		t.Fatal("zero capabilities: solid")
	}
}

func TestBarConfigSwitches(t *testing.T) {
	at := t0.Add(3 * 150 * time.Second)
	// progress = "solid" turns the bar off even on a segmented strip.
	r, _ := barAt(t, func(c *config.Config) { c.Pomo.Progress = "solid" }, stripCaps(10), nil, at)
	if len(r.Layer.State.Segments) != 0 {
		t.Fatal("solid means solid")
	}
	// fill instead of drain.
	r, _ = barAt(t, func(c *config.Config) { c.Pomo.ProgressDirection = "fill" }, stripCaps(10), nil, at)
	if got := segBri(r.Layer.State); got[2] != 100 || got[3] != 20 {
		t.Fatalf("3 slices elapsed in fill mode: %v", got)
	}
	// A custom dim level.
	r, _ = barAt(t, func(c *config.Config) { c.Pomo.ProgressDim = 35 }, stripCaps(10), nil, at)
	if got := segBri(r.Layer.State); got[9] != 35 {
		t.Fatalf("%v", got)
	}
	// dim 0: unfilled segments are black.
	r, _ = barAt(t, func(c *config.Config) { c.Pomo.ProgressDim = 0 }, stripCaps(10), nil, at)
	if s := r.Layer.State.Segments[9]; s.Color != (color.RGB{}) {
		t.Fatalf("%+v", s)
	}
	// No segment brightness on the device: lit or off only.
	caps := stripCaps(10)
	caps.SegBrightness = false
	r, _ = barAt(t, nil, caps, nil, at)
	for _, s := range r.Layer.State.Segments {
		if s.Brightness != 0 {
			t.Fatalf("can't send brightness to this device: %+v", s)
		}
	}
}

func TestPausedBarKeepsItsShapeAtTwentyPercent(t *testing.T) {
	paused := func(p state.Pomo, ps pomo.Settings) state.Pomo {
		p, _ = pomo.Pause(p, t0.Add(5*150*time.Second), ps)
		return p
	}
	r, in := barAt(t, nil, stripCaps(10), paused, t0.Add(3*time.Hour))
	if got := segBri(r.Layer.State); !eq(got, []int{20, 20, 20, 20, 20, 4, 4, 4, 4, 4}) {
		t.Fatalf("%v", got)
	}
	if r.Layer.State.Brightness != 100 {
		t.Fatal("pause dims the segments, not the whole strip")
	}
	if _, ok := NextChange(t0.Add(3*time.Hour), in); ok {
		t.Fatal("a paused timer has nothing to wake for")
	}
}

func TestWaitingIsNotABar(t *testing.T) {
	waiting := func(p state.Pomo, ps pomo.Settings) state.Pomo { return pomo.Advance(p, t0.Add(26*time.Minute), ps) }
	r, _ := barAt(t, func(c *config.Config) { c.Pomo.AutoStartBreaks = false }, stripCaps(10), waiting, t0.Add(26*time.Minute))
	if len(r.Layer.State.Segments) != 0 || r.Layer.State.Brightness == 0 {
		t.Fatalf("waiting (and its flash) are whole-strip: %+v", r.Layer.State)
	}
}

// The loop must wake exactly when a segment's slice ends, and not before.
func TestWakesAtEachSegmentBoundary(t *testing.T) {
	seg := 150 * time.Second
	_, in := barAt(t, nil, stripCaps(10), nil, t0.Add(10*time.Second))
	if next, ok := NextChange(t0.Add(10*time.Second), in); !ok || !next.Equal(t0.Add(seg)) {
		t.Fatalf("first boundary: %v %v", next, ok)
	}
	_, in = barAt(t, nil, stripCaps(10), nil, t0.Add(seg+time.Second))
	if next, _ := NextChange(t0.Add(seg+time.Second), in); !next.Equal(t0.Add(2 * seg)) {
		t.Fatalf("second boundary: %v", next)
	}
	// In the last slice the next change is the end of the phase itself.
	last := t0.Add(9*seg + time.Second)
	_, in = barAt(t, nil, stripCaps(10), nil, last)
	if next, _ := NextChange(last, in); !next.Equal(t0.Add(25 * time.Minute)) {
		t.Fatalf("last boundary: %v", next)
	}
	// And the bar's content really changes there.
	before, _ := barAt(t, nil, stripCaps(10), nil, t0.Add(seg-time.Millisecond))
	after, _ := barAt(t, nil, stripCaps(10), nil, t0.Add(seg))
	if eq(segBri(before.Layer.State), segBri(after.Layer.State)) {
		t.Fatal("the bar must differ across the reported boundary")
	}
}

// End to end through the fake light: what would actually be sent.
func TestBarReachesTheLightAsBatchedChangesOnly(t *testing.T) {
	cfg, f := pomoFile(t, nil, nil)
	l := newFakeStrip()
	at := t0.Add(time.Second)
	apply := func(at time.Time) {
		t.Helper()
		in, err := BuildInputs(cfg, f, at, Env{Caps: stripCaps(10)})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Apply(context.Background(), Resolve(at, in).Layer.State); err != nil {
			t.Fatal(err)
		}
	}
	apply(at)
	first := len(l.Log())
	apply(t0.Add(2 * time.Second)) // same bar: nothing new to send
	if len(l.Log()) != first {
		t.Fatalf("an unchanged bar must send nothing: %v", l.Log()[first:])
	}
	apply(t0.Add(150 * time.Second)) // one slice used up: one command
	if got := l.Log()[first:]; len(got) != 1 || got[0] != "segment-brightness 9 20" {
		t.Fatalf("%v", got)
	}
}
