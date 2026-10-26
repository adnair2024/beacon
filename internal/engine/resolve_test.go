package engine

import (
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/effect"
	"github.com/adnair2024/beacon/internal/govee"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func solid(r, g, b uint8) Layer {
	c := color.RGB{R: r, G: g, B: b}
	return Layer{State: govee.State{On: true, Brightness: 100, Color: &c}}
}

var (
	ovr   = solid(1, 0, 0)
	work  = solid(2, 0, 0)
	wait  = solid(3, 0, 0)
	ready = solid(4, 0, 0)
	pomoL = solid(5, 0, 0)
	sched = solid(6, 0, 0)
	idleC = solid(7, 0, 0)
)

func claudeIn(mode, status string, changed time.Time) Claude {
	return Claude{Mode: mode, Status: status, Changed: changed, ReadyHold: 10 * time.Second,
		Layers: map[string]Layer{"working": work, "waiting": wait, "ready": ready}}
}

func red(l Layer) uint8 { return l.State.Color.R }

func TestPriorityStack(t *testing.T) {
	all := Inputs{
		Override: &Override{Layer: ovr},
		Claude:   claudeIn("override", "working", t0),
		Pomo:     &Pomo{Layer: pomoL}, Schedule: &Schedule{Layer: sched},
		Idle: Idle{Action: "color", Layer: idleC},
	}
	cases := []struct {
		name   string
		mutate func(*Inputs)
		src    Source
		red    uint8
	}{
		{"everything: override wins", func(*Inputs) {}, SourceOverride, 1},
		{"no override: claude wins", func(i *Inputs) { i.Override = nil }, SourceClaude, 2},
		{"claude waiting", func(i *Inputs) { i.Override = nil; i.Claude = claudeIn("override", "waiting", t0) }, SourceClaude, 3},
		{"claude off: pomoL", func(i *Inputs) { i.Override = nil; i.Claude.Mode = "off" }, SourcePomo, 5},
		{"no claude sessions: pomoL", func(i *Inputs) { i.Override = nil; i.Claude = Claude{Mode: "override"} }, SourcePomo, 5},
		{"no pomoL: schedule", func(i *Inputs) { i.Override = nil; i.Claude.Mode = "off"; i.Pomo = nil }, SourceSchedule, 6},
		{"no schedule: idle color", func(i *Inputs) { i.Override = nil; i.Claude.Mode = "off"; i.Pomo, i.Schedule = nil, nil }, SourceIdle, 7},
	}
	for _, c := range cases {
		in := all
		in.Claude = claudeIn("override", "working", t0)
		c.mutate(&in)
		r := Resolve(t0.Add(time.Second), in)
		if r.Source != c.src || (c.src != SourceNone && red(r.Layer) != c.red) {
			t.Errorf("%s: got %v (red %d)", c.name, r, red(r.Layer))
		}
	}
}

func TestIdleActions(t *testing.T) {
	if r := Resolve(t0, Inputs{Idle: Idle{Action: "leave"}}); r.Source != SourceNone {
		t.Fatal(r)
	}
	if r := Resolve(t0, Inputs{}); r.Source != SourceNone {
		t.Fatal("zero Inputs should leave the light alone", r)
	}
	if r := Resolve(t0, Inputs{Idle: Idle{Action: "off"}}); r.Source != SourceIdle || r.Layer.State.On {
		t.Fatal(r)
	}
}

func TestOverrideExpiry(t *testing.T) {
	exp := t0.Add(15 * time.Minute)
	in := Inputs{Override: &Override{Layer: ovr, Expires: exp}, Schedule: &Schedule{Layer: sched}}
	if r := Resolve(exp.Add(-time.Second), in); r.Source != SourceOverride {
		t.Fatal("before expiry", r)
	}
	if r := Resolve(exp, in); r.Source != SourceSchedule {
		t.Fatal("at expiry the override is gone", r)
	}
	forever := Inputs{Override: &Override{Layer: ovr}, Schedule: &Schedule{Layer: sched}}
	if r := Resolve(t0.Add(1000*time.Hour), forever); r.Source != SourceOverride {
		t.Fatal("zero Expires means until cleared", r)
	}
}

func TestReadyHold(t *testing.T) {
	in := Inputs{Claude: claudeIn("override", "ready", t0), Schedule: &Schedule{Layer: sched}}
	if r := Resolve(t0.Add(9*time.Second), in); r.Source != SourceClaude || red(r.Layer) != 4 {
		t.Fatal("within hold", r)
	}
	if r := Resolve(t0.Add(10*time.Second), in); r.Source != SourceSchedule {
		t.Fatal("after hold it yields", r)
	}
}

func TestAccentMode(t *testing.T) {
	in := Inputs{Claude: claudeIn("accent", "waiting", t0), Pomo: &Pomo{Layer: pomoL}}
	r := Resolve(t0.Add(time.Second), in)
	if r.Source != SourceClaude || red(r.Layer) != 3 || r.Layer.Effect != effect.Pulse {
		t.Fatal("transition should pulse the claude color", r)
	}
	if r := Resolve(t0.Add(3*time.Second), in); r.Source != SourcePomo {
		t.Fatal("after the accent window the lower layer returns", r)
	}
	// A long-running 'working' state must NOT hold the light in accent mode.
	long := Inputs{Claude: claudeIn("accent", "working", t0), Pomo: &Pomo{Layer: pomoL}}
	if r := Resolve(t0.Add(time.Hour), long); r.Source != SourcePomo {
		t.Fatal(r)
	}
}

func TestClaudeWorkingHasNoHoldTimeout(t *testing.T) {
	in := Inputs{Claude: claudeIn("override", "working", t0), Pomo: &Pomo{Layer: pomoL}}
	if r := Resolve(t0.Add(3*time.Hour), in); r.Source != SourceClaude {
		t.Fatal("working persists until the status changes (TTL pruning is the state layer's job)", r)
	}
}

func TestUnknownStatusIgnored(t *testing.T) {
	in := Inputs{Claude: claudeIn("override", "bogus", t0), Schedule: &Schedule{Layer: sched}}
	if r := Resolve(t0, in); r.Source != SourceSchedule {
		t.Fatal(r)
	}
}

func TestNextChange(t *testing.T) {
	exp := t0.Add(5 * time.Minute)
	in := Inputs{Override: &Override{Layer: ovr, Expires: exp}, Claude: claudeIn("override", "ready", t0)}
	if at, ok := NextChange(t0.Add(time.Second), in); !ok || !at.Equal(t0.Add(10*time.Second)) {
		t.Fatal("ready hold ends first", at, ok)
	}
	if at, ok := NextChange(t0.Add(time.Minute), in); !ok || !at.Equal(exp) {
		t.Fatal("then the override expiry", at, ok)
	}
	if _, ok := NextChange(exp.Add(time.Hour), in); ok {
		t.Fatal("nothing left to wait for")
	}
	acc := Inputs{Claude: claudeIn("accent", "ready", t0)}
	if at, _ := NextChange(t0, acc); !at.Equal(t0.Add(AccentDuration)) {
		t.Fatal(at)
	}
}

func TestResolveIsPure(t *testing.T) {
	in := Inputs{Override: &Override{Layer: ovr}, Pomo: &Pomo{Layer: pomoL}}
	a, b := Resolve(t0, in), Resolve(t0, in)
	if a.Source != b.Source || red(a.Layer) != red(b.Layer) {
		t.Fatal("same inputs, different answers")
	}
}
