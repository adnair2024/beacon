package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// Light is one cloud device. It remembers what it last sent and skips
// redundant commands; after any error it forgets, so the next Apply resends
// everything rather than trusting a state it can't vouch for.
type Light struct {
	c   *Client
	dev govee.Device

	mu         sync.Mutex
	last       govee.State
	powerKnown bool
	// segs is what we last sent per segment (logical indices). The API can't
	// read segment state back, so this is all we know; nil means "unknown,
	// resend everything".
	segs map[int]govee.Segment
}

var _ govee.Light = (*Light)(nil)

// Light returns a govee.Light bound to dev.
func (c *Client) Light(dev govee.Device) *Light { return &Light{c: c, dev: dev} }

func (l *Light) Device() govee.Device { return l.dev }

func (l *Light) wrap(err error) error { return fmt.Errorf("cloud: %s: %w", l.dev.Name, err) }

func (l *Light) invalidate() { l.last, l.powerKnown, l.segs = govee.State{}, false, nil }

func (l *Light) send(ctx context.Context, typ, inst string, v any) error {
	if err := l.c.control(ctx, l.dev, capability{Type: typ, Instance: inst, Value: v}); err != nil {
		l.invalidate()
		return l.wrap(err)
	}
	return nil
}

func (l *Light) setPower(ctx context.Context, on bool) error {
	if err := l.dev.Caps.CheckPower(); err != nil {
		return l.wrap(err)
	}
	if l.powerKnown && l.last.On == on {
		return nil
	}
	v := 0
	if on {
		v = 1
	}
	if err := l.send(ctx, typeOnOff, instPower, v); err != nil {
		return err
	}
	l.last.On, l.powerKnown = on, true
	return nil
}

func (l *Light) setBrightness(ctx context.Context, pct int) error {
	pct, err := l.dev.Caps.ClampBrightness(pct)
	if err != nil {
		return l.wrap(err)
	}
	if l.last.Brightness == pct {
		return nil
	}
	if err := l.send(ctx, typeRange, instBright, pct); err != nil {
		return err
	}
	l.last.Brightness = pct
	l.segs = nil // a whole-strip change may reset the segments; don't trust our memory
	return nil
}

func (l *Light) setColor(ctx context.Context, c color.RGB) error {
	if err := l.dev.Caps.CheckRGB(); err != nil {
		return l.wrap(err)
	}
	if l.last.Color != nil && *l.last.Color == c {
		return nil
	}
	if err := l.send(ctx, typeColor, instRGB, c.Packed()); err != nil {
		return err
	}
	l.last.Color, l.last.Kelvin = &c, 0
	l.segs = nil // whole-strip color overwrites segment colors (seen on a real strip)
	return nil
}

func (l *Light) setKelvin(ctx context.Context, k int) error {
	k, err := l.dev.Caps.ClampKelvin(k)
	if err != nil {
		return l.wrap(err)
	}
	if l.last.Kelvin == k {
		return nil
	}
	if err := l.send(ctx, typeColor, instKelvin, k); err != nil {
		return err
	}
	l.last.Kelvin, l.last.Color = k, nil
	l.segs = nil
	return nil
}

func (l *Light) SetPower(ctx context.Context, on bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setPower(ctx, on)
}

func (l *Light) SetBrightness(ctx context.Context, pct int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setBrightness(ctx, pct)
}

func (l *Light) SetColor(ctx context.Context, c color.RGB) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setColor(ctx, c)
}

func (l *Light) SetKelvin(ctx context.Context, k int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setKelvin(ctx, k)
}

// wireIndex maps a logical segment index to the one the API expects.
func (l *Light) wireIndex(i int) int {
	if l.dev.SegReverse && l.dev.Caps.SegmentCount > 0 {
		return l.dev.Caps.SegmentCount - 1 - i
	}
	return i
}

func (l *Light) wireIndices(idx []int) []int {
	out := make([]int, len(idx))
	for i, v := range idx {
		out[i] = l.wireIndex(v)
	}
	sort.Ints(out)
	return out
}

