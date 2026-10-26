package engine

import (
	"fmt"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/effect"
	"github.com/adnair2024/beacon/internal/govee"
)

// Source names the layer that won.
type Source string

const (
	SourceNone     Source = "none" // nobody wants the light: leave it alone
	SourceOverride Source = "override"
	SourceClaude   Source = "claude"
	SourcePomo     Source = "pomo"
	SourceSchedule Source = "schedule"
	SourceIdle     Source = "idle"
)

// AccentDuration is how long accent mode shows a Claude transition.
const AccentDuration = 2 * time.Second

// Layer is a state a mode wants, with the effect to animate it.
type Layer struct {
	State  govee.State
	Effect effect.Kind
}

// Override is a manual `beacon set`. Zero Expires means until cleared.
type Override struct {
	Layer   Layer
	Expires time.Time
}

// Claude is the aggregate over all sessions (waiting > working > ready).
type Claude struct {
	Mode string // "override" | "accent" | "split" | "off"
	// SplitSegments is how many segments Claude owns in split mode.
	SplitSegments int
	Status        string // "" (no sessions) | "working" | "waiting" | "ready"
	Changed       time.Time
	ReadyHold     time.Duration
	Layers        map[string]Layer // keyed by Status
}

// Idle is the lowest layer.
type Idle struct {
	Action string // "leave" | "off" | "color"
	Layer  Layer  // used when Action == "color"
}

// Pomo is the pomodoro layer: the phase's look, plus what the engine needs to
// know about time.
type Pomo struct {
	Layer Layer
	// Boundary is when a running phase ends (zero when paused or waiting), so
	// a loop can wake exactly then.
	Boundary time.Time
	// FlashSince is when a phase ended and is waiting on the user; the light
	// flashes FlashCount times from then. Zero disables the flash.
	FlashSince time.Time
	// SegmentNext is when the progress bar next changes by a segment (zero if
	// it isn't a bar or isn't running), so the loop sends one update then.
	SegmentNext time.Time
	// Reason is shown on the dashboard, e.g. "pomodoro: focus 18:42 left".
	Reason string
}

// Split-mode "waiting" pulse: Claude's segments alternate between full and
// PulseDim every PulseStep, for PulseDuration after the prompt appears, so a
// permission prompt is noticeable even when only part of the strip is Claude's.
// It stops after a minute to save API calls.
const (
	PulseStep     = 2 * time.Second
	PulseDuration = 60 * time.Second
	PulseDim      = 30
)

// Flash timing. Steps are longer than the cloud backend's batching window so
// every on/off step is sent rather than merged away.
const (
	FlashStep  = 700 * time.Millisecond
	FlashCount = 3
	flashDim   = 10 // brightness % of the "off" half of a flash
)

func (p *Pomo) flashEnd() time.Time {
	return p.FlashSince.Add(time.Duration(2*FlashCount) * FlashStep)
}

// Schedule is the block in force from the time-of-day schedule.
type Schedule struct {
	Layer  Layer
	Reason string // e.g. "schedule: Focus (09:30)"
}

// Inputs is everything Resolve looks at. nil layers don't participate.
type Inputs struct {
	Override *Override
	Claude   Claude
	Pomo     *Pomo
	Schedule *Schedule
	// ScheduleNext is when the schedule next changes, even if nothing is in
	// force yet (before the first entry), so a loop can wake for it.
	ScheduleNext time.Time
	// ScheduleErr is a problem with the schedule file or its colors. It is
	// reported, not fatal: a typo in a schedule must not stop Claude status.
	ScheduleErr error
	Idle        Idle
	// Caps are the capabilities of the device being driven. Split mode needs
	// them (segments, segment brightness) and falls back to override without.
	Caps govee.Capabilities
}

// Result is the winning layer. For SourceNone, Layer is meaningless.
type Result struct {
	Source Source
	Layer  Layer
	Reason string // for the dashboard: why this layer won
	// Split is how many segments (from segment 0) Claude owns when it is
	// composed over a lower layer in split mode; 0 for a whole-strip result.
	Split int
	// BelowReason says what the rest of the strip is showing in split mode.
	BelowReason string
}

