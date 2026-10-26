package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

func monday(h, m int) time.Time { return time.Date(2026, 10, 26, h, m, 0, 0, time.UTC) }

func withSchedule(t *testing.T, p schedule.Profile, f state.File, at time.Time) (Result, Inputs) {
	t.Helper()
	in, err := BuildInputs(config.Default(), f, at, Env{Schedule: &p})
	if err != nil {
		t.Fatal(err)
	}
	return Resolve(at, in), in
}

func TestScheduleBlockBecomesALayer(t *testing.T) {
	r, in := withSchedule(t, schedule.Default(), state.File{}, monday(10, 0))
	if r.Source != SourceSchedule || r.Layer.State.Kelvin != 4800 || r.Layer.State.Brightness != 90 {
		t.Fatal(r)
	}
	if r.Reason != "schedule: Focus (09:30)" {
		t.Fatal(r.Reason)
	}
	if next, ok := NextChange(monday(10, 0), in); !ok || !next.Equal(monday(11, 0)) {
		t.Fatalf("the daemon must wake for Lunch: %v %v", next, ok)
	}
}

func TestScheduleSitsBelowEverythingButIdle(t *testing.T) {
	at := monday(10, 0)
	f := state.File{}
	claude.Record(&f.Claude, claude.EvWorking, claude.Input{SessionID: "a"}, at, time.Hour)
	if r, _ := withSchedule(t, schedule.Default(), f, at); r.Source != SourceClaude {
		t.Fatalf("claude outranks the schedule: %v", r)
	}
	f = state.File{Override: &state.Override{}}
	if r, _ := withSchedule(t, schedule.Default(), f, at); r.Source != SourceOverride {
		t.Fatal(r)
	}
	// The pomodoro too.
	cfg, pf := pomoFile(t, nil, nil)
	prof := schedule.Default()
	in, err := BuildInputs(cfg, pf, t0.Add(time.Minute), Env{Schedule: &prof})
	if err != nil {
		t.Fatal(err)
	}
	if r := Resolve(t0.Add(time.Minute), in); r.Source != SourcePomo {
		t.Fatalf("pomodoro outranks the schedule: %v", r)
	}
}

func TestScheduleEndOfDayAssertsOff(t *testing.T) {
	r, _ := withSchedule(t, schedule.Default(), state.File{}, monday(17, 0))
	if r.Source != SourceSchedule || r.Layer.State.On {
		t.Fatalf("after Leave the schedule keeps the light off: %v", r)
	}
}

func TestScheduleBeforeFirstEntryWakesForIt(t *testing.T) {
	r, in := withSchedule(t, schedule.Default(), state.File{}, monday(6, 0))
	if r.Source != SourceNone {
		t.Fatalf("nothing in force before 07:55: %v", r)
	}
	if next, ok := NextChange(monday(6, 0), in); !ok || !next.Equal(monday(7, 55)) {
		t.Fatalf("but the loop must still wake at 07:55: %v %v", next, ok)
	}
}

func TestScheduleDisabledOrAbsent(t *testing.T) {
	p := schedule.Default()
	p.SetEnabled(false)
	if r, _ := withSchedule(t, p, state.File{}, monday(10, 0)); r.Source != SourceNone {
		t.Fatal(r)
	}
	in, _ := BuildInputs(config.Default(), state.File{}, monday(10, 0), Env{})
	if in.Schedule != nil || !in.ScheduleNext.IsZero() {
		t.Fatal("no profile, no schedule layer")
	}
}

// A broken schedule must be reported but must not stop anything else.
func TestBadScheduleDoesNotBreakTheRestOfTheEngine(t *testing.T) {
	p := schedule.Default()
	p.Entries[1].Color, p.Entries[1].Kelvin = "not-a-color", 0
	f := state.File{}
	claude.Record(&f.Claude, claude.EvWorking, claude.Input{SessionID: "a"}, monday(10, 0), time.Hour)
	in, err := BuildInputs(config.Default(), f, monday(10, 0), Env{Schedule: &p})
	if err != nil {
		t.Fatalf("a schedule typo must not be fatal: %v", err)
	}
	if in.ScheduleErr == nil || !strings.Contains(in.ScheduleErr.Error(), "09:30") {
		t.Fatalf("it should be reported and name the entry: %v", in.ScheduleErr)
	}
	if r := Resolve(monday(10, 0), in); r.Source != SourceClaude {
		t.Fatalf("claude keeps working: %v", r)
	}
}

func TestScheduleUsesTheUserPalette(t *testing.T) {
	cfg := config.Default()
	cfg.Palette = map[string]string{"mine": "#010203"}
	p := schedule.Profile{Days: schedule.AllDays, Entries: []schedule.Entry{{At: "08:00", Name: "A", Color: "mine"}}}
	in, err := BuildInputs(cfg, state.File{}, monday(9, 0), Env{Schedule: &p})
	if err != nil || in.ScheduleErr != nil || in.Schedule.Layer.State.Color.Hex() != "#010203" {
		t.Fatal(in.ScheduleErr, err)
	}
}
