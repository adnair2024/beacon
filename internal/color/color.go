package color

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// RGB is an 8-bit-per-channel color.
type RGB struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
}

// Hex returns the color as "#RRGGBB".
func (c RGB) Hex() string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

// Packed returns (r<<16)|(g<<8)|b, the integer form the Govee cloud API uses.
func (c RGB) Packed() int { return int(c.R)<<16 | int(c.G)<<8 | int(c.B) }

// FromPacked is the inverse of Packed. Bits above 24 are ignored.
func FromPacked(v int) RGB { return RGB{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)} }

// Scale returns c with each channel multiplied by pct/100.
func (c RGB) Scale(pct int) RGB {
	f := func(v uint8) uint8 { return uint8(int(v) * pct / 100) }
	return RGB{f(c.R), f(c.G), f(c.B)}
}

// Palette maps lowercase names to colors.
type Palette map[string]RGB

// Default returns the built-in named colors.
func Default() Palette {
	return Palette{
		"red":    {255, 0, 0},
		"green":  {0, 200, 83},
		"blue":   {41, 121, 255},
		"orange": {255, 140, 0},
		"amber":  {255, 179, 0},
		"pink":   {255, 79, 163},
		"teal":   {29, 233, 182},
		"purple": {156, 39, 255},
		"white":  {255, 255, 255},
		"yellow": {255, 214, 0},
		"cyan":   {0, 229, 255},
	}
}

// Parse accepts a palette name, "#RRGGBB", or "rgb(r,g,b)". User entries in
// extra take precedence over built-in names.
func Parse(s string, extra Palette) (RGB, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return RGB{}, fmt.Errorf("empty color")
	}
	if c, ok := extra[s]; ok {
		return c, nil
	}
	if c, ok := Default()[s]; ok {
		return c, nil
	}
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) != 6 {
			return RGB{}, fmt.Errorf("invalid hex color %q: want #RRGGBB", s)
		}
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return RGB{}, fmt.Errorf("invalid hex color %q", s)
		}
		return FromPacked(int(v)), nil
	}
	if strings.HasPrefix(s, "rgb(") && strings.HasSuffix(s, ")") {
		parts := strings.Split(s[4:len(s)-1], ",")
		if len(parts) != 3 {
			return RGB{}, fmt.Errorf("invalid color %q: want rgb(r,g,b)", s)
		}
		var ch [3]uint8
		for i, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 0 || n > 255 {
				return RGB{}, fmt.Errorf("invalid channel %q in %q", p, s)
			}
			ch[i] = uint8(n)
		}
		return RGB{ch[0], ch[1], ch[2]}, nil
	}
	return RGB{}, fmt.Errorf("unknown color %q", s)
}

// FromKelvin approximates a color temperature as RGB (Tanner Helland's
// curve fit, good enough for 1000-40000 K). It's the fallback for lights
// that can't do kelvin natively.
func FromKelvin(k int) RGB {
	t := float64(min(max(k, 1000), 40000)) / 100
	clamp := func(v float64) uint8 { return uint8(math.Round(math.Min(255, math.Max(0, v)))) }
	var r, g, b float64
	if t <= 66 {
		r = 255
		g = 99.4708025861*math.Log(t) - 161.1195681661
	} else {
		r = 329.698727446 * math.Pow(t-60, -0.1332047592)
		g = 288.1221695283 * math.Pow(t-60, -0.0755148492)
	}
	switch {
	case t >= 66:
		b = 255
	case t <= 19:
		b = 0
	default:
		b = 138.5177312231*math.Log(t-10) - 305.0447927307
	}
	return RGB{R: clamp(r), G: clamp(g), B: clamp(b)}
}
