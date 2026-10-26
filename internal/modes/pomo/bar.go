package pomo

import (
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// PausedBrightness is how dim a paused timer looks (percent).
const PausedBrightness = 20

// Filled is how many of count segments are lit. In "drain" mode (the default)
// the bar starts full and empties as time runs out, retracting toward
// segment 0; a segment stays lit until its whole slice of time is used up. In
// "fill" mode it starts empty and fills the same way.
func (st Status) Filled(count int, dir string) int {
	if count <= 0 || st.Total <= 0 {
		return 0
	}
	rem := int64(st.Remaining)
	total, n := int64(st.Total), int64(count)
	var filled int64
	if dir == "fill" {
		filled = (total - rem) * n / total // floor of elapsed
	} else {
		filled = (rem*n + total - 1) / total // ceil of remaining
	}
	return int(min(max(filled, 0), n))
}

// NextBarChange is the moment Filled next changes for a running timer, so a
// loop can wake exactly then and send one update. Paused and waiting timers
// have none.
func (st Status) NextBarChange(count int, dir string) (time.Time, bool) {
	if st.State != Running || count <= 0 || st.Total <= 0 || st.Boundary.IsZero() {
		return time.Time{}, false
	}
	k := int64(st.Filled(count, dir))
	total, n := int64(st.Total), int64(count)
	if dir == "fill" {
		if k >= n {
			return time.Time{}, false
		}
		// Filled becomes k+1 once elapsed >= ceil((k+1)*total/n).
		need := ((k+1)*total + n - 1) / n
		return st.Boundary.Add(-st.Total).Add(time.Duration(need)), true
	}
	if k <= 0 {
		return time.Time{}, false
	}
	// Filled drops to k-1 once remaining <= floor((k-1)*total/n).
	left := (k - 1) * total / n
	return st.Boundary.Add(-time.Duration(left)), true
}

var black = color.RGB{}

// BarSegments is the per-segment state for the bar: lit segments in the phase
// color at full brightness, the rest in the same color at dim percent. When
// dim is 0, or the device can't set segment brightness, the unfilled
// segments are switched off by coloring them black instead. Paused shows the
// same bar at PausedBrightness.
func (st Status) BarSegments(count int, dir string, dim int, c color.RGB, segBrightness bool) []govee.Segment {
	filled := st.Filled(count, dir)
	lit := 100
	if st.State == Paused {
		lit = PausedBrightness
	}
	unfilledBri := dim * lit / 100
	if dim > 0 {
		unfilledBri = max(unfilledBri, 1)
	}
	segs := make([]govee.Segment, count)
	for i := range segs {
		s := govee.Segment{Index: i, Color: c}
		switch {
		case !segBrightness: // can't dim: lit or off, nothing in between
			if i >= filled {
				s.Color = black
			}
		case i < filled:
			s.Brightness = lit
		case dim == 0:
			s.Color, s.Brightness = black, lit
		default:
			s.Brightness = unfilledBri
		}
		segs[i] = s
	}
	return segs
}
