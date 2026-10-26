package govee

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adnair2024/beacon/internal/color"
)

// Range is an inclusive integer range advertised by the API.
type Range struct{ Min, Max int }

func (r *Range) clamp(v int) int { return min(max(v, r.Min), r.Max) }

// Capabilities is what a device can do, parsed from the API at runtime.
// beacon never decides this from a SKU. Nil ranges mean "not supported".
type Capabilities struct {
	Power         bool
	RGB           bool
	Kelvin        *Range
	Brightness    *Range
	Segments      bool
	SegmentCount  int
	SegBrightness bool
	Raw           json.RawMessage `json:"-"` // the device's capabilities array, kept for debugging
}

// DefaultCapabilities is the permissive set used by backends and tests that
// can't ask a device: everything except segments.
func DefaultCapabilities() Capabilities {
	return Capabilities{Power: true, RGB: true, Kelvin: &Range{2000, 9000}, Brightness: &Range{1, 100}}
}

func (c Capabilities) String() string {
	var p []string
	if c.Power {
		p = append(p, "power")
	}
	if c.Brightness != nil {
		p = append(p, fmt.Sprintf("brightness(%d-%d)", c.Brightness.Min, c.Brightness.Max))
	}
	if c.RGB {
		p = append(p, "rgb")
	}
	if c.Kelvin != nil {
		p = append(p, fmt.Sprintf("kelvin(%d-%d)", c.Kelvin.Min, c.Kelvin.Max))
	}
	if c.Segments {
		s := fmt.Sprintf("segments×%d", c.SegmentCount)
		if c.SegBrightness {
			s += "+brightness"
		}
		p = append(p, s)
	}
	if len(p) == 0 {
		return "none"
	}
	return strings.Join(p, " ")
}

func unsupported(what, hint string) error {
	if hint != "" {
		return fmt.Errorf("%s: %w (%s)", what, ErrUnsupported, hint)
	}
	return fmt.Errorf("%s: %w", what, ErrUnsupported)
}

func (c Capabilities) CheckPower() error {
	if !c.Power {
		return unsupported("power", "")
	}
	return nil
}

func (c Capabilities) CheckRGB() error {
	if !c.RGB {
		return unsupported("color", "try a kelvin value instead")
	}
	return nil
}

// ClampBrightness returns pct limited to the advertised range.
func (c Capabilities) ClampBrightness(pct int) (int, error) {
	if c.Brightness == nil {
		return 0, unsupported("brightness", "")
	}
	return c.Brightness.clamp(pct), nil
}

// ClampKelvin returns k limited to the advertised range.
func (c Capabilities) ClampKelvin(k int) (int, error) {
	if c.Kelvin == nil {
		return 0, unsupported("kelvin", "")
	}
	return c.Kelvin.clamp(k), nil
}

func (c Capabilities) CheckSegments(segs []Segment) error {
	if !c.Segments {
		return unsupported("segments", "")
	}
	for _, s := range segs {
		if s.Index < 0 || s.Index >= c.SegmentCount {
			return fmt.Errorf("segment %d out of range 0-%d", s.Index, c.SegmentCount-1)
		}
		if s.Brightness != 0 && !c.SegBrightness {
			return unsupported("segment brightness", "")
		}
	}
	return nil
}

// Fit adapts a desired State to what the device can do, so a refused request
// never half-applies:
//   - brightness and kelvin are clamped to the advertised ranges;
//   - kelvin on a device without it is approximated in RGB;
//   - segments on a device without them collapse to the first segment's color;
//   - anything still impossible (color on a white-only light, segment indices
//     out of range) is an ErrUnsupported-wrapped error.
func (c Capabilities) Fit(s State) (State, error) {
	if !s.On {
		return State{}, c.CheckPower()
	}
	if err := c.CheckPower(); err != nil {
		return s, err
	}
	out := State{On: true}
	if len(s.Segments) > 0 {
		if c.Segments {
			if err := c.CheckSegments(s.Segments); err != nil {
				return s, err
			}
			out.Segments = append([]Segment(nil), s.Segments...)
		} else if s.Color == nil && s.Kelvin == 0 {
			first := s.Segments[0].Color
			s.Color = &first
		}
	}
	switch {
	case s.Color != nil:
		if err := c.CheckRGB(); err != nil {
			return s, err
		}
		col := *s.Color
		out.Color = &col
	case s.Kelvin != 0:
		if c.Kelvin != nil {
			out.Kelvin, _ = c.ClampKelvin(s.Kelvin)
		} else if c.RGB {
			col := color.FromKelvin(s.Kelvin)
			out.Color = &col
		} else {
			return s, unsupported("kelvin", "")
		}
	}
	if s.Brightness != 0 {
		b, err := c.ClampBrightness(s.Brightness)
		if err != nil {
			return s, err
		}
		out.Brightness = b
	}
	return out, nil
}
