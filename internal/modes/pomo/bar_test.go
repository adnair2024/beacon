package pomo

import (
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/state"
)

func running(t *testing.T) (state.Pomo, Settings) {
	t.Helper()
	s := defaults()
	p, err := Start(state.Pomo{}, t0, s)
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}

func at(t *testing.T, p state.Pomo, s Settings, d time.Duration) Status {
	t.Helper()
	return Evaluate(p, t0.Add(d), s)
}

// 25 minutes over 10 segments is 2m30s per segment.
func TestFilledDrainAndFill(t *testing.T) {
	p, s := running(t)
	seg := 150 * time.Second
	cases := []struct {
		at          time.Duration
		drain, fill int
	}{
		{0, 10, 0},
		{seg - time.Second, 10, 0},
		{seg, 9, 1}, // the first slice is used up exactly now
		{seg + time.Second, 9, 1},
		{5 * seg, 5, 5},
		{9*seg + 149*time.Second, 1, 9}, // last slice, one second to go: still one lit
		{9 * seg, 1, 9},
		{10*seg - time.Second, 1, 9},
		{10 * seg, 0, 10}, // phase over: this is where the state machine moves on
	}
	for _, c := range cases {
		// Evaluate would advance past the end; build the status by hand there.
		st := at(t, p, s, c.at)
		if c.at >= 10*seg {
			st = Status{State: Running, Total: 25 * time.Minute, Remaining: 0}
		}
		if got := st.Filled(10, "drain"); got != c.drain {
			t.Errorf("drain at %v: %d, want %d", c.at, got, c.drain)
		}
		if got := st.Filled(10, "fill"); got != c.fill {
			t.Errorf("fill at %v: %d, want %d", c.at, got, c.fill)
		}
	}
	if (Status{}).Filled(10, "drain") != 0 || (Status{Total: time.Minute, Remaining: time.Minute}).Filled(0, "drain") != 0 {
		t.Fatal("degenerate inputs are empty, not a panic")
	}
	// An unknown direction behaves like the default.
	if got := at(t, p, s, 0).Filled(10, "sideways"); got != 10 {
		t.Fatal(got)
	}
}

// The property the daemon depends on: Filled is constant until exactly the
// instant NextBarChange reports, and differs from then on.
func TestNextBarChangeIsExact(t *testing.T) {
	p, s := running(t)
	for _, dir := range []string{"drain", "fill"} {
		for _, count := range []int{1, 2, 3, 7, 10, 15} {
			now := t0.Add(7 * time.Second)
			changes := 0
			for i := 0; i < count+2; i++ {
				st := Evaluate(p, now, s)
				k := st.Filled(count, dir)
				next, ok := st.NextBarChange(count, dir)
				if !ok {
					break
				}
				if !next.After(now) {
					t.Fatalf("%s/%d: next change %v is not in the future (now %v)", dir, count, next, now)
				}
				before := Evaluate(p, next.Add(-time.Nanosecond), s).Filled(count, dir)
				var after int
				if !next.Before(p.EndsAt) { // the phase ends here; the state machine takes over
					after = Status{State: Running, Total: st.Total}.Filled(count, dir)
					if dir == "fill" {
						after = count
					}
				} else {
					after = Evaluate(p, next, s).Filled(count, dir)
				}
				if before != k {
					t.Fatalf("%s/%d: Filled moved early: %d at %v, %d just before %v", dir, count, k, now, before, next)
				}
				if after == k {
					t.Fatalf("%s/%d: Filled did not change at the reported instant %v (still %d)", dir, count, next, k)
				}
				changes++
				if !next.Before(p.EndsAt) {
					break // the phase is over; what follows belongs to the next one
				}
				now = next
			}
			if changes != count {
				t.Errorf("%s/%d: expected %d changes over the phase, saw %d", dir, count, count, changes)
			}
		}
	}
}

func TestNextBarChangeOnlyForRunningTimers(t *testing.T) {
	p, s := running(t)
	paused, _ := Pause(p, t0.Add(time.Minute), s)
	if _, ok := Evaluate(paused, t0.Add(time.Hour), s).NextBarChange(10, "drain"); ok {
		t.Fatal("a paused timer has nothing to wake for")
	}
	if _, ok := (Status{}).NextBarChange(10, "drain"); ok {
		t.Fatal("idle")
	}
}

func bri(segs []govee.Segment) []int {
	out := make([]int, len(segs))
	for i, s := range segs {
		out[i] = s.Brightness
	}
	return out
}

func TestBarSegmentsDrain(t *testing.T) {
	p, s := running(t)
	pink := color.RGB{R: 255, G: 79, B: 163}
	segs := at(t, p, s, 5*150*time.Second).BarSegments(10, "drain", 20, pink, true)
	want := []int{100, 100, 100, 100, 100, 20, 20, 20, 20, 20}
	for i, b := range bri(segs) {
		if b != want[i] || segs[i].Index != i || segs[i].Color != pink {
			t.Fatalf("segment %d: %+v, want brightness %d in the phase color", i, segs[i], want[i])
		}
	}
	// Fill mode lights the same ones from the other direction in time.
	segs = at(t, p, s, 3*150*time.Second).BarSegments(10, "fill", 20, pink, true)
	if got := bri(segs); got[2] != 100 || got[3] != 20 {
		t.Fatalf("3 slices elapsed: segments 0-2 lit, 3+ dim: %v", got)
	}
}

func TestBarSegmentsPausedAndOff(t *testing.T) {
	p, s := running(t)
	pink := color.RGB{R: 255, G: 79, B: 163}
	paused, _ := Pause(p, t0.Add(5*150*time.Second), s)
	segs := Evaluate(paused, t0.Add(time.Hour), s).BarSegments(10, "drain", 20, pink, true)
	if got := bri(segs); got[0] != PausedBrightness || got[9] != 4 { // 20% of the 20% dim level
		t.Fatalf("a paused bar keeps its shape at 20%%: %v", got)
	}
	// dim 0: unfilled segments are turned off by coloring them black.
	segs = at(t, p, s, 5*150*time.Second).BarSegments(10, "drain", 0, pink, true)
	if segs[0].Color != pink || segs[9].Color != (color.RGB{}) || segs[9].Brightness != 100 {
		t.Fatalf("%+v %+v", segs[0], segs[9])
	}
	// A device with no segment brightness can only show lit or off.
	segs = at(t, p, s, 5*150*time.Second).BarSegments(10, "drain", 20, pink, false)
	for i, sg := range segs {
		if sg.Brightness != 0 {
			t.Fatalf("segment %d: brightness can't be sent to this device: %+v", i, sg)
		}
		if (i < 5) != (sg.Color == pink) || (i >= 5 && sg.Color != (color.RGB{})) {
			t.Fatalf("segment %d: %+v", i, sg)
		}
	}
	// A tiny dim level never rounds down to 0 (which would mean "inherit").
	if segs := at(t, p, s, 5*150*time.Second).BarSegments(10, "drain", 1, pink, true); segs[9].Brightness != 1 {
		t.Fatalf("%+v", segs[9])
	}
}
