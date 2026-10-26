package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/fake"
	"github.com/adnair2024/beacon/internal/state"
	"github.com/adnair2024/beacon/internal/tui"
)

func devs() []govee.Device { return fake.Devices() }

func TestPickByNameConfigAndSingle(t *testing.T) {
	l := devs()
	// explicit name, case-insensitive
	if got, err := pick(l, config.Config{}, "SHELF"); err != nil || len(got) != 1 || got[0].SKU != "FAKE-WHITE" {
		t.Fatal(got, err)
	}
	// unknown name lists what's available
	if _, err := pick(l, config.Config{}, "nope"); err == nil || !strings.Contains(err.Error(), "desk, shelf") {
		t.Fatal(err)
	}
	// several devices and nothing configured: must choose
	if _, err := pick(l, config.Config{}, ""); err == nil || !strings.Contains(err.Error(), "--device") {
		t.Fatal(err)
	}
	// single device needs no choice
	if got, err := pick(l[:1], config.Config{}, ""); err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	// configured by id, nickname replaces listed name
	cfg := config.Config{Devices: []config.Device{{Name: "lamp", ID: l[1].ID, Backend: "cloud"}}}
	if got, err := pick(l, cfg, ""); err != nil || got[0].Name != "lamp" || got[0].SKU != "FAKE-WHITE" {
		t.Fatal(got, err)
	}
	if got, err := pick(l, cfg, "lamp"); err != nil || got[0].ID != l[1].ID {
		t.Fatal(got, err)
	}
	// configured device missing from the account
	bad := config.Config{Devices: []config.Device{{Name: "ghost", ID: "ZZ"}}}
	if _, err := pick(l, bad, ""); err == nil {
		t.Fatal("want error for missing configured device")
	}
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("BEACON_CONFIG", t.TempDir()+"/none.toml")
	if os.Getenv("BEACON_STATE_DIR") == "" {
		t.Setenv("BEACON_STATE_DIR", t.TempDir())
	}
	t.Setenv("GOVEE_API_KEY", "")
	var out bytes.Buffer
	root := NewRoot("test")
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestDryRunDevicesList(t *testing.T) {
	out, err := run(t, "devices", "list", "--dry-run")
	if err != nil || !strings.Contains(out, "FAKE-STRIP") || !strings.Contains(out, "segments×10") || !strings.Contains(out, "kelvin(2200-6500)") {
		t.Fatal(out, err)
	}
}

func TestDryRunSetNeedsDeviceWhenSeveral(t *testing.T) {
	if _, err := run(t, "set", "red", "--dry-run"); err == nil {
		t.Fatal("two fake devices and no --device should be an error")
	}
	out, err := run(t, "set", "orange", "--device", "desk", "--brightness", "80", "--dry-run")
	if err != nil || !strings.Contains(out, "dry-run [desk]: color #FF8C00") || !strings.Contains(out, "brightness 80") {
		t.Fatal(out, err)
	}
}

func TestDryRunRefusesColorOnWhiteOnlyDevice(t *testing.T) {
	out, err := run(t, "set", "red", "--device", "shelf", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "not supported") || strings.Contains(out, "dry-run [shelf]") {
		t.Fatal(out, err)
	}
	out, err = run(t, "set", "3000k", "--device", "shelf", "--dry-run")
	if err != nil || !strings.Contains(out, "kelvin 3000") {
		t.Fatal(out, err)
	}
}

func TestDryRunDevicesTest(t *testing.T) {
	out, err := run(t, "devices", "test", "desk", "--dry-run")
	if err != nil || strings.Count(out, "color #") != 3 {
		t.Fatal(out, err)
	}
	// The strip has 10 segments, so one white segment is chased across all of them.
	if strings.Count(out, "#FFFFFF") != 10 || !strings.Contains(out, "segments 0 #FFFFFF") ||
		!strings.Contains(out, "segments 9 #FFFFFF") || !strings.Contains(out, "chased a white segment across all 10 segments") {
		t.Fatal(out)
	}
	if _, err := run(t, "devices", "test", "shelf", "--dry-run"); err == nil {
		t.Fatal("white-only device can't flash colors")
	}
}

func TestRealBackendNeedsKey(t *testing.T) {
	_, err := run(t, "set", "red")
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatal(err)
	}
}

func TestOverrideExpiry(t *testing.T) {
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	if got, err := overrideExpiry(now, nil, "15m"); err != nil || !got.Equal(now.Add(15*time.Minute)) {
		t.Fatal(got, err)
	}
	thirty := 30 * time.Minute
	if got, _ := overrideExpiry(now, &thirty, "15m"); !got.Equal(now.Add(thirty)) {
		t.Fatal(got)
	}
	zero := time.Duration(0)
	if got, _ := overrideExpiry(now, &zero, "15m"); !got.IsZero() {
		t.Fatal("--for 0 means until cleared")
	}
	if _, err := overrideExpiry(now, nil, "soon"); err == nil {
		t.Fatal("want error for bad default")
	}
}