// splitActive reports whether Claude should be composed over the lower layers
// rather than replacing them: split mode on a device with enough segments.
func splitActive(in Inputs) bool {
	k := in.Claude.SplitSegments
	return in.Claude.Mode == "split" && in.Caps.Segments && k >= 1 && in.Caps.SegmentCount > k
}

// Resolve applies the priority stack, highest first:
// override > claude > pomo > schedule > idle. In split mode Claude doesn't
// take the whole strip: it paints its own segments over whatever the lower
// layers would show.
func Resolve(now time.Time, in Inputs) Result {
	if o := in.Override; o != nil && (o.Expires.IsZero() || now.Before(o.Expires)) {
		why := "manual override until cleared"
		if !o.Expires.IsZero() {
			why = "manual override until " + o.Expires.Format("15:04")
		}
		return Result{Source: SourceOverride, Layer: o.Layer, Reason: why}
	}
	cl, claudeOn := resolveClaude(now, in.Claude)
	if claudeOn && !splitActive(in) {
		return cl
	}
	below := resolveBelow(now, in)
	if claudeOn {
		return composeSplit(now, in, cl, below)
	}
	return below
}

// resolveBelow is the stack under Claude: pomodoro, schedule, idle.
func resolveBelow(now time.Time, in Inputs) Result {
	if p := in.Pomo; p != nil {
		return resolvePomo(now, p)
	}
	if sc := in.Schedule; sc != nil {
		why := sc.Reason
		if why == "" {
			why = "schedule block"
		}
		return Result{Source: SourceSchedule, Layer: sc.Layer, Reason: why}
	}
	switch in.Idle.Action {
	case "off":
		return Result{Source: SourceIdle, Layer: Layer{State: govee.State{}}, Reason: "idle: off"}
	case "color":
		return Result{Source: SourceIdle, Layer: in.Idle.Layer, Reason: "idle: color"}
	}
	return Result{Source: SourceNone, Reason: "nothing active; leaving the light alone"}
}

var black = color.RGB{}

// composeSplit paints Claude's color on segments [0, SplitSegments) over the
// state the lower layers produced. The lower state's whole-strip color goes
// out first and the per-segment commands after it, so the segments survive.
func composeSplit(now time.Time, in Inputs, cl, below Result) Result {
	c, caps := in.Claude, in.Caps
	k := c.SplitSegments
	col := black
	if cl.Layer.State.Color != nil {
		col = *cl.Layer.State.Color
	}
	bri := 0 // 0 = leave brightness alone (the device can't set it per segment)
	if caps.SegBrightness {
		if bri = cl.Layer.State.Brightness; bri == 0 {
			bri = 100
		}
		if c.Status == "waiting" {
			if age := now.Sub(c.Changed); age >= 0 && age < PulseDuration && int(age/PulseStep)%2 == 1 {
				bri = PulseDim
			}
		}
	}
	claudeSegs := make([]govee.Segment, k)
	for i := range claudeSegs {
		claudeSegs[i] = govee.Segment{Index: i, Color: col, Brightness: bri}
	}

	var st govee.State
	switch {
	case below.Source == SourceNone:
		st = govee.State{On: true} // the rest of the strip is left as it is
	case !below.Layer.State.On:
		// The layer below wants the light off (e.g. after the schedule's
		// power-off). Claude still needs its segments on, so turn the strip
		// on and keep everything that isn't Claude's dark.
		st = govee.State{On: true}
		for i := k; i < caps.SegmentCount; i++ {
			dark := govee.Segment{Index: i, Color: black}
			if caps.SegBrightness {
				dark.Brightness = 100
			}
			st.Segments = append(st.Segments, dark)
		}
	default:
		st = below.Layer.State
	}
	// Claude paints over whatever the layer below put on its segments.
	rest := make([]govee.Segment, 0, len(st.Segments))
	for _, s := range st.Segments {
		if s.Index >= k {
			rest = append(rest, s)
		}
	}
	st.Segments = append(claudeSegs, rest...) // claudeSegs is fresh, so nothing is aliased
	return Result{
		Source: SourceClaude, Layer: Layer{State: st}, Split: k,
		Reason: cl.Reason + " (split)", BelowReason: below.Reason,
	}
}

