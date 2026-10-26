package pomo

import (
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/state"
)

var t0 = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

func min_(n int) time.Duration { return time.Duration(n) * time.Minute }

// Defaults: 25/5/15, long break after every 4, auto breaks on, auto focus off.
func defaults() Settings {
	s, err := SettingsFrom(config.Default())
	if err != nil {
		panic(err)
	}
	return s
}

func must(t *testing.T) func(state.Pomo, error) state.Pomo {
	return func(p state.Pomo, err error) state.Pomo {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
}

func TestSettingsFromConfig(t *testing.T) {
	s := defaults()
	if s.Focus != min_(25) || s.Short != min_(5) || s.Long != min_(15) || s.LongEvery != 4 || !s.AutoBreaks || s.AutoFocus {
		t.Fatalf("%+v", s)
	}
	bad := config.Default()
	bad.Pomo.Focus = "soon"
	if _, err := SettingsFrom(bad); err == nil {
		t.Fatal("want error")
	}
	bad = config.Default()
	bad.Pomo.LongEvery = 0
	if _, err := SettingsFrom(bad); err == nil {
		t.Fatal("want error")
	}
}

func TestStartFocusAndRemaining(t *testing.T) {
	s := defaults()
	p := must(t)(Start(Stop(state.Pomo{}), t0, s))
	if p.Phase != Focus || p.State != Running || !p.EndsAt.Equal(t0.Add(min_(25))) {
		t.Fatalf("%+v", p)
	}
	st := Evaluate(p, t0.Add(min_(7)), s)
	if st.Remaining != min_(18) || st.Total != min_(25) || st.Dots() != 0 {
		t.Fatalf("%+v", st)
	}
	if got := st.String(); got != "focus · 18:00 left" {
		t.Fatal(got)
	}
	if _, err := Start(p, t0.Add(time.Minute), s); err == nil {
		t.Fatal("starting a running timer is an error")
	}
}

// The full cycle: 4 focus blocks with 3 short breaks and a long break, each
// phase starting exactly where the previous ended.
func TestFullCycleAndLongBreakCadence(t *testing.T) {
	s := defaults()
	s.AutoFocus = true // continuous, so every boundary is exact
	p := must(t)(Start(state.Pomo{}, t0, s))
	at := t0
	want := []struct {
		phase string
		dur   time.Duration
	}{
		{Focus, min_(25)}, {ShortBreak, min_(5)},
		{Focus, min_(25)}, {ShortBreak, min_(5)},
		{Focus, min_(25)}, {ShortBreak, min_(5)},
		{Focus, min_(25)}, {LongBreak, min_(15)},
	}
	for i, w := range want {
		if p.Phase != w.phase || p.State != Running || !p.StartedAt.Equal(at) || !p.EndsAt.Equal(at.Add(w.dur)) {
			t.Fatalf("step %d: want %s %v from %v, got %+v", i, w.phase, w.dur, at, p)
		}
		at = at.Add(w.dur)
		// Look at it one second after it ends: it must have moved on without drift.
		p = Advance(p, at.Add(time.Second), s)
		if i < len(want)-1 && p.Phase == w.phase && p.State == Running {
			t.Fatalf("step %d did not advance", i)
		}
	}
	// After the long break the next focus block has started on its own, exactly
	// at the boundary, with all four blocks credited.
	if p.Phase != Focus || p.State != Running || p.CompletedFocus != 4 || !p.StartedAt.Equal(at) {
		t.Fatalf("%+v", p)
	}
	if st := Evaluate(p, at.Add(time.Minute), s); st.Dots() != 0 || st.Remaining != min_(24) {
		t.Fatalf("a new cycle starts with empty dots: %+v", st)
	}
	// The 5th block is followed by a short break; the long one comes after the 8th.
	p = Advance(p, p.EndsAt.Add(time.Second), s)
	if p.Phase != ShortBreak || p.CompletedFocus != 5 {
		t.Fatalf("%+v", p)
	}
}

// The default config (auto_start_focus = false) stops after each break and
// waits for the user, which is when the light flashes.
func TestDefaultsWaitForFocusAfterBreak(t *testing.T) {
	s := defaults()
	p := must(t)(Start(state.Pomo{}, t0, s))
	p = Advance(p, t0.Add(min_(25)+min_(5)+time.Second), s)
	if p.State != Waiting || p.Phase != ShortBreak || p.Next != Focus || p.CompletedFocus != 1 ||
		!p.WaitingSince.Equal(t0.Add(min_(30))) {
		t.Fatalf("%+v", p)
	}
	st := Evaluate(p, t0.Add(min_(40)), s)
	if st.State != Waiting || st.Next != Focus || st.Total != min_(25) || st.Remaining != 0 || !st.WaitedAt.Equal(t0.Add(min_(30))) {
		t.Fatalf("%+v", st)
	}
	if got := st.String(); got != "short break finished · start the focus when ready" {
		t.Fatal(got)
	}
	// Long break after the 4th block, then waiting again.
	q := p
	for i := 0; i < 3; i++ {
		q = must(t)(Start(q, q.WaitingSince.Add(time.Minute), s))
		q = Advance(q, q.EndsAt.Add(time.Second), s) // focus -> auto break
		q = Advance(q, q.EndsAt.Add(time.Second), s) // break -> waiting
	}
	if q.CompletedFocus != 4 || q.Phase != LongBreak || q.State != Waiting {
		t.Fatalf("%+v", q)
	}
}

func TestWaitingAfterFocusWhenAutoBreaksOff(t *testing.T) {
	s := defaults()
	s.AutoBreaks = false
	p := must(t)(Start(state.Pomo{}, t0, s))
	p = Advance(p, t0.Add(min_(26)), s)
	if p.State != Waiting || p.Phase != Focus || p.Next != ShortBreak || p.CompletedFocus != 1 {
		t.Fatalf("%+v", p)
	}
	if got := Evaluate(p, t0.Add(min_(26)), s).String(); got != "focus finished · start the short break when ready" {
		t.Fatal(got)
	}
	p = must(t)(Start(p, t0.Add(min_(30)), s))
	if p.Phase != ShortBreak || !p.StartedAt.Equal(t0.Add(min_(30))) {
		t.Fatalf("a manual start begins at the keypress, not the old boundary: %+v", p)
	}
}

func TestAutoStartFocusChainsAndCatchesUpAfterSleep(t *testing.T) {
	s := defaults()
	s.AutoFocus = true
	p := must(t)(Start(state.Pomo{}, t0, s))
	// The machine slept for 3 hours. Timeline in minutes from the start:
	// blocks 1-4 with breaks 5,5,5 and a 15 min long break end at 130; then
	// focus 130-155 (5th), break 155-160, focus 160-185 (6th), break 185-190.
	p = Advance(p, t0.Add(3*time.Hour), s)
	if p.Phase != Focus || p.CompletedFocus != 5 || !p.StartedAt.Equal(t0.Add(160*time.Minute)) {
		t.Fatalf("%+v", p)
	}
	// 3h10m = minute 190, exactly the end of that break: the 7th focus block
	// starts right there (no drift, however late we looked).
	at := t0.Add(190 * time.Minute)
	p = Advance(p, at, s)
	if st := Evaluate(p, at, s); st.Phase != Focus || st.Remaining != min_(25) || p.CompletedFocus != 6 || !p.StartedAt.Equal(at) {
		t.Fatalf("%+v %+v", p, st)
	}
	if st := Evaluate(p, at.Add(min_(10)), s); st.Remaining != min_(15) {
		t.Fatalf("%+v", st)
	}
	// Catch-up through a long break: 4 focus blocks -> long break lands right.
	q := must(t)(Start(state.Pomo{}, t0, s))
	q = Advance(q, t0.Add(25*time.Minute*4+5*time.Minute*3+time.Minute), s)
	if q.Phase != LongBreak || q.CompletedFocus != 4 {
		t.Fatalf("%+v", q)
	}
}

func TestAdvanceIsIdempotentAndLeavesFutureAlone(t *testing.T) {
	s := defaults()
	p := must(t)(Start(state.Pomo{}, t0, s))
	if a := Advance(p, t0.Add(min_(10)), s); !Same(a, p) {
		t.Fatal("nothing has ended yet; state must be unchanged")
	}
	a := Advance(p, t0.Add(min_(26)), s)
	if b := Advance(a, t0.Add(min_(26)), s); !Same(a, b) {
		t.Fatal("advancing twice must equal advancing once")
	}
	if !Same(Advance(state.Pomo{}, t0, s), state.Pomo{}) {
		t.Fatal("idle stays idle")
	}
}

func TestPauseResumeKeepsRemainingTime(t *testing.T) {
	s := defaults()
	p := must(t)(Start(state.Pomo{}, t0, s))
	p = must(t)(Pause(p, t0.Add(min_(10)), s))
	if p.State != Paused || p.PausedRemaining != (min_(15)).Milliseconds() || !p.EndsAt.IsZero() {
		t.Fatalf("%+v", p)
	}
	// Hours pass while paused: nothing advances.
	late := t0.Add(5 * time.Hour)
	if st := Evaluate(p, late, s); st.Remaining != min_(15) || st.State != Paused {
		t.Fatalf("%+v", st)
	}
	if got := Describe(p, late, s); got != "focus paused · 15:00 left" {
		t.Fatal(got)
	}
	p = must(t)(Resume(p, late, s))
	if p.State != Running || !p.EndsAt.Equal(late.Add(min_(15))) || p.PausedRemaining != 0 {
		t.Fatalf("%+v", p)
	}
	if _, err := Resume(p, late, s); err == nil {
		t.Fatal("resume of a running timer is an error")
	}
	if _, err := Pause(state.Pomo{}, t0, s); err == nil {
		t.Fatal("pause of idle is an error")
	}
	if _, err := Start(must(t)(Pause(p, late, s)), late, s); err == nil {
		t.Fatal("start while paused should point at resume")
	}
}

func TestSkip(t *testing.T) {
	s := defaults()
	p := must(t)(Start(state.Pomo{}, t0, s))
	// Skipping focus: straight to a short break, no credit toward the long-break cadence.
	p = must(t)(Skip(p, t0.Add(min_(3)), s))
	if p.Phase != ShortBreak || p.CompletedFocus != 0 || !p.StartedAt.Equal(t0.Add(min_(3))) {
		t.Fatalf("%+v", p)
	}
	// Skipping a break starts focus.
	p = must(t)(Skip(p, t0.Add(min_(4)), s))
	if p.Phase != Focus || p.State != Running {
		t.Fatalf("%+v", p)
	}
	// Skip while paused works and runs the next phase.
	p = must(t)(Pause(p, t0.Add(min_(5)), s))
	p = must(t)(Skip(p, t0.Add(min_(6)), s))
	if p.Phase != ShortBreak || p.State != Running {
		t.Fatalf("%+v", p)
	}
	// Skip from waiting starts what was waiting.
	w := Advance(must(t)(Start(state.Pomo{}, t0, func() Settings { x := s; x.AutoBreaks = false; return x }())), t0.Add(min_(30)), func() Settings { x := s; x.AutoBreaks = false; return x }())
	w = must(t)(Skip(w, t0.Add(min_(31)), s))
	if w.Phase != ShortBreak || w.State != Running {
		t.Fatalf("%+v", w)
	}
	if _, err := Skip(state.Pomo{}, t0, s); err == nil {
		t.Fatal("skip on idle is an error")
	}
}

func TestStopResets(t *testing.T) {
	s := defaults()
	p := Advance(must(t)(Start(state.Pomo{}, t0, s)), t0.Add(min_(26)), s)
	if p.CompletedFocus != 1 {
		t.Fatal(p)
	}
	if q := Stop(p); q.Phase != Idle || q.CompletedFocus != 0 || q.State != "" {
		t.Fatalf("%+v", q)
	}
	if got := Describe(Stop(p), t0, s); got != "idle" {
		t.Fatal(got)
	}
}

func TestDotsAndProgress(t *testing.T) {
	cases := []struct {
		completed int
		phase     string
		next      string
		want      int
	}{
		{0, Focus, "", 0}, {1, ShortBreak, "", 1}, {3, Focus, "", 3},
		{4, LongBreak, "", 4},    // long break on: cycle shown complete
		{4, Focus, LongBreak, 4}, // focus 4 finished, waiting to start the long break
		{4, Focus, "", 0},        // next cycle's first block: empty again
		{5, Focus, "", 1}, {8, LongBreak, "", 4},
	}
	for _, c := range cases {
		st := Status{Completed: c.completed, LongEvery: 4, Phase: c.phase, Next: c.next}
		if st.Dots() != c.want {
			t.Errorf("%+v: dots %d, want %d", c, st.Dots(), c.want)
		}
	}
	st := Status{Total: min_(25), Remaining: min_(10)}
	if p := st.Progress(); p < 0.59 || p > 0.61 {
		t.Fatal(p)
	}
	if (Status{}).Progress() != 0 {
		t.Fatal("zero total")
	}
}

func TestClockRoundsUp(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0:00", time.Millisecond: "0:01", 59*time.Second + 100*time.Millisecond: "1:00", min_(25): "25:00", 61 * time.Second: "1:01"} {
		if got := Clock(d); got != want {
			t.Errorf("%v -> %s, want %s", d, got, want)
		}
	}
}

func TestPersistWritesOnlyWhenSomethingMoved(t *testing.T) {
	s := defaults()
	store := state.Open(t.TempDir())
	p := must(t)(Start(state.Pomo{}, t0, s))
	store.Update(func(f *state.File) error { f.Pomo = &p; return nil })
	stat := func() time.Time {
		fi, err := osStat(store.Path())
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}
	before := stat()
	time.Sleep(15 * time.Millisecond)
	if changed, err := Persist(store, t0.Add(min_(1)), s); err != nil || changed {
		t.Fatal(changed, err)
	}
	if !stat().Equal(before) {
		t.Fatal("nothing ended; the file must not be rewritten")
	}
	if changed, err := Persist(store, t0.Add(min_(26)), s); err != nil || !changed {
		t.Fatal(changed, err)
	}
	f, _ := store.Load()
	if f.Pomo.Phase != ShortBreak || f.Pomo.CompletedFocus != 1 {
		t.Fatalf("%+v", f.Pomo)
	}
}
