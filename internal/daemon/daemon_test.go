package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/fake"
	"github.com/adnair2024/beacon/internal/logging"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

type harness struct {
	store  *state.Store
	light  *fake.Light
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, cfg config.Config) *harness { return startWith(t, cfg, "") }

// startWith also runs the schedule from schedDir ("workday.json"), watched for edits.
func startWith(t *testing.T, cfg config.Config, schedDir string) *harness {
	t.Helper()
	h := &harness{store: state.Open(t.TempDir()), light: fake.New(govee.Device{}, nil), done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	opts := Options{
		Store: h.store, Light: h.light, Log: logging.Discard(),
		LoadConfig: func() (config.Config, error) { return cfg, nil },
	}
	if schedDir != "" {
		opts.ScheduleDir = schedDir
		opts.LoadSchedule = func() (*schedule.Profile, error) {
			p, _, err := schedule.Load(schedDir, "workday")
			if err != nil {
				return nil, err
			}
			return &p, nil
		}
	}
	go func() { h.done <- Run(ctx, opts) }()
	t.Cleanup(func() { cancel(); <-h.done })
	waitFor(t, "engine lock", func() bool { return h.store.EngineRunning() })
	return h
}

func (h *harness) hook(t *testing.T, ev claude.Event, sid string) {
	t.Helper()
	err := h.store.Update(func(f *state.File) error {
		claude.Record(&f.Claude, ev, claude.Input{SessionID: sid}, time.Now(), 30*time.Minute)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	waitUpTo(t, 3*time.Second, what, ok)
}

func waitUpTo(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (h *harness) sawCmd(cmd string) func() bool {
	return func() bool {
		for _, c := range h.light.Log() {
			if c == cmd {
				return true
			}
		}
		return false
	}
}

func TestHooksDriveTheLight(t *testing.T) {
	h := start(t, config.Default())
	h.hook(t, claude.EvWorking, "a")
	waitFor(t, "orange", h.sawCmd("color #FF8C00"))
	h.hook(t, claude.EvWaiting, "a")
	waitFor(t, "blue", h.sawCmd("color #2979FF"))
	h.hook(t, claude.EvReady, "a")
	waitFor(t, "green", h.sawCmd("color #00C853"))

	// Nothing but the changes: power once, then three colors, brightness once.
	got := strings.Join(h.light.Log(), ",")
	if got != "power true,color #FF8C00,brightness 100,color #2979FF,color #00C853" {
		t.Fatalf("commands: %s", got)
	}
}

func TestRedundantEventsSendNothing(t *testing.T) {
	h := start(t, config.Default())
	h.hook(t, claude.EvWorking, "a")
	waitFor(t, "orange", h.sawCmd("color #FF8C00"))
	n := len(h.light.Log())
	h.hook(t, claude.EvWorking, "a") // another prompt, same state
	h.hook(t, claude.EvWorking, "b")
	time.Sleep(150 * time.Millisecond)
	if len(h.light.Log()) != n {
		t.Fatalf("extra commands: %v", h.light.Log()[n:])
	}
}

func TestOverrideExpiryRevertsWithoutAnyEvent(t *testing.T) {
	cfg := config.Default()
	cfg.Idle.Action = "off" // so the layer below the override is visible
	h := start(t, cfg)
	err := h.store.Update(func(f *state.File) error {
		col := fakeColor()
		f.Override = &state.Override{State: govee.State{On: true, Brightness: 100, Color: &col},
			ExpiresAt: time.Now().Add(300 * time.Millisecond)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "override color", h.sawCmd("color #010203"))
	waitFor(t, "idle off after expiry", h.sawCmd("power false"))
}

func TestStaleSessionIsPrunedAndDropsOut(t *testing.T) {
	cfg := config.Default()
	cfg.Claude.SessionTTL = "300ms"
	cfg.Idle.Action = "off"
	h := start(t, cfg)
	h.hook(t, claude.EvWorking, "crashed") // recorded with the real 30m ttl, but...
	err := h.store.Update(func(f *state.File) error {
		s := f.Claude.Sessions["crashed"]
		s.Updated = time.Now() // fresh now; goes stale in 300ms per this config
		f.Claude.Sessions["crashed"] = s
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "orange", h.sawCmd("color #FF8C00"))
	waitFor(t, "idle off once the session expires", h.sawCmd("power false"))
	waitFor(t, "session pruned from the file", func() bool {
		f, _ := h.store.Load()
		return len(f.Claude.Sessions) == 0
	})
}

func TestSecondEngineIsRefused(t *testing.T) {
	h := start(t, config.Default())
	err := Run(context.Background(), Options{Store: h.store, Light: h.light, Log: logging.Discard(),
		LoadConfig: func() (config.Config, error) { return config.Default(), nil }})
	if !errors.Is(err, state.ErrEngineRunning) {
		t.Fatalf("got %v", err)
	}
}

func TestShutdownReleasesLockAndFlushesCoalescer(t *testing.T) {
	store := state.Open(t.TempDir())
	inner := fake.New(govee.Device{}, nil)
	co := govee.NewCoalescer(inner, time.Hour, nil) // would never fire on its own
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Store: store, Light: co, Log: logging.Discard(),
			LoadConfig: func() (config.Config, error) { return config.Default(), nil }})
	}()
	waitFor(t, "engine up", store.EngineRunning)
	store.Update(func(f *state.File) error {
		claude.Record(&f.Claude, claude.EvWorking, claude.Input{SessionID: "a"}, time.Now(), time.Hour)
		return nil
	})
	time.Sleep(200 * time.Millisecond) // let it be queued in the coalescer
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if store.EngineRunning() {
		t.Fatal("lock should be released on shutdown")
	}
	if got := strings.Join(inner.Log(), ","); !strings.Contains(got, "color #FF8C00") {
		t.Fatalf("final state should be flushed on shutdown, got %q", got)
	}
}

func TestFailedApplyIsRetried(t *testing.T) {
	store := state.Open(t.TempDir())
	flaky := &flakyLight{Light: fake.New(govee.Device{}, nil), failures: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Store: store, Light: flaky, Log: logging.Discard(),
			LoadConfig: func() (config.Config, error) { return config.Default(), nil }})
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "engine up", store.EngineRunning)
	store.Update(func(f *state.File) error {
		claude.Record(&f.Claude, claude.EvWorking, claude.Input{SessionID: "a"}, time.Now(), time.Hour)
		return nil
	})
	deadline := time.Now().Add(8 * time.Second) // retryWake is 5s
	for time.Now().Before(deadline) {
		if got := strings.Join(flaky.Light.(*fake.Light).Log(), ","); strings.Contains(got, "color #FF8C00") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("a failed apply should be retried")
}

type flakyLight struct {
	govee.Light
	failures int
}

func (f *flakyLight) Apply(ctx context.Context, s govee.State) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("network down")
	}
	return f.Light.Apply(ctx, s)
}

func fakeColor() (c color.RGB) { return color.RGB{R: 1, G: 2, B: 3} }

// Phases complete on their own: no hook, no CLI call, just the wall clock.
func TestPomodoroAdvancesWithNoEventsAndPersists(t *testing.T) {
	cfg := config.Default()
	cfg.Pomo.Focus, cfg.Pomo.ShortBreak = "400ms", "400ms"
	h := start(t, cfg)
	ps, _ := pomo.SettingsFrom(cfg)
	err := h.store.Update(func(f *state.File) error {
		p, err := pomo.Start(state.Pomo{}, time.Now(), ps)
		f.Pomo = &p
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "focus pink", h.sawCmd("color #FF4FA3"))
	waitFor(t, "short break teal (no event nudged the daemon)", h.sawCmd("color #1DE9B6"))
	// Break over with auto_start_focus off: waiting, flashing, then a dim glow.
	waitFor(t, "waiting: focus color again", func() bool {
		n := 0
		for _, c := range h.light.Log() {
			if c == "color #FF4FA3" {
				n++
			}
		}
		return n == 2
	})
	waitFor(t, "a dim flash step", h.sawCmd("brightness 10"))
	waitUpTo(t, 8*time.Second, "settled glow after three flashes (4.2s)", h.sawCmd("brightness 20"))
	waitFor(t, "completed focus block persisted", func() bool {
		f, _ := h.store.Load()
		return f.Pomo != nil && f.Pomo.CompletedFocus == 1 && f.Pomo.State == pomo.Waiting
	})
}

// An entry that is already in force from 00:00 on every day, so the test
// doesn't depend on the time of day it runs.
func allDay(kelvin int) schedule.Profile {
	return schedule.Profile{Days: schedule.AllDays, Entries: []schedule.Entry{
		{At: "00:00", Name: "All day", Kelvin: kelvin, Brightness: 50},
	}}
}

func TestScheduleFileDrivesTheLightAndReloadsLive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "schedules")
	save := func(p schedule.Profile) {
		t.Helper()
		if err := schedule.Save(dir, "workday", p, nil); err != nil {
			t.Fatal(err)
		}
	}
	save(allDay(3000))
	cfg := config.Default()
	cfg.Idle.Action = "off" // so "the schedule let go" is visible
	h := startWith(t, cfg, dir)

	waitFor(t, "schedule applied", h.sawCmd("kelvin 3000"))

	// Edit the file: the watcher picks it up with no other event.
	save(allDay(5000))
	waitFor(t, "edited schedule applied", h.sawCmd("kelvin 5000"))

	// A half-saved file (an editor mid-write) is ignored: the last good
	// schedule stays in force and the daemon keeps running.
	path := filepath.Join(dir, "workday.json")
	if err := os.WriteFile(path, []byte("{ nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	n := len(h.light.Log())
	if got := h.light.Log()[n-1]; got == "power false" {
		t.Fatalf("a broken file must not blank the schedule: %v", h.light.Log())
	}
	save(allDay(4000)) // fixing it recovers
	waitFor(t, "recovered after the broken file", h.sawCmd("kelvin 4000"))

	// Disabling hands the light to the next layer (idle: off).
	p := allDay(4000)
	p.SetEnabled(false)
	save(p)
	waitFor(t, "idle off after the schedule is disabled", h.sawCmd("power false"))

	// Higher layers still win while the schedule is live.
	p.SetEnabled(true)
	save(p)
	waitFor(t, "back on", func() bool {
		log := h.light.Log()
		return len(log) > 0 && log[len(log)-1] != "power false"
	})
	h.hook(t, claude.EvWorking, "a")
	waitFor(t, "claude outranks the schedule", h.sawCmd("color #FF8C00"))
}

// The bar drains with no event from anyone: the daemon wakes at each segment
// boundary and sends exactly one small update.
func TestPomodoroBarDrainsOnItsOwn(t *testing.T) {
	cfg := config.Default()
	cfg.Pomo.Focus = "1s" // ten segments: one boundary every 100 ms
	strip := fake.Devices()[0]
	h := &harness{store: state.Open(t.TempDir()), light: fake.New(strip, nil), done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		h.done <- Run(ctx, Options{
			Store: h.store, Light: h.light, Log: logging.Discard(), Caps: strip.Caps,
			LoadConfig: func() (config.Config, error) { return cfg, nil },
		})
	}()
	t.Cleanup(func() { cancel(); <-h.done })
	waitFor(t, "engine lock", func() bool { return h.store.EngineRunning() })

	ps, _ := pomo.SettingsFrom(cfg)
	if err := h.store.Update(func(f *state.File) error {
		p, err := pomo.Start(state.Pomo{}, time.Now(), ps)
		f.Pomo = &p
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "full bar", h.sawCmd("segment-brightness 0-9 100"))
	// Each later update dims exactly one more segment, from the far end in.
	for _, n := range []int{9, 8, 7, 6} {
		waitFor(t, fmt.Sprintf("segment %d dimmed", n), h.sawCmd(fmt.Sprintf("segment-brightness %d 20", n)))
	}
	log := h.light.Log()
	pos := func(cmd string) int {
		for i, c := range log {
			if c == cmd {
				return i
			}
		}
		return -1
	}
	log = h.light.Log()
	if !(pos("segment-brightness 9 20") < pos("segment-brightness 8 20") && pos("segment-brightness 8 20") < pos("segment-brightness 7 20")) {
		t.Fatalf("segments must go dim in order: %v", log)
	}
	// One update per boundary: never a whole-bar resend while draining.
	count := 0
	for _, c := range log {
		if c == "segment-brightness 0-9 100" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the full bar is sent once, then only deltas: %v", log)
	}
}

// Split mode end to end: a waiting prompt paints Claude's two segments blue
// and pulses them, while the pomodoro bar keeps running on the others.
func TestSplitModeWaitingPulsesWhileTheBarRuns(t *testing.T) {
	cfg := config.Default()
	cfg.Claude.Mode = "split"
	strip := fake.Devices()[0]
	h := &harness{store: state.Open(t.TempDir()), light: fake.New(strip, nil), done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		h.done <- Run(ctx, Options{
			Store: h.store, Light: h.light, Log: logging.Discard(), Caps: strip.Caps,
			LoadConfig: func() (config.Config, error) { return cfg, nil },
		})
	}()
	t.Cleanup(func() { cancel(); <-h.done })
	waitFor(t, "engine lock", func() bool { return h.store.EngineRunning() })

	ps, _ := pomo.SettingsFrom(cfg)
	h.store.Update(func(f *state.File) error {
		p, err := pomo.Start(state.Pomo{}, time.Now(), ps)
		f.Pomo = &p
		return err
	})
	waitFor(t, "pomodoro bar (pink everywhere)", h.sawCmd("segments 0-9 #FF4FA3"))

	h.hook(t, claude.EvWaiting, "a")
	waitFor(t, "Claude's segments turn blue", h.sawCmd("segments 0-1 #2979FF"))
	waitUpTo(t, 6*time.Second, "pulse dims", h.sawCmd("segment-brightness 0-1 30"))
	waitUpTo(t, 6*time.Second, "pulse recovers", func() bool {
		log := h.light.Log()
		dim := -1
		for i, c := range log {
			if c == "segment-brightness 0-1 30" {
				dim = i
			}
		}
		return dim >= 0 && len(log) > dim+1 && log[len(log)-1] == "segment-brightness 0-1 100"
	})

	// Back to ready then the session ends: the segments return to the pomodoro's color.
	h.hook(t, claude.SessionEnd, "a")
	waitFor(t, "Claude's segments return to the phase color", func() bool {
		log := h.light.Log()
		return len(log) > 0 && log[len(log)-1] == "segments 0-1 #FF4FA3"
	})
}