func resolvePomo(now time.Time, p *Pomo) Result {
	why := p.Reason
	if why == "" {
		why = "pomodoro"
	}
	layer := p.Layer
	if !p.FlashSince.IsZero() && !now.Before(p.FlashSince) && now.Before(p.flashEnd()) {
		// Alternate bright/dim; the first half-step is bright so the flash
		// starts with a pulse of the color the moment the phase ends.
		st := layer.State
		if int(now.Sub(p.FlashSince)/FlashStep)%2 == 0 {
			st.Brightness = 100
		} else {
			st.Brightness = flashDim
		}
		layer.State = st
		why += " (flashing)"
	}
	return Result{Source: SourcePomo, Layer: layer, Reason: why}
}

func resolveClaude(now time.Time, c Claude) (Result, bool) {
	layer, ok := c.Layers[c.Status]
	if !ok {
		return Result{}, false
	}
	age := now.Sub(c.Changed)
	switch c.Mode {
	case "override", "split":
		// split needs segment support and is composed in milestone 8; until
		// then it behaves like override, which is also its documented
		// fallback for strips without segments.
		switch c.Status {
		case "working", "waiting":
			return Result{Source: SourceClaude, Layer: layer, Reason: "claude " + c.Status}, true
		case "ready":
			// Show "done" briefly, then hand the light back.
			if age < c.ReadyHold {
				return Result{Source: SourceClaude, Layer: layer, Reason: "claude ready (holding)"}, true
			}
		}
	case "accent":
		// Only the transition is shown, as a pulse; the lower layers stay visible.
		if age >= 0 && age < AccentDuration {
			layer.Effect = effect.Pulse
			return Result{Source: SourceClaude, Layer: layer, Reason: "claude " + c.Status + " (accent)"}, true
		}
	}
	return Result{}, false
}

// NextChange reports the next moment at which Resolve's answer could change
// by the passage of time alone (override expiry, ready hold ending, accent
// ending), so a loop can sleep until then. Changes driven by new inputs, or
// by a schedule boundary or pomo tick, are the caller's to schedule.
func NextChange(now time.Time, in Inputs) (time.Time, bool) {
	var best time.Time
	consider := func(t time.Time) {
		if t.After(now) && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	if o := in.Override; o != nil && !o.Expires.IsZero() {
		consider(o.Expires)
	}
	consider(in.ScheduleNext)
	if p := in.Pomo; p != nil {
		consider(p.Boundary)
		consider(p.SegmentNext)
		if !p.FlashSince.IsZero() && now.Before(p.flashEnd()) {
			if now.Before(p.FlashSince) {
				consider(p.FlashSince)
			} else {
				step := now.Sub(p.FlashSince)/FlashStep + 1
				consider(p.FlashSince.Add(step * FlashStep))
			}
		}
	}
	if splitActive(in) && in.Claude.Status == "waiting" && in.Caps.SegBrightness {
		if age := now.Sub(in.Claude.Changed); age >= 0 && age < PulseDuration {
			consider(in.Claude.Changed.Add((age/PulseStep + 1) * PulseStep))
		}
	}
	switch in.Claude.Mode {
	case "override", "split":
		if in.Claude.Status == "ready" {
			consider(in.Claude.Changed.Add(in.Claude.ReadyHold))
		}
	case "accent":
		if in.Claude.Status != "" {
			consider(in.Claude.Changed.Add(AccentDuration))
		}
	}
	return best, !best.IsZero()
}

func (r Result) String() string { return fmt.Sprintf("%s: %s", r.Source, r.Reason) }