func TestSetRecordsOverrideAndClearRemovesIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BEACON_STATE_DIR", dir)
	// two fake devices and no --device: set must name one... so configure both via --device-free single list
	out, err := run(t, "set", "orange", "--device", "desk", "--dry-run")
	if err != nil {
		t.Fatal(out, err)
	}
	store := state.Open(filepath.Join(dir, "dry-run"))
	if f, _ := store.Load(); f.Override != nil {
		t.Fatal("--device is a one-off and must not record an override")
	}
	if f, _ := store.Load(); f.LastApplied == nil {
		t.Fatal("last_applied should be recorded")
	}
	if _, err := run(t, "set", "red", "--device", "desk", "--for", "5m", "--dry-run"); err == nil {
		t.Fatal("--for with --device should be rejected")
	}
}

func TestOverrideLifecycleWithConfiguredDevices(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(cfgPath, []byte("[[devices]]\nname=\"desk\"\nid=\"FA:KE:00:00:00:00:00:01\"\nbackend=\"cloud\"\n"), 0o600)
	runCfg := func(args ...string) (string, error) {
		t.Setenv("BEACON_CONFIG", cfgPath)
		t.Setenv("BEACON_STATE_DIR", dir)
		t.Setenv("GOVEE_API_KEY", "")
		var out bytes.Buffer
		root := NewRoot("test")
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}
	out, err := runCfg("set", "blue", "--for", "20m", "--dry-run")
	if err != nil || !strings.Contains(out, "override active until") {
		t.Fatal(out, err)
	}
	store := state.Open(filepath.Join(dir, "dry-run"))
	f, _ := store.Load()
	if f.Override == nil || f.Override.ExpiresAt.IsZero() || f.Override.State.Color == nil {
		t.Fatalf("%+v", f.Override)
	}
	out, err = runCfg("clear", "--dry-run")
	if err != nil || !strings.Contains(out, "stays as it is") {
		t.Fatal(out, err)
	}
	if f, _ := store.Load(); f.Override != nil {
		t.Fatal("override should be gone")
	}
	out, err = runCfg("clear", "--dry-run")
	if err != nil || !strings.Contains(out, "no override was active") {
		t.Fatal(out, err)
	}
	// real dir untouched
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err == nil {
		t.Fatal("dry-run must not write the real state file")
	}
}

func TestClearAppliesIdleOff(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(cfgPath, []byte("[idle]\naction=\"off\"\n[[devices]]\nname=\"desk\"\nid=\"FA:KE:00:00:00:00:00:01\"\nbackend=\"cloud\"\n"), 0o600)
	t.Setenv("BEACON_CONFIG", cfgPath)
	t.Setenv("BEACON_STATE_DIR", t.TempDir())
	var out bytes.Buffer
	root := NewRoot("test")
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"clear", "--dry-run"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "now showing: idle") {
		t.Fatal(out.String(), err)
	}
}

func TestInspectRedactsIDs(t *testing.T) {
	out, err := run(t, "devices", "inspect", "desk", "--dry-run")
	if err != nil || strings.Contains(out, "FA:KE:00") || !strings.Contains(out, redactedID) || !strings.Contains(out, "segmentedColorRgb") {
		t.Fatal(out, err)
	}
	out, err = run(t, "devices", "inspect", "desk", "--dry-run", "--no-redact")
	if err != nil || !strings.Contains(out, "FA:KE:00:00:00:00:00:01") {
		t.Fatal(out, err)
	}
}

func TestCacheDevices(t *testing.T) {
	cfg := config.Config{Devices: []config.Device{
		{Name: "desk", ID: fake.Devices()[0].ID},
		{Name: "shelf"}, // matched by name
		{Name: "ghost", ID: "nope"},
	}}
	updated, missing := cacheDevices(&cfg, fake.Devices())
	if len(updated) != 2 || len(missing) != 1 || missing[0] != "ghost" {
		t.Fatal(updated, missing)
	}
	d := cfg.Devices[0]
	if d.SKU != "FAKE-STRIP" || d.Caps == nil || d.Caps.SegmentCount != 10 || cfg.Devices[1].ID != fake.Devices()[1].ID {
		t.Fatalf("%+v", cfg.Devices)
	}
}

func TestRefreshDryRunDoesNotWriteConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(cfgPath, []byte("[[devices]]\nname=\"desk\"\nbackend=\"cloud\"\n"), 0o600)
	t.Setenv("BEACON_CONFIG", cfgPath)
	t.Setenv("BEACON_STATE_DIR", t.TempDir())
	var out bytes.Buffer
	root := NewRoot("test")
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"devices", "refresh", "--dry-run"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "refreshed desk (FAKE-STRIP)") {
		t.Fatal(out.String(), err)
	}
	if b, _ := os.ReadFile(cfgPath); strings.Contains(string(b), "FAKE") {
		t.Fatal("dry-run must not write the config")
	}
}

func TestChooseMode(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		running bool
		berr    error
		terr    error
		mode    tui.Mode
		note    string
	}{
		{false, nil, nil, tui.ModeEngine, ""},
		{true, nil, nil, tui.ModeViewer, "daemon (pid 42)"},
		{true, boom, boom, tui.ModeViewer, "daemon (pid 42)"}, // a daemon wins even if we can't reach the API
		{false, boom, nil, tui.ModeOffline, "boom"},
		{false, nil, boom, tui.ModeOffline, "boom"},
	}
	for i, c := range cases {
		mode, note := chooseMode(c.running, "42", c.berr, c.terr)
		if mode != c.mode || note != c.note {
			t.Errorf("case %d: got %v %q", i, mode, note)
		}
	}
}
