package pomo

import (
	"errors"
	"fmt"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/state"
)

// Phase names, as stored in state.json.
const (
	Idle       = "idle"
	Focus      = "focus"
	ShortBreak = "short_break"
	LongBreak  = "long_break"
)

// Run states within a phase.
const (
	Running = "running"
	Paused  = "paused"
	// Waiting means a phase ended and the next one needs a keypress (or
	// `beacon pomo start`) because auto-start is off for it.
	Waiting = "waiting"
)

// Settings are the cadence rules, from config.
type Settings struct {
	Focus, Short, Long time.Duration
	LongEvery          int
	AutoBreaks         bool
	AutoFocus          bool
}

// SettingsFrom reads and validates the pomo section of the config.
func SettingsFrom(cfg config.Config) (Settings, error) {
	p := cfg.Pomo
	s := Settings{LongEvery: p.LongEvery, AutoBreaks: p.AutoStartBreaks, AutoFocus: p.AutoStartFocus}
	for _, d := range []struct {
		name string
		src  string
		dst  *time.Duration
	}{{"pomo.focus", p.Focus, &s.Focus}, {"pomo.short_break", p.ShortBreak, &s.Short}, {"pomo.long_break", p.LongBreak, &s.Long}} {
		v, err := time.ParseDuration(d.src)
		if err != nil || v <= 0 {
			return s, fmt.Errorf("%s: invalid duration %q", d.name, d.src)
		}
		*d.dst = v
	}
	if s.LongEvery < 1 {
		return s, errors.New("pomo.long_every: must be >= 1")
	}
	return s, nil
}

// Duration is how long a phase lasts.
func (s Settings) Duration(phase string) time.Duration {
	switch phase {
	case Focus:
		return s.Focus
	case ShortBreak:
		return s.Short
	case LongBreak:
		return s.Long
	}
	return 0
}

// maxCatchUp bounds Advance when the clock jumped a long way with auto-start
// on for everything (days of 25-minute phases); beyond it we just stop.
const maxCatchUp = 10000

func begin(p state.Pomo, phase string, at time.Time, s Settings) state.Pomo {
	p.Phase, p.State = phase, Running
	p.StartedAt, p.EndsAt = at, at.Add(s.Duration(phase))
	p.PausedRemaining, p.Next, p.WaitingSince = 0, "", time.Time{}
	return p
}

func idle() state.Pomo { return state.Pomo{Phase: Idle} }

// breakAfterFocus applies the long-break cadence to a count that already
// includes the focus block that just finished.
func breakAfterFocus(completed int, s Settings) string {
	if completed%s.LongEvery == 0 {
		return LongBreak
	}
	return ShortBreak
}

// Advance catches the state up to now: every phase that has ended since it
// was last looked at is completed in order, each starting exactly where the
// previous one ended (so the schedule doesn't drift, however late we look).
// It's idempotent.
func Advance(p state.Pomo, now time.Time, s Settings) state.Pomo {
	for i := 0; i < maxCatchUp; i++ {
		if p.Phase == Idle || p.Phase == "" || p.State != Running || now.Before(p.EndsAt) {
			return p
		}
		end := p.EndsAt
		var next string
		var auto bool
		if p.Phase == Focus {
			p.CompletedFocus++
			next, auto = breakAfterFocus(p.CompletedFocus, s), s.AutoBreaks
		} else {
			next, auto = Focus, s.AutoFocus
		}
		if auto {
			p = begin(p, next, end, s)
			continue
		}
		p.State, p.Next, p.WaitingSince = Waiting, next, end
		p.StartedAt, p.EndsAt, p.PausedRemaining = time.Time{}, time.Time{}, 0
		return p // Phase stays the one that just finished; Next is what a keypress starts
	}
	return p
}

