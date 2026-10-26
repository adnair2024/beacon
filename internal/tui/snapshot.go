package tui

import (
	"fmt"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/engine"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

// snapshot is everything the dashboard needs at one instant. It's computed
// the same way the engine computes it (BuildInputs + Resolve), so a viewer
// sees exactly what the driving process is showing.
type snapshot struct {
	At     time.Time
	File   state.File
	In     engine.Inputs
	Res    engine.Result
	Claude claude.View
	Err    error
	// Schedule: the active profile and what it says now. SchedErr is a problem
	// with the file; the rest of the dashboard still works.
	Prof        *schedule.Profile
	SchedExists bool
	SchedErr    error
	Sched       schedule.Info
	// Show is the state to draw: the winning layer, or, if nobody wants the
	// light, whatever was last applied (the light is being left alone).
	Show   govee.State
	Placed bool // Show came from LastApplied, not a live layer
}

func takeSnapshot(cfg config.Config, store *state.Store, schedDir string, caps govee.Capabilities, now time.Time) snapshot {
	s := snapshot{At: now}
	f, err := store.Load()
	if err != nil {
		s.Err = fmt.Errorf("state: %w", err)
		return s
	}
	s.File = f
	if schedDir != "" {
		p, exists, err := schedule.Load(schedDir, cfg.Schedule.Profile)
		if err != nil {
			s.SchedErr = err
		} else {
			s.Prof, s.SchedExists = &p, exists
			s.Sched, _ = p.At(now, cfg.UserPalette())
		}
	}
	in, err := engine.BuildInputs(cfg, f, now, engine.Env{Schedule: s.Prof, Caps: caps})
	if err != nil {
		s.Err = err
		return s
	}
	s.In = in
	if s.SchedErr == nil {
		s.SchedErr = in.ScheduleErr
	}
	s.Res = engine.Resolve(now, in)
	if ttl, err := time.ParseDuration(cfg.Claude.SessionTTL); err == nil {
		s.Claude = claude.Current(f.Claude, now, ttl)
	}
	switch {
	case s.Res.Source != engine.SourceNone:
		s.Show = s.Res.Layer.State
	case f.LastApplied != nil:
		s.Show, s.Placed = *f.LastApplied, true
	}
	return s
}

// accentHex is the color the UI accent should track.
func (s snapshot) accentHex() string {
	if !s.Show.On {
		return "#8A8A8A"
	}
	st := s.Show
	st.Brightness = 0 // the accent shouldn't dim with the lamp
	return cellsFor(st, govee.Capabilities{})[0].c.Hex()
}
