package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/govee"
)

func TestDefaultsValid(t *testing.T) {
	if errs := Default().Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestLoadMissingFile(t *testing.T) {
	c, _, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || c.Claude.Mode != "override" {
		t.Fatal(c, err)
	}
}

func TestLoadOverrideAndWarn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("bogus = 1\n[claude]\nmode = \"accent\"\n"), 0o600)
	c, warns, err := Load(p)
	if err != nil || c.Claude.Mode != "accent" || c.Claude.Ready != "#00C853" || len(warns) != 1 {
		t.Fatal(c.Claude, warns, err)
	}
}

func TestValidateErrors(t *testing.T) {
	c := Default()
	c.Claude.Mode = "x"
	c.Schedule.Profile = "../escape"
	if n := len(c.Validate()); n != 2 {
		t.Fatalf("want 2 errors, got %d", n)
	}
}

func TestMaskKey(t *testing.T) {
	if MaskKey("abcdefgh") != "****efgh" || MaskKey("ab") != "****" {
		t.Fatal("bad mask")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "c.toml")
	c := Default()
	c.Claude.Working = "#123456"
	c.Effects.ClaudeWaiting = "flash"
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, c); err != nil { // second save writes a .bak
		t.Fatal(err)
	}
	got, warns, err := Load(p)
	if err != nil || len(warns) != 0 {
		t.Fatal(warns, err)
	}
	if got.Claude.Working != "#123456" || got.Effects.ClaudeWaiting != "flash" {
		t.Fatal(got.Claude, got.Effects)
	}
	if errs := got.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Fatal(err)
	}
}

func TestBadEffect(t *testing.T) {
	c := Default()
	c.Effects.PomoFocus = "disco"
	if len(c.Validate()) != 1 {
		t.Fatal("want 1 error")
	}
}

func TestCapsCacheRoundTrip(t *testing.T) {
	in := govee.Capabilities{Power: true, RGB: true, Kelvin: &govee.Range{Min: 2000, Max: 9000},
		Brightness: &govee.Range{Min: 1, Max: 100}, Segments: true, SegmentCount: 15, SegBrightness: true}
	c := Default()
	k := CacheOf(in)
	c.Devices = []Device{{Name: "desk", ID: "X", SKU: "S", Backend: "cloud", Caps: &k}}
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, warns, err := Load(p)
	if err != nil || len(warns) != 0 {
		t.Fatal(warns, err)
	}
	out := got.Devices[0].Caps.Capabilities()
	if out.String() != in.String() || !got.Devices[0].SegmentsEnabled() {
		t.Fatalf("%v != %v", out, in)
	}
}

func TestNewFieldsValidated(t *testing.T) {
	c := Default()
	c.Claude.Mode = "split"
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	c.Claude.SplitSegments = 0
	c.Pomo.Progress = "wave"
	c.Pomo.ProgressDirection = "up"
	c.Pomo.ProgressDim = 101
	if n := len(c.Validate()); n != 4 {
		t.Fatalf("want 4 errors, got %d", n)
	}
}

func TestBackendOptions(t *testing.T) {
	c := Default()
	for backend, ok := range map[string]bool{"": true, "cloud": true, "lan": false, "auto": false, "carrier-pigeon": false} {
		c.Devices = []Device{{Name: "d", Backend: backend}}
		errs := c.Validate()
		if (len(errs) == 0) != ok {
			t.Errorf("backend %q: errors %v", backend, errs)
		}
	}
	c.Devices = []Device{{Name: "d", Backend: "lan"}}
	if msg := c.Validate()[0].Error(); !strings.Contains(msg, "dropped") || !strings.Contains(msg, "cloud") {
		t.Fatalf("the error should say why and what to do: %s", msg)
	}
}
