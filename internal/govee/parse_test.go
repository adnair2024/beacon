package govee

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseState(t *testing.T) {
	if s, err := ParseState("3000k", 50, nil); err != nil || s.Kelvin != 3000 || s.Color != nil || s.Brightness != 50 {
		t.Fatal(s, err)
	}
	if s, err := ParseState("off", 50, nil); err != nil || s.On {
		t.Fatal(s, err)
	}
	if s, err := ParseState(" ON ", 50, nil); err != nil || !s.On || s.Color != nil {
		t.Fatal(s, err)
	}
	if s, err := ParseState("#FF8C00", 50, nil); err != nil || s.Color == nil || s.Color.Hex() != "#FF8C00" {
		t.Fatal(s, err)
	}
	if _, err := ParseState("bogus", 50, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestParseSegments(t *testing.T) {
	segs, err := ParseSegments("0-2=#FF8C00, 5=teal@30 ,7-8=rgb(1,2,3)", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range segs {
		got = append(got, fmt.Sprintf("%d:%s@%d", s.Index, s.Color.Hex(), s.Brightness))
	}
	want := "0:#FF8C00@0 1:#FF8C00@0 2:#FF8C00@0 5:#1DE9B6@30 7:#010203@0 8:#010203@0"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	// Later items win.
	segs, _ = ParseSegments("0-3=red,2=blue", nil)
	if segs[2].Color.Hex() != "#2979FF" || segs[3].Color.Hex() != "#FF0000" || len(segs) != 4 {
		t.Fatalf("%+v", segs)
	}
}

func TestParseSegmentsRejectsBadSpecs(t *testing.T) {
	for _, bad := range []string{"", "  ", "0-4", "a-b=red", "4-2=red", "-1=red", "0=nope", "0=red@0", "0=red@101", "0=red@x", "0=red,,1=blue"} {
		if _, err := ParseSegments(bad, nil); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
