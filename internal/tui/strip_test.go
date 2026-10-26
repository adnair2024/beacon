package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/fake"
)

func TestCellsForWholeStripAndBrightness(t *testing.T) {
	c := color.RGB{R: 200, G: 100}
	cells := cellsFor(govee.State{On: true, Brightness: 50, Color: &c}, govee.Capabilities{})
	if len(cells) != 1 || !cells[0].on || cells[0].c != (color.RGB{R: 100, G: 50}) {
		t.Fatalf("%+v", cells)
	}
	if off := cellsFor(govee.State{}, fake.StripCaps()); len(off) != 10 || off[0].on {
		t.Fatalf("off strip should be 10 dark cells, got %+v", off)
	}
	if k := cellsFor(govee.State{On: true, Kelvin: 2000}, govee.Capabilities{}); k[0].c.R != 255 || k[0].c.B > 100 {
		t.Fatalf("2000K should be warm: %+v", k[0])
	}
}

func TestCellsForSegments(t *testing.T) {
	base := color.RGB{R: 255}
	seg := color.RGB{B: 255}
	st := govee.State{On: true, Brightness: 100, Color: &base,
		Segments: []govee.Segment{{Index: 2, Color: seg, Brightness: 50}, {Index: 99, Color: seg}}}
	cells := cellsFor(st, fake.StripCaps())
	if len(cells) != 10 || cells[0].c != base || cells[2].c != (color.RGB{B: 127}) {
		t.Fatalf("segment override should apply with its own brightness: %+v", cells[:4])
	}
}

func TestLabelLineAlignsAndTruncates(t *testing.T) {
	got := labelLine([]label{{0, 1, "Claude"}, {2, 14, "Pomo · focus"}}, 4)
	// cell width 4: "Claude" gets 8 columns, Pomo starts at column 8.
	if got != "Claude  Pomo · focus" {
		t.Fatalf("%q", got)
	}
	if got := labelLine([]label{{0, 0, "Overlong"}}, 4); got != "Ove…" {
		t.Fatalf("%q", got)
	}
}

func TestPlainStripForNoColor(t *testing.T) {
	cells := cellsFor(govee.State{On: true}, fake.StripCaps())
	got := plainStrip(cells, []label{{0, 1, "working"}, {2, 14, "focus 87%"}})
	if got != "[0-1 working] [2-14 focus 87%]" {
		t.Fatal(got)
	}
	if got := plainStrip(cellsFor(govee.State{}, govee.Capabilities{}), nil); got != "[off]" {
		t.Fatal(got)
	}
}

func TestRenderStripHonorsNoColor(t *testing.T) {
	old := noColor
	defer func() { noColor = old }()
	noColor = func() bool { return true }
	cells := cellsFor(govee.State{On: true}, govee.Capabilities{})
	out := renderStrip(newTheme("#FFFFFF", true), cells, []label{{0, 0, "idle"}}, 40, 2)
	if !strings.Contains(out, "[0 idle]") {
		t.Fatal(out)
	}
	noColor = func() bool { return false }
	out = renderStrip(newTheme("#FFFFFF", true), cells, []label{{0, 0, "idle"}}, 40, 2)
	if strings.Count(out, "\n") != 2 || !strings.HasSuffix(strings.TrimRight(out, " "), "idle") {
		t.Fatalf("2 rows + label line expected:\n%q", out)
	}
}

// The label line must be exactly as wide as the bar so that centering the
// whole block keeps labels under the segments they name.
func TestLabelLineIsPaddedToTheBarWidth(t *testing.T) {
	th := newTheme("#FFFFFF", true)
	cells := cellsFor(govee.State{On: true}, fake.StripCaps())
	out := renderStrip(th, cells, []label{{0, 3, "lit"}, {4, 9, "dim"}}, 50, 1)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("%q", out)
	}
	if lipgloss.Width(lines[0]) != lipgloss.Width(lines[1]) {
		t.Fatalf("bar is %d wide but its labels are %d", lipgloss.Width(lines[0]), lipgloss.Width(lines[1]))
	}
}
