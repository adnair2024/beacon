package cli

import (
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/fake"
)

func TestSetSegmentsDryRun(t *testing.T) {
	e := newEnv(t, "")
	// With the desk configured, no --device is needed and an override is recorded.
	out, errOut, err := e.run("", "set", "segments", "0-4=#FF0000,5-9=#0000FF@30", "--dry-run")
	if err != nil {
		t.Fatal(err, errOut)
	}
	for _, want := range []string{"dry-run [desk]: power true", "segments 0-4 #FF0000", "segments 5-9 #0000FF", "override active until"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	f := e.dryState()
	if f.Override == nil || len(f.Override.State.Segments) != 10 || f.Override.State.Segments[7].Brightness != 30 {
		t.Fatalf("the segments should be stored in the override: %+v", f.Override)
	}
	// Clearing hands the light back.
	if out, _, err := e.run("", "clear", "--dry-run"); err != nil || !strings.Contains(out, "stays as it is") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestSetSegmentsErrors(t *testing.T) {
	e := newEnv(t, "")
	_, _, err := e.run("", "set", "segments", "0-12=red", "--device", "desk", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "out of range 0-9") {
		t.Fatalf("the range check uses the device's own segment count: %v", err)
	}
	for _, bad := range []string{"", "0-4", "0=nope", "4-2=red"} {
		if _, _, err := e.run("", "set", "segments", bad, "--device", "desk", "--dry-run"); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	// A white-only light can't show a colored segment at all, so it's refused
	// with a clear message and nothing is sent to it.
	out, _, err := e.run("", "set", "segments", "0-4=red", "--device", "shelf", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "not supported") || strings.Contains(out, "dry-run [shelf]:") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestSetColorStillWorksAlongsideTheSubcommand(t *testing.T) {
	e := newEnv(t, "")
	out, _, err := e.run("", "set", "orange", "--device", "desk", "--dry-run")
	if err != nil || !strings.Contains(out, "color #FF8C00") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestWithDeviceConfig(t *testing.T) {
	base := fake.Devices()[0] // 10 segments, with brightness
	off := false
	cases := []struct {
		name    string
		cfg     config.Device
		count   int
		segs    bool
		segBri  bool
		reverse bool
	}{
		{"defaults", config.Device{}, 10, true, true, false},
		{"reverse", config.Device{Segments: config.DeviceSegments{Reverse: true}}, 10, true, true, true},
		{"count override", config.Device{Segments: config.DeviceSegments{Count: 8}}, 8, true, true, false},
		{"disabled", config.Device{Segments: config.DeviceSegments{Enabled: &off, Reverse: true}}, 0, false, false, false},
	}
	for _, c := range cases {
		d := withDeviceConfig(base, c.cfg)
		if d.Caps.SegmentCount != c.count || d.Caps.Segments != c.segs || d.Caps.SegBrightness != c.segBri || d.SegReverse != c.reverse {
			t.Errorf("%s: %+v reverse=%v", c.name, d.Caps, d.SegReverse)
		}
	}
	// A device with no segments never gets segment settings forced on it.
	white := fake.Devices()[1]
	if d := withDeviceConfig(white, config.Device{Segments: config.DeviceSegments{Count: 5, Reverse: true}}); d.Caps.Segments || d.SegReverse {
		t.Fatal("config can't conjure segments the API didn't advertise")
	}
	_ = govee.Device{}
}

func TestCachedTargetsApplySegmentConfig(t *testing.T) {
	caps := config.CacheOf(fake.StripCaps())
	cfg := config.Config{Devices: []config.Device{{
		Name: "desk", ID: "X", SKU: "S", Backend: "cloud", Caps: &caps,
		Segments: config.DeviceSegments{Reverse: true, Count: 8},
	}}}
	devs, ok := cachedTargets(cfg)
	if !ok || !devs[0].SegReverse || devs[0].Caps.SegmentCount != 8 {
		t.Fatalf("%+v %v", devs, ok)
	}
}
