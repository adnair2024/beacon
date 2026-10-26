package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// cell is one segment of the strip as it should appear on screen.
type cell struct {
	c  color.RGB
	on bool
}

// label names a range of cells (inclusive) under the strip.
type label struct {
	from, to int
	text     string
}

// noColor reports whether the user asked for no color; the strip then
// becomes a text description.
var noColor = func() bool { return os.Getenv("NO_COLOR") != "" }

func scaled(c color.RGB, pct int) color.RGB {
	if pct <= 0 || pct > 100 {
		pct = 100
	}
	return c.Scale(pct)
}

// cellsFor expresses a light state as screen cells: one per segment for
// segmented devices, otherwise a single cell. Brightness dims the color so
// what you see is roughly what the light shows.
func cellsFor(st govee.State, caps govee.Capabilities) []cell {
	n := 1
	if caps.Segments && caps.SegmentCount > 0 {
		n = caps.SegmentCount
	}
	out := make([]cell, n)
	if !st.On {
		return out
	}
	base := color.FromKelvin(4000) // "on" with no color: neutral white
	switch {
	case st.Color != nil:
		base = *st.Color
	case st.Kelvin != 0:
		base = color.FromKelvin(st.Kelvin)
	}
	whole := scaled(base, st.Brightness)
	for i := range out {
		out[i] = cell{c: whole, on: true}
	}
	for _, s := range st.Segments {
		if s.Index >= 0 && s.Index < n {
			b := s.Brightness
			if b == 0 {
				b = st.Brightness
			}
			out[s.Index] = cell{c: scaled(s.Color, b), on: true}
		}
	}
	return out
}

// renderStrip draws the cells scaled to width, rows tall, with labels
// underneath. The same widget serves the dashboard, pomo tab, previews, and
// (via --dry-run) stands in for the real light.
func renderStrip(th theme, cells []cell, labels []label, width, rows int) string {
	if len(cells) == 0 || width < len(cells) {
		return ""
	}
	if noColor() {
		return plainStrip(cells, labels)
	}
	cw := width / len(cells)
	if len(cells) > 1 {
		cw = min(cw, 6) // a 15-segment strip shouldn't become a wall
	}
	var row strings.Builder
	for _, c := range cells {
		hex := th.offHex()
		if c.on {
			hex = c.c.Hex()
		}
		row.WriteString(th.block(hex, cw, 1))
	}
	lines := make([]string, 0, rows+1)
	for i := 0; i < rows; i++ {
		lines = append(lines, row.String())
	}
	if len(labels) > 0 {
		// Pad to the bar's width: callers center the block, and a shorter
		// line would be centered separately and drift out from under the bar.
		ll := labelLine(labels, cw)
		ll += strings.Repeat(" ", max(0, cw*len(cells)-len([]rune(ll))))
		lines = append(lines, th.dim().Render(ll))
	}
	return strings.Join(lines, "\n")
}

// labelLine positions each label under its cell range.
func labelLine(labels []label, cw int) string {
	var b strings.Builder
	pos := 0
	for _, l := range labels {
		start, w := l.from*cw, (l.to-l.from+1)*cw
		if start < pos {
			continue
		}
		b.WriteString(strings.Repeat(" ", start-pos))
		t := l.text
		if len([]rune(t)) > w {
			r := []rune(t)
			t = string(r[:max(0, w-1)]) + "…"
		}
		b.WriteString(t)
		b.WriteString(strings.Repeat(" ", max(0, w-len([]rune(t)))))
		pos = start + w
	}
	return strings.TrimRight(b.String(), " ")
}

// plainStrip is the NO_COLOR rendering: [0-1 label] [2-14 label], or a
// description of the single cell.
func plainStrip(cells []cell, labels []label) string {
	if len(labels) > 0 {
		parts := make([]string, 0, len(labels))
		for _, l := range labels {
			rng := fmt.Sprint(l.from)
			if l.to != l.from {
				rng = fmt.Sprintf("%d-%d", l.from, l.to)
			}
			parts = append(parts, fmt.Sprintf("[%s %s]", rng, l.text))
		}
		return strings.Join(parts, " ")
	}
	if !cells[0].on {
		return "[off]"
	}
	return fmt.Sprintf("[%s]", cells[0].c.Hex())
}
