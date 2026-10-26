package fake

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

func TestApplySkipsRedundant(t *testing.T) {
	l := &Light{}
	c := color.RGB{R: 255, G: 140}
	s := govee.State{On: true, Brightness: 80, Color: &c}
	for i := 0; i < 2; i++ {
		if err := l.Apply(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.Log(); len(got) != 3 {
		t.Fatalf("want 3 commands, got %v", got)
	}
}

func TestCapsRefuseBeforeSending(t *testing.T) {
	l := New(Devices()[1], nil) // white-only
	c := color.RGB{R: 1}
	err := l.Apply(context.Background(), govee.State{On: true, Brightness: 50, Color: &c})
	if !errors.Is(err, govee.ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
	if len(l.Log()) != 0 {
		t.Fatalf("nothing should be sent, got %v", l.Log())
	}
}

func TestKelvinAndBrightnessClamp(t *testing.T) {
	l := New(Devices()[1], nil)
	if err := l.Apply(context.Background(), govee.State{On: true, Kelvin: 9000, Brightness: 1}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(l.Log(), ","); got != "power true,kelvin 6500,brightness 5" {
		t.Fatal(got)
	}
}

func TestSegmentBatchingAndDiffing(t *testing.T) {
	l := New(Devices()[0], nil)
	a, b := color.RGB{R: 255}, color.RGB{B: 255}
	var segs []govee.Segment
	for i := 0; i < 10; i++ {
		c := a
		if i >= 5 {
			c = b
		}
		segs = append(segs, govee.Segment{Index: i, Color: c})
	}
	ctx := context.Background()
	if err := l.SetSegments(ctx, segs); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(l.Log(), ","); got != "segments 0-4 #FF0000,segments 5-9 #0000FF" {
		t.Fatal(got)
	}
	// Moving the boundary by one segment sends exactly one command for one segment.
	segs[4].Color = b
	before := len(l.Log())
	l.SetSegments(ctx, segs)
	if got := l.Log()[before:]; len(got) != 1 || got[0] != "segments 4 #0000FF" {
		t.Fatal(got)
	}
	// A whole-strip color after segments comes first in Apply, so segments survive.
	l2 := New(Devices()[0], nil)
	white := color.RGB{R: 255, G: 255, B: 255}
	l2.Apply(ctx, govee.State{On: true, Color: &white, Segments: segs[:2]})
	if got := strings.Join(l2.Log(), ","); got != "power true,color #FFFFFF,segments 0-1 #FF0000" {
		t.Fatal(got)
	}
	if err := l.SetSegments(ctx, []govee.Segment{{Index: 10}}); err == nil {
		t.Fatal("index 10 is out of range for 10 segments")
	}
	if err := New(Devices()[1], nil).SetSegments(ctx, segs[:1]); !errors.Is(err, govee.ErrUnsupported) {
		t.Fatal("white-only device has no segments")
	}
}
