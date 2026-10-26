package engine

import (
	"fmt"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/effect"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

// Env is what BuildInputs needs besides config and state.
type Env struct {
	// Schedule is the active schedule profile, or nil for none.
	Schedule *schedule.Profile
	// Caps are the capabilities of the device being driven. They decide, for
	// example, whether the pomodoro shows a segment bar or a solid color. The
	// zero value (no segments) is a safe default.
	Caps govee.Capabilities
}

// BuildInputs assembles Resolve's inputs from config and the state file.
// Config is expected to have passed Validate.
func BuildInputs(cfg config.Config, f state.File, now time.Time, env Env) (Inputs, error) {
	prof := env.Schedule
	pal := cfg.UserPalette()
	layer := func(role, hex, eff string) (Layer, error) {
		c, err := color.Parse(hex, pal)
		if err != nil {
			return Layer{}, fmt.Errorf("%s: %w", role, err)
		}
		k, err := effect.Parse(eff)
		if err != nil {
			return Layer{}, fmt.Errorf("%s: %w", role, err)
		}
		return Layer{State: govee.State{On: true, Brightness: cfg.Claude.Brightness, Color: &c}, Effect: k}, nil
	}

	in := Inputs{Idle: Idle{Action: cfg.Idle.Action}, Caps: env.Caps}
	if cfg.Idle.Action == "color" {
		c, err := color.Parse(cfg.Idle.Color, pal)
		if err != nil {
			return in, fmt.Errorf("idle.color: %w", err)
		}
		in.Idle.Layer = Layer{State: govee.State{On: true, Color: &c}}
	}
	if o := f.Override; o != nil {
		in.Override = &Override{Layer: Layer{State: o.State}, Expires: o.ExpiresAt}
	}

	if f.Pomo != nil {
		ps, err := pomo.SettingsFrom(cfg)
		if err != nil {
			return in, err
		}
		if in.Pomo, err = pomoInput(cfg, pomo.Evaluate(*f.Pomo, now, ps), pal, env.Caps); err != nil {
			return in, err
		}
	}

	if prof != nil {
		info, err := prof.At(now, pal)
		switch {
		case err != nil:
			in.ScheduleErr = err
		default:
			if c := info.Current; c != nil {
				in.Schedule = &Schedule{Layer: Layer{State: c.State}, Reason: "schedule: " + c.Label()}
			}
			if n := info.Next; n != nil {
				in.ScheduleNext = n.Starts
			}
		}
	}

	ttl, err := time.ParseDuration(cfg.Claude.SessionTTL)
	if err != nil {
		return in, fmt.Errorf("claude.session_ttl: %w", err)
	}
	hold, err := time.ParseDuration(cfg.Claude.ReadyHold)
	if err != nil {
		return in, fmt.Errorf("claude.ready_hold: %w", err)
	}
	v := claude.Current(f.Claude, now, ttl)
	in.Claude = Claude{Mode: cfg.Claude.Mode, SplitSegments: cfg.Claude.SplitSegments, Status: v.Status, Changed: v.Changed, ReadyHold: hold,
		Layers: map[string]Layer{}}
	for status, src := range map[string]struct{ hex, eff string }{
		claude.Working: {cfg.Claude.Working, cfg.Effects.ClaudeWorking},
		claude.Ready:   {cfg.Claude.Ready, cfg.Effects.ClaudeReady},
		claude.Waiting: {cfg.Claude.Waiting, cfg.Effects.ClaudeWaiting},
	} {
		l, err := layer("claude."+status, src.hex, src.eff)
		if err != nil {
			return in, err
		}
		in.Claude.Layers[status] = l
	}
	return in, nil
}

// NextWake is the earliest time-driven reason to re-evaluate: an engine
// boundary (override expiry, ready hold, accent end) or a Claude session
// going stale.
func NextWake(cfg config.Config, f state.File, now time.Time, in Inputs) (time.Time, bool) {
	best, ok := NextChange(now, in)
	if ttl, err := time.ParseDuration(cfg.Claude.SessionTTL); err == nil {
		if e, has := claude.NextExpiry(f.Claude, now, ttl); has && (!ok || e.Before(best)) {
			best, ok = e, true
		}
	}
	return best, ok
}

// pausedBrightness is how dim a paused (or waiting) pomodoro looks.
const pausedBrightness = pomo.PausedBrightness

// pomoInput turns the timer's status into a layer, or nil when idle:
// running shows the phase color; paused shows it at 20%; waiting shows the
// color of what a keypress will start, at 20%, and flashes when the previous
// phase has just ended.
func pomoInput(cfg config.Config, st pomo.Status, pal color.Palette, caps govee.Capabilities) (*Pomo, error) {
	if !st.Active() {
		return nil, nil
	}
	shown := st.Phase
	if st.State == pomo.Waiting {
		shown = st.Next
	}
	hex, eff := map[string][2]string{
		pomo.Focus:      {cfg.Pomo.FocusColor, cfg.Effects.PomoFocus},
		pomo.ShortBreak: {cfg.Pomo.ShortBreakColor, cfg.Effects.PomoShortBreak},
		pomo.LongBreak:  {cfg.Pomo.LongBreakColor, cfg.Effects.PomoLongBreak},
	}[shown][0], map[string]string{
		pomo.Focus: cfg.Effects.PomoFocus, pomo.ShortBreak: cfg.Effects.PomoShortBreak, pomo.LongBreak: cfg.Effects.PomoLongBreak,
	}[shown]
	c, err := color.Parse(hex, pal)
	if err != nil {
		return nil, fmt.Errorf("pomo.%s_color: %w", shown, err)
	}
	k, err := effect.Parse(eff)
	if err != nil {
		return nil, fmt.Errorf("effects.pomo_%s: %w", shown, err)
	}
	p := &Pomo{Layer: Layer{State: govee.State{On: true, Brightness: 100, Color: &c}, Effect: k}, Reason: "pomodoro: " + st.String()}
	switch st.State {
	case pomo.Running:
		p.Boundary = st.Boundary
	case pomo.Paused:
		p.Layer.State.Brightness = pausedBrightness
	case pomo.Waiting:
		p.Layer.State.Brightness = pausedBrightness
		if cfg.Pomo.FlashOnPhaseEnd {
			p.FlashSince = st.WaitedAt
		}
	}
	// On a segmented device the running or paused timer is a progress bar
	// instead of a solid color. The whole strip stays at full brightness and
	// the dimming lives in the segments, so paused just dims the bar.
	if _, n, ok := BarLayout(cfg, caps); ok && (st.State == pomo.Running || st.State == pomo.Paused) {
		dir := cfg.Pomo.ProgressDirection
		p.Layer.State.Brightness = 100
		p.Layer.State.Segments = BarSegments(cfg, caps, st, c)
		if next, ok := st.NextBarChange(n, dir); ok {
			p.SegmentNext = next
		}
	}
	return p, nil
}

// BarLayout says where the pomodoro bar lives: n segments starting at
// offset. In Claude split mode the first claude.split_segments segments are
// Claude's, and are kept out of the bar whether or not a session is active, so
// the bar doesn't reflow when one starts. ok is false when the pomodoro should
// be a solid color instead.
func BarLayout(cfg config.Config, caps govee.Capabilities) (offset, n int, ok bool) {
	if cfg.Pomo.Progress != "bar" || !caps.Segments || caps.SegmentCount <= 0 {
		return 0, 0, false
	}
	n = caps.SegmentCount
	if k := cfg.Claude.SplitSegments; cfg.Claude.Mode == "split" && k >= 1 && n > k {
		offset, n = k, n-k
	}
	return offset, n, true
}

// BarSegments is the complete per-segment state for the pomodoro on a
// segmented device: the bar itself, shifted past any segments Claude
// reserves, preceded by those reserved segments painted in the phase color.
// Painting them matters: when a Claude session ends, its color must be
// replaced, and the change-only sender only repaints segments whose wanted
// state changed.
func BarSegments(cfg config.Config, caps govee.Capabilities, st pomo.Status, c color.RGB) []govee.Segment {
	off, n, ok := BarLayout(cfg, caps)
	if !ok {
		return nil
	}
	segs := st.BarSegments(n, cfg.Pomo.ProgressDirection, cfg.Pomo.ProgressDim, c, caps.SegBrightness)
	for i := range segs {
		segs[i].Index += off
	}
	lit := 100
	if st.State == pomo.Paused {
		lit = pomo.PausedBrightness
	}
	if !caps.SegBrightness {
		lit = 0
	}
	reserved := make([]govee.Segment, off)
	for i := range reserved {
		reserved[i] = govee.Segment{Index: i, Color: c, Brightness: lit}
	}
	return append(reserved, segs...)
}

// BarOK reports whether the pomodoro is drawn as a segment bar on a device
// with these capabilities.
func BarOK(cfg config.Config, caps govee.Capabilities) bool {
	_, _, ok := BarLayout(cfg, caps)
	return ok
}