// setSegments sends only what changed, batched: one segmentedColorRgb call per
// distinct color and one segmentedBrightness call per distinct brightness
// (colors first, so a brightness never lands on a stale color). A Segment's
// Brightness of 0 means "leave brightness alone". After any error our memory
// is dropped, so the next call resends every segment.
func (l *Light) setSegments(ctx context.Context, segs []govee.Segment) error {
	if err := l.dev.Caps.CheckSegments(segs); err != nil {
		return l.wrap(err)
	}
	// Dedupe (the API tolerates duplicates, but they'd defeat batching); last wins.
	byIdx := map[int]govee.Segment{}
	for _, s := range segs {
		byIdx[s.Index] = s
	}
	idxs := make([]int, 0, len(byIdx))
	for i := range byIdx {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)

	var colorOrder, briOrder []int // distinct values, in order of first segment
	colorGroups, briGroups := map[int][]int{}, map[int][]int{}
	for _, i := range idxs {
		s, old := byIdx[i], l.segs[i]
		_, known := l.segs[i]
		if !known || old.Color != s.Color {
			k := s.Color.Packed()
			if _, seen := colorGroups[k]; !seen {
				colorOrder = append(colorOrder, k)
			}
			colorGroups[k] = append(colorGroups[k], i)
		}
		if s.Brightness != 0 && (!known || old.Brightness != s.Brightness) {
			if _, seen := briGroups[s.Brightness]; !seen {
				briOrder = append(briOrder, s.Brightness)
			}
			briGroups[s.Brightness] = append(briGroups[s.Brightness], i)
		}
	}
	if l.segs == nil && (len(colorOrder) > 0 || len(briOrder) > 0) {
		l.segs = map[int]govee.Segment{}
	}
	for _, k := range colorOrder {
		group := colorGroups[k]
		if err := l.send(ctx, typeSegment, instSegRGB, segColorValue{Segment: l.wireIndices(group), RGB: k}); err != nil {
			return err // send() already dropped our memory
		}
		for _, i := range group {
			cur := l.segs[i]
			cur.Index, cur.Color = i, byIdx[i].Color
			l.segs[i] = cur
		}
	}
	for _, b := range briOrder {
		group := briGroups[b]
		if err := l.send(ctx, typeSegment, instSegBri, segBrightnessValue{Segment: l.wireIndices(group), Brightness: b}); err != nil {
			return err
		}
		for _, i := range group {
			cur := l.segs[i]
			cur.Brightness = b
			l.segs[i] = cur
		}
	}
	return nil
}

func (l *Light) SetSegments(ctx context.Context, segs []govee.Segment) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.setSegments(ctx, segs)
}

// Apply fits the whole state to the device's capabilities first, so an
// impossible request sends nothing at all, then sends only what changed.
func (l *Light) Apply(ctx context.Context, s govee.State) error {
	s, err := l.dev.Caps.Fit(s)
	if err != nil {
		return l.wrap(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(s.Segments) == 0 && len(l.segs) > 0 {
		// Segments are in effect on the strip and the new state has none. A
		// whole-strip command is what clears them, but it would be skipped as
		// redundant if its value hasn't changed, so force it.
		l.last.Color, l.last.Kelvin, l.last.Brightness = nil, 0, 0
		l.segs = nil
	}
	if err := l.setPower(ctx, s.On); err != nil || !s.On {
		return err
	}
	switch {
	case s.Color != nil:
		if err := l.setColor(ctx, *s.Color); err != nil {
			return err
		}
	case s.Kelvin != 0:
		if err := l.setKelvin(ctx, s.Kelvin); err != nil {
			return err
		}
	}
	if s.Brightness != 0 {
		if err := l.setBrightness(ctx, s.Brightness); err != nil {
			return err
		}
	}
	if len(s.Segments) > 0 { // after the whole-strip commands, which would overwrite them
		return l.setSegments(ctx, s.Segments)
	}
	return nil
}

func intValue(raw json.RawMessage) (int, bool) {
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false // e.g. "" for an instance that can't be queried
	}
	return int(f), true
}

// Status queries the device (30/min/device limit; don't call it per frame).
// If the device is offline the other values are stale, so ErrOffline is
// returned alongside the last-known state.
func (l *Light) Status(ctx context.Context) (govee.State, error) {
	r, err := l.c.state(ctx, l.dev)
	if err != nil {
		return govee.State{}, l.wrap(err)
	}
	var s govee.State
	offline := false
	for _, k := range r.Payload.Capabilities {
		v, ok := intValue(k.State.Value)
		switch {
		case k.Type == typeOnline && k.Instance == instOnline:
			var b bool
			if json.Unmarshal(k.State.Value, &b) == nil && !b {
				offline = true
			}
		case !ok:
		case k.Instance == instPower:
			s.On = v == 1
		case k.Instance == instBright:
			s.Brightness = v
		case k.Instance == instKelvin:
			s.Kelvin = v
		case k.Instance == instRGB:
			c := color.FromPacked(v)
			s.Color = &c
		}
	}
	// The device reports both color and kelvin; kelvin > 0 means white mode.
	if s.Kelvin > 0 {
		s.Color = nil
	}
	if offline {
		return s, l.wrap(ErrOffline)
	}
	return s, nil
}