// Start begins a focus block from idle, or starts the phase a waiting timer
// is holding. Starting a running or paused timer is an error.
func Start(p state.Pomo, now time.Time, s Settings) (state.Pomo, error) {
	p = Advance(p, now, s)
	switch {
	case p.Phase == Idle || p.Phase == "":
		return begin(idle(), Focus, now, s), nil
	case p.State == Waiting:
		return begin(p, p.Next, now, s), nil
	case p.State == Paused:
		return p, errors.New("the timer is paused; use resume")
	}
	return p, fmt.Errorf("already running (%s)", Describe(p, now, s))
}

// Pause freezes a running phase with its remaining time.
func Pause(p state.Pomo, now time.Time, s Settings) (state.Pomo, error) {
	p = Advance(p, now, s)
	if p.State != Running {
		return p, errors.New("nothing is running")
	}
	p.State = Paused
	p.PausedRemaining = max(0, p.EndsAt.Sub(now).Milliseconds())
	p.EndsAt = time.Time{}
	return p, nil
}

// Resume continues a paused phase for exactly the time that was left.
func Resume(p state.Pomo, now time.Time, s Settings) (state.Pomo, error) {
	if p.State != Paused {
		return p, errors.New("the timer isn't paused")
	}
	p.State = Running
	p.EndsAt = now.Add(time.Duration(p.PausedRemaining) * time.Millisecond)
	p.PausedRemaining = 0
	return p, nil
}

// Skip ends the current phase early and starts the next one running. Skipping
// a focus block earns no credit toward the long-break cadence.
func Skip(p state.Pomo, now time.Time, s Settings) (state.Pomo, error) {
	p = Advance(p, now, s)
	switch {
	case p.Phase == Idle || p.Phase == "":
		return p, errors.New("nothing is running")
	case p.State == Waiting:
		return begin(p, p.Next, now, s), nil
	case p.Phase == Focus:
		return begin(p, ShortBreak, now, s), nil
	}
	return begin(p, Focus, now, s), nil
}

// Stop abandons the session.
func Stop(state.Pomo) state.Pomo { return idle() }

// Status is a derived, display-oriented view of the state at one instant.
type Status struct {
	Phase     string
	State     string
	Remaining time.Duration // running/paused; 0 when waiting or idle
	Total     time.Duration
	Completed int // focus blocks completed this session
	LongEvery int
	Next      string    // waiting: what starts on the next keypress
	WaitedAt  time.Time // waiting: when the previous phase ended
	Boundary  time.Time // running: when the phase ends
}

// Progress is the fraction of the phase elapsed, 0-1.
func (st Status) Progress() float64 {
	if st.Total <= 0 {
		return 0
	}
	return min(1, max(0, 1-float64(st.Remaining)/float64(st.Total)))
}

// Dots is how many focus blocks of the current cycle are done. A finished
// cycle shows full only while its long break is on (or waiting to start);
// the next focus block starts an empty one.
func (st Status) Dots() int {
	if st.LongEvery < 1 {
		return 0
	}
	n := st.Completed % st.LongEvery
	if n == 0 && st.Completed > 0 && (st.Phase == LongBreak || st.Next == LongBreak) {
		return st.LongEvery
	}
	return n
}

// Active reports whether a session is in progress (running, paused or waiting).
func (st Status) Active() bool { return st.Phase != Idle && st.Phase != "" }

// Evaluate describes p at now, catching up first.
func Evaluate(p state.Pomo, now time.Time, s Settings) Status {
	p = Advance(p, now, s)
	st := Status{Phase: p.Phase, State: p.State, Completed: p.CompletedFocus, LongEvery: s.LongEvery}
	if st.Phase == "" {
		st.Phase = Idle
	}
	st.Total = s.Duration(p.Phase)
	switch p.State {
	case Running:
		st.Remaining = max(0, p.EndsAt.Sub(now))
		st.Boundary = p.EndsAt
	case Paused:
		st.Remaining = time.Duration(p.PausedRemaining) * time.Millisecond
	case Waiting:
		st.Next, st.WaitedAt = p.Next, p.WaitingSince
		st.Total = s.Duration(p.Next)
	}
	return st
}

// Describe is a one-line human summary.
func Describe(p state.Pomo, now time.Time, s Settings) string {
	return Evaluate(p, now, s).String()
}

