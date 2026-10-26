package fake

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// Light records every command it would have sent and refuses what a real
// device with the same Capabilities would refuse. A zero Light accepts
// everything (govee.DefaultCapabilities). Out, if set, echoes each command as
// "dry-run: ...".
type Light struct {
	Dev govee.Device
	Out io.Writer

	mu    sync.Mutex
	state govee.State
	segs  map[int]govee.Segment
	cmds  []string
}

var _ govee.Light = (*Light)(nil)

// New returns a fake bound to dev.
func New(dev govee.Device, out io.Writer) *Light { return &Light{Dev: dev, Out: out} }

func (l *Light) caps() govee.Capabilities {
	if l.Dev.SKU == "" && l.Dev.ID == "" {
		return govee.DefaultCapabilities()
	}
	return l.Dev.Caps
}

func (l *Light) Device() govee.Device {
	d := l.Dev
	d.Caps = l.caps()
	return d
}

// Log returns a copy of the recorded commands.
func (l *Light) Log() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.cmds...)
}

func (l *Light) record(format string, a ...any) {
	cmd := fmt.Sprintf(format, a...)
	l.cmds = append(l.cmds, cmd)
	if l.Out != nil {
		fmt.Fprintln(l.Out, "dry-run:", cmd)
	}
}

func (l *Light) setPower(on bool) error {
	if err := l.caps().CheckPower(); err != nil {
		return err
	}
	if l.state.On == on {
		return nil
	}
	l.state.On = on
	l.record("power %t", on)
	return nil
}

func (l *Light) setBrightness(pct int) error {
	pct, err := l.caps().ClampBrightness(pct)
	if err != nil {
		return err
	}
	if l.state.Brightness == pct {
		return nil
	}
	l.state.Brightness = pct
	l.record("brightness %d", pct)
	return nil
}

func (l *Light) setColor(c color.RGB) error {
	if err := l.caps().CheckRGB(); err != nil {
		return err
	}
	if l.state.Color != nil && *l.state.Color == c {
		return nil
	}
	l.state.Color, l.state.Kelvin = &c, 0
	l.segs = nil // a whole-strip color overwrites segments
	l.record("color %s", c.Hex())
	return nil
}

func (l *Light) setKelvin(k int) error {
	k, err := l.caps().ClampKelvin(k)
	if err != nil {
		return err
	}
	if l.state.Kelvin == k {
		return nil
	}
	l.state.Kelvin, l.state.Color = k, nil
	l.segs = nil
	l.record("kelvin %d", k)
	return nil
}

// setSegments mirrors the cloud backend: only changed segments are sent, one
// command per distinct color and one per distinct brightness (colors first).
// A Segment's Brightness of 0 means "leave it alone".
func (l *Light) setSegments(segs []govee.Segment) error {
	if err := l.caps().CheckSegments(segs); err != nil {
		return err
	}
	if l.segs == nil {
		l.segs = map[int]govee.Segment{}
	}
	byIdx := map[int]govee.Segment{}
	for _, s := range segs {
		byIdx[s.Index] = s
	}
	idxs := make([]int, 0, len(byIdx))
	for i := range byIdx {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)

	var colorOrder []color.RGB
	var briOrder []int
	colorGroups, briGroups := map[color.RGB][]int{}, map[int][]int{}
	for _, i := range idxs {
		s := byIdx[i]
		old, known := l.segs[i]
		if !known || old.Color != s.Color {
			if _, seen := colorGroups[s.Color]; !seen {
				colorOrder = append(colorOrder, s.Color)
			}
			colorGroups[s.Color] = append(colorGroups[s.Color], i)
		}
		if s.Brightness != 0 && (!known || old.Brightness != s.Brightness) {
			if _, seen := briGroups[s.Brightness]; !seen {
				briOrder = append(briOrder, s.Brightness)
			}
			briGroups[s.Brightness] = append(briGroups[s.Brightness], i)
		}
	}
	for _, c := range colorOrder {
		for _, i := range colorGroups[c] {
			cur := l.segs[i]
			cur.Index, cur.Color = i, c
			l.segs[i] = cur
		}
		l.record("segments %s %s", govee.FormatIndices(colorGroups[c]), c.Hex())
	}
	for _, b := range briOrder {
		for _, i := range briGroups[b] {
			cur := l.segs[i]
			cur.Brightness = b
			l.segs[i] = cur
		}
		l.record("segment-brightness %s %d", govee.FormatIndices(briGroups[b]), b)
	}
	return nil
}

func (l *Light) SetPower(_ context.Context, on bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setPower(on)
}
func (l *Light) SetBrightness(_ context.Context, pct int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setBrightness(pct)
}
func (l *Light) SetColor(_ context.Context, c color.RGB) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setColor(c)
}
func (l *Light) SetKelvin(_ context.Context, k int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setKelvin(k)
}
func (l *Light) SetSegments(_ context.Context, segs []govee.Segment) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setSegments(segs)
}

// Apply fits the state to the device first, then sends the whole-strip
// commands before any segment commands (a whole-strip color would overwrite
// them otherwise).
func (l *Light) Apply(_ context.Context, s govee.State) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, err := l.caps().Fit(s)
	if err != nil {
		return err
	}
	if len(s.Segments) == 0 && len(l.segs) > 0 {
		// Mirror the cloud backend: leaving segment mode needs a whole-strip
		// command even if its value is unchanged.
		l.state.Color, l.state.Kelvin, l.state.Brightness = nil, 0, 0
		l.segs = nil
	}
	if err := l.setPower(s.On); err != nil || !s.On {
		return err
	}
	if s.Color != nil {
		err = l.setColor(*s.Color)
	} else if s.Kelvin != 0 {
		err = l.setKelvin(s.Kelvin)
	}
	if err == nil && s.Brightness != 0 {
		err = l.setBrightness(s.Brightness)
	}
	if err == nil && len(s.Segments) > 0 {
		err = l.setSegments(s.Segments)
	}
	return err
}

func (l *Light) Status(context.Context) (govee.State, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state, nil
}

// StripCaps is the default fake profile, modeled on a real capture of the
// strip this project targets (testdata/govee/h612f_devices.json): 10 segments
// with per-segment brightness, kelvin 2700-6500. It is a stand-in; real
// capabilities always come from the API.
func StripCaps() govee.Capabilities {
	return govee.Capabilities{Power: true, RGB: true, Kelvin: &govee.Range{Min: 2700, Max: 6500},
		Brightness: &govee.Range{Min: 1, Max: 100}, Segments: true, SegmentCount: 10, SegBrightness: true}
}

// Devices returns made-up devices for --dry-run and tests. The SKUs are
// obviously fake; the two shapes (segmented strip vs. white-only) exist to
// exercise capability handling.
func Devices() []govee.Device {
	return []govee.Device{
		{ID: "FA:KE:00:00:00:00:00:01", SKU: "FAKE-STRIP", Name: "desk", Backend: "cloud", Caps: StripCaps()},
		{ID: "FA:KE:00:00:00:00:00:02", SKU: "FAKE-WHITE", Name: "shelf", Backend: "cloud",
			Caps: govee.Capabilities{Power: true, Kelvin: &govee.Range{Min: 2200, Max: 6500},
				Brightness: &govee.Range{Min: 5, Max: 100}}},
	}
}
