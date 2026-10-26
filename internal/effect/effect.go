package effect

import (
	"fmt"
	"math"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// Kind names an effect.
type Kind string

const (
	Solid   Kind = "solid"
	Pulse   Kind = "pulse"   // quick brightness wobble, for "busy"
	Breathe Kind = "breathe" // slow, deep fade in and out, for "calm"
	Flash   Kind = "flash"   // hard on/off, for "needs attention"
	Rainbow Kind = "rainbow" // hue rotation, ignores the base color
)

// Kinds lists every effect in display order.
func Kinds() []Kind { return []Kind{Solid, Pulse, Breathe, Flash, Rainbow} }

// Parse validates an effect name. Empty means Solid.
func Parse(s string) (Kind, error) {
	if s == "" {
		return Solid, nil
	}
	for _, k := range Kinds() {
		if string(k) == s {
			return k, nil
		}
	}
	return Solid, fmt.Errorf("unknown effect %q (want one of %v)", s, Kinds())
}

// Next returns the effect after k, wrapping around.
func Next(k Kind) Kind {
	ks := Kinds()
	for i, c := range ks {
		if c == k {
			return ks[(i+1)%len(ks)]
		}
	}
	return Solid
}

// Animated reports whether the effect needs repeated frames.
func (k Kind) Animated() bool { return k != Solid }

// wave maps t onto 0..1 as a sine with the given period.
func wave(t, period time.Duration) float64 {
	return (1 - math.Cos(2*math.Pi*float64(t%period)/float64(period))) / 2
}

func lerp(lo, hi int, f float64) int { return lo + int(math.Round(float64(hi-lo)*f)) }

// Frame returns the light state at t since the effect started. baseBrightness
// is the 1-100 ceiling the effect modulates beneath.
func Frame(k Kind, base color.RGB, baseBrightness int, t time.Duration) govee.State {
	b := baseBrightness
	if b < 1 {
		b = 100
	}
	s := govee.State{On: true, Brightness: b, Color: &base}
	switch k {
	case Pulse:
		s.Brightness = max(1, b*lerp(35, 100, wave(t, 1500*time.Millisecond))/100)
	case Breathe:
		s.Brightness = max(1, b*lerp(8, 100, wave(t, 4*time.Second))/100)
	case Flash:
		if t%(time.Second) >= 500*time.Millisecond {
			s.Brightness = max(1, b*5/100)
		}
	case Rainbow:
		c := hue(float64(t%(12*time.Second)) / float64(12*time.Second))
		s.Color = &c
	}
	return s
}

// hue converts a 0..1 hue at full saturation and value to RGB.
func hue(h float64) color.RGB {
	x := 1 - math.Abs(math.Mod(h*6, 2)-1)
	var r, g, b float64
	switch int(h * 6) {
	case 0:
		r, g, b = 1, x, 0
	case 1:
		r, g, b = x, 1, 0
	case 2:
		r, g, b = 0, 1, x
	case 3:
		r, g, b = 0, x, 1
	case 4:
		r, g, b = x, 0, 1
	default:
		r, g, b = 1, 0, x
	}
	return color.RGB{R: uint8(r * 255), G: uint8(g * 255), B: uint8(b * 255)}
}
