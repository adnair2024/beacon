package effect

import (
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
)

var base = color.RGB{R: 255, G: 140}

func TestParse(t *testing.T) {
	if k, err := Parse(""); err != nil || k != Solid {
		t.Fatal(k, err)
	}
	if _, err := Parse("disco"); err == nil {
		t.Fatal("want error")
	}
}

func TestSolidIsConstant(t *testing.T) {
	a := Frame(Solid, base, 80, 0)
	b := Frame(Solid, base, 80, 3*time.Second)
	if a.Brightness != 80 || b.Brightness != 80 || *a.Color != base {
		t.Fatal(a, b)
	}
}

func TestBrightnessStaysInRange(t *testing.T) {
	for _, k := range Kinds() {
		for ms := 0; ms < 20000; ms += 37 {
			s := Frame(k, base, 100, time.Duration(ms)*time.Millisecond)
			if s.Brightness < 1 || s.Brightness > 100 {
				t.Fatalf("%s at %dms: brightness %d", k, ms, s.Brightness)
			}
		}
	}
}

func TestFlashAlternates(t *testing.T) {
	on := Frame(Flash, base, 100, 100*time.Millisecond)
	off := Frame(Flash, base, 100, 700*time.Millisecond)
	if on.Brightness != 100 || off.Brightness != 5 {
		t.Fatal(on.Brightness, off.Brightness)
	}
}

func TestPulsePeriodic(t *testing.T) {
	a := Frame(Pulse, base, 100, 200*time.Millisecond)
	b := Frame(Pulse, base, 100, 1700*time.Millisecond)
	if a.Brightness != b.Brightness {
		t.Fatal(a.Brightness, b.Brightness)
	}
}

func TestRainbowCyclesHue(t *testing.T) {
	a := Frame(Rainbow, base, 100, 0)
	b := Frame(Rainbow, base, 100, 4*time.Second)
	if *a.Color == *b.Color || *a.Color != (color.RGB{R: 255}) {
		t.Fatal(*a.Color, *b.Color)
	}
}

func TestNextWraps(t *testing.T) {
	if Next(Rainbow) != Solid || Next(Solid) != Pulse {
		t.Fatal("bad Next")
	}
}
