package engine

import (
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/effect"
	cl "github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/state"
)

func TestBuildInputsFromClaudeSessions(t *testing.T) {
	cfg := config.Default()
	cfg.Effects.ClaudeWaiting = "flash"
	var f state.File
	cl.Record(&f.Claude, cl.EvWorking, cl.Input{SessionID: "a"}, t0, 30*time.Minute)

	in, err := BuildInputs(cfg, f, t0.Add(time.Second), Env{})
	if err != nil {
		t.Fatal(err)
	}
	r := Resolve(t0.Add(time.Second), in)
	if r.Source != SourceClaude || r.Layer.State.Color.Hex() != "#FF8C00" {
		t.Fatal(r)
	}
	if in.Claude.Layers[cl.Waiting].Effect != effect.Flash {
		t.Fatal("effect from config should flow into the layer")
	}
	// The same state a day later: the session is stale, nobody wants the light.
	in, _ = BuildInputs(cfg, f, t0.Add(24*time.Hour), Env{})
	if r := Resolve(t0.Add(24*time.Hour), in); r.Source != SourceNone {
		t.Fatal(r)
	}
}

func TestBuildInputsUsesUserPaletteAndReportsBadConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Palette = map[string]string{"mine": "#010203"}
	cfg.Claude.Ready = "mine"
	var f state.File
	cl.Record(&f.Claude, cl.EvReady, cl.Input{SessionID: "a"}, t0, time.Hour)
	in, err := BuildInputs(cfg, f, t0, Env{})
	if err != nil || in.Claude.Layers["ready"].State.Color.Hex() != "#010203" {
		t.Fatal(in.Claude.Layers["ready"], err)
	}
	cfg.Claude.Working = "not-a-color"
	if _, err := BuildInputs(cfg, f, t0, Env{}); err == nil {
		t.Fatal("bad color should be an error naming the field")
	}
}

func TestNextWakeIncludesSessionExpiry(t *testing.T) {
	cfg := config.Default() // ttl 30m
	var f state.File
	cl.Record(&f.Claude, cl.EvWorking, cl.Input{SessionID: "a"}, t0, 30*time.Minute)
	in, _ := BuildInputs(cfg, f, t0, Env{})
	if at, ok := NextWake(cfg, f, t0, in); !ok || !at.Equal(t0.Add(30*time.Minute)) {
		t.Fatal(at, ok)
	}
}
