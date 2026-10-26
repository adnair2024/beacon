package color

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want RGB
		bad  bool
	}{
		{"#FF8C00", RGB{255, 140, 0}, false},
		{"orange", RGB{255, 140, 0}, false},
		{"rgb(1, 2, 3)", RGB{1, 2, 3}, false},
		{"rgb(1,2,300)", RGB{}, true},
		{"#12345", RGB{}, true},
		{"nope", RGB{}, true},
		{"", RGB{}, true},
	}
	for _, c := range cases {
		got, err := Parse(c.in, nil)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("Parse(%q) = %v, %v", c.in, got, err)
		}
	}
}

func TestPacked(t *testing.T) {
	cases := []struct {
		c RGB
		v int
	}{
		{RGB{0, 0, 0}, 0},
		{RGB{255, 255, 255}, 16777215},
		{RGB{255, 0, 0}, 16711680},
		{RGB{0, 255, 0}, 65280},
		{RGB{0, 0, 255}, 255},
		{RGB{255, 140, 0}, 16747520}, // orange: #FF8C00
	}
	for _, c := range cases {
		if c.c.Packed() != c.v || FromPacked(c.v) != c.c {
			t.Errorf("%v <-> %d", c.c, c.v)
		}
	}
	for n, c := range Default() {
		if FromPacked(c.Packed()) != c {
			t.Errorf("palette %s does not round-trip", n)
		}
	}
}

func TestFromKelvin(t *testing.T) {
	warm, cool := FromKelvin(2000), FromKelvin(9000)
	if warm.R != 255 || warm.B >= warm.R/2 || cool.B != 255 || cool.R >= 255 {
		t.Fatalf("warm %v cool %v", warm, cool)
	}
	if mid := FromKelvin(6600); mid.R < 250 || mid.G < 250 || mid.B < 250 {
		t.Fatalf("6600K should be near white, got %v", mid)
	}
	if FromKelvin(-5) != FromKelvin(1000) {
		t.Fatal("out-of-range input should clamp")
	}
}

func TestUserPaletteWins(t *testing.T) {
	got, _ := Parse("amber", Palette{"amber": {1, 2, 3}})
	if got != (RGB{1, 2, 3}) {
		t.Fatalf("got %v", got)
	}
}