func label(phase string) string {
	switch phase {
	case ShortBreak:
		return "short break"
	case LongBreak:
		return "long break"
	}
	return phase
}

func clock(d time.Duration) string {
	secs := int((d + 999*time.Millisecond) / time.Second) // round up: 0:00 means done
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// Clock formats a remaining duration as M:SS, rounding up.
func Clock(d time.Duration) string { return clock(d) }

// Label is a phase's display name.
func Label(phase string) string { return label(phase) }

func (st Status) String() string {
	switch {
	case !st.Active():
		return "idle"
	case st.State == Waiting:
		return fmt.Sprintf("%s finished · start the %s when ready", label(st.Phase), label(st.Next))
	case st.State == Paused:
		return fmt.Sprintf("%s paused · %s left", label(st.Phase), clock(st.Remaining))
	}
	return fmt.Sprintf("%s · %s left", label(st.Phase), clock(st.Remaining))
}

// Load returns the persisted pomo state, treating nil as idle.
func Load(f state.File) state.Pomo {
	if f.Pomo == nil {
		return idle()
	}
	return *f.Pomo
}

// Persist catches the stored state up to now (completing phases that ended
// while nobody was looking) and writes it back only if that changed anything.
func Persist(store *state.Store, now time.Time, s Settings) (changed bool, err error) {
	f, err := store.Load()
	if err != nil {
		return false, err
	}
	if f.Pomo == nil || Same(Advance(*f.Pomo, now, s), *f.Pomo) {
		return false, nil
	}
	err = store.Update(func(f *state.File) error {
		if f.Pomo != nil { // re-run under the lock; another process may have moved it
			adv := Advance(*f.Pomo, now, s)
			f.Pomo = &adv
		}
		return nil
	})
	return err == nil, err
}

// Same reports whether two states are identical (time.Time values compared
// by instant, not by representation).
func Same(a, b state.Pomo) bool {
	return a.Phase == b.Phase && a.State == b.State && a.Next == b.Next &&
		a.CompletedFocus == b.CompletedFocus && a.PausedRemaining == b.PausedRemaining &&
		a.StartedAt.Equal(b.StartedAt) && a.EndsAt.Equal(b.EndsAt) && a.WaitingSince.Equal(b.WaitingSince)
}

// Verb is a user-triggered transition, shared by the CLI and the TUI.
type Verb struct {
	Name, Short string
	Apply       func(state.Pomo, time.Time, Settings) (state.Pomo, error)
}

// Verbs lists every transition in display order.
var Verbs = []Verb{
	{"start", "Start a focus block (or the phase the timer is waiting to start)", Start},
	{"pause", "Pause the running phase", Pause},
	{"resume", "Resume a paused phase", Resume},
	{"skip", "End the current phase early and start the next", Skip},
	{"stop", "Abandon the session", func(p state.Pomo, _ time.Time, _ Settings) (state.Pomo, error) { return Stop(p), nil }},
}

// Do runs a verb under the state lock, so concurrent callers (CLI, TUI,
// daemon) can't interleave, and an invalid transition writes nothing. It
// returns the resulting status.
func Do(store *state.Store, name string, now time.Time, s Settings) (Status, error) {
	var v *Verb
	for i := range Verbs {
		if Verbs[i].Name == name {
			v = &Verbs[i]
		}
	}
	if v == nil {
		return Status{}, fmt.Errorf("unknown pomo command %q", name)
	}
	var st Status
	err := store.Update(func(f *state.File) error {
		next, err := v.Apply(Load(*f), now, s)
		if err != nil {
			return err
		}
		f.Pomo = &next
		st = Evaluate(next, now, s)
		return nil
	})
	return st, err
}

// Toggle is what the space bar does: start when idle or waiting, pause when
// running, resume when paused.
func (st Status) Toggle() string {
	switch {
	case st.State == Running:
		return "pause"
	case st.State == Paused:
		return "resume"
	}
	return "start"
}
