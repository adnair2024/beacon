package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/effect"
	"github.com/adnair2024/beacon/internal/fsutil"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/schedule"
)

// DeviceSegments tunes segment handling; it only applies if the API
// advertises segment support.
type DeviceSegments struct {
	Enabled *bool `toml:"enabled,omitempty"` // nil means true
	Reverse bool  `toml:"reverse,omitempty"` // segment 0 is on the "wrong" end
	Count   int   `toml:"count,omitempty"`   // overrides a bad parse of the API
}

// CapsCache is the last capabilities read from the API for a device, so
// short-lived commands (hooks) don't need a /user/devices round trip.
// `beacon devices refresh` rewrites it.
type CapsCache struct {
	Power         bool `toml:"power"`
	RGB           bool `toml:"rgb"`
	KelvinMin     int  `toml:"kelvin_min,omitempty"` // 0/0 = no kelvin
	KelvinMax     int  `toml:"kelvin_max,omitempty"`
	BrightMin     int  `toml:"brightness_min,omitempty"` // 0/0 = no brightness
	BrightMax     int  `toml:"brightness_max,omitempty"`
	Segments      bool `toml:"segments,omitempty"`
	SegmentCount  int  `toml:"segment_count,omitempty"`
	SegBrightness bool `toml:"segment_brightness,omitempty"`
}

// CacheOf converts API capabilities to their cached form.
func CacheOf(c govee.Capabilities) CapsCache {
	k := CapsCache{Power: c.Power, RGB: c.RGB, Segments: c.Segments, SegmentCount: c.SegmentCount, SegBrightness: c.SegBrightness}
	if c.Kelvin != nil {
		k.KelvinMin, k.KelvinMax = c.Kelvin.Min, c.Kelvin.Max
	}
	if c.Brightness != nil {
		k.BrightMin, k.BrightMax = c.Brightness.Min, c.Brightness.Max
	}
	return k
}

// Capabilities converts the cache back. Raw is not cached.
func (k CapsCache) Capabilities() govee.Capabilities {
	c := govee.Capabilities{Power: k.Power, RGB: k.RGB, Segments: k.Segments, SegmentCount: k.SegmentCount, SegBrightness: k.SegBrightness}
	if k.KelvinMax > 0 {
		c.Kelvin = &govee.Range{Min: k.KelvinMin, Max: k.KelvinMax}
	}
	if k.BrightMax > 0 {
		c.Brightness = &govee.Range{Min: k.BrightMin, Max: k.BrightMax}
	}
	return c
}

type Device struct {
	Name     string         `toml:"name"`
	ID       string         `toml:"id"`
	SKU      string         `toml:"sku,omitempty"`     // filled by `devices refresh`, never assumed
	Backend  string         `toml:"backend,omitempty"` // "cloud" (the default and only choice)
	Segments DeviceSegments `toml:"segments"`
	Caps     *CapsCache     `toml:"caps,omitempty"`
}

// SegmentsEnabled reports whether segment features are allowed for the device.
func (d Device) SegmentsEnabled() bool { return d.Segments.Enabled == nil || *d.Segments.Enabled }

type Claude struct {
	Mode          string `toml:"mode"`           // override | accent | split | off
	SplitSegments int    `toml:"split_segments"` // split mode: how many segments Claude owns
	Working       string `toml:"working"`
	Ready         string `toml:"ready"`
	Waiting       string `toml:"waiting"`
	ReadyHold     string `toml:"ready_hold"`
	SessionTTL    string `toml:"session_ttl"`
	Brightness    int    `toml:"brightness"`
}

type Pomo struct {
	Focus             string `toml:"focus"`
	ShortBreak        string `toml:"short_break"`
	LongBreak         string `toml:"long_break"`
	LongEvery         int    `toml:"long_every"`
	AutoStartBreaks   bool   `toml:"auto_start_breaks"`
	AutoStartFocus    bool   `toml:"auto_start_focus"`
	FocusColor        string `toml:"focus_color"`
	ShortBreakColor   string `toml:"short_break_color"`
	LongBreakColor    string `toml:"long_break_color"`
	FlashOnPhaseEnd   bool   `toml:"flash_on_phase_end"`
	Progress          string `toml:"progress"`           // bar | solid (bar needs segment support)
	ProgressDirection string `toml:"progress_direction"` // drain | fill
	ProgressDim       int    `toml:"progress_dim"`       // brightness % of unfilled segments
}

// Effects names the effect for each color role (see package effect). Empty
// means solid. Animated effects are only practical on the LAN backend.
type Effects struct {
	ClaudeWorking  string `toml:"claude_working"`
	ClaudeReady    string `toml:"claude_ready"`
	ClaudeWaiting  string `toml:"claude_waiting"`
	PomoFocus      string `toml:"pomo_focus"`
	PomoShortBreak string `toml:"pomo_short_break"`
	PomoLongBreak  string `toml:"pomo_long_break"`
}

type Override struct {
	DefaultDuration string `toml:"default_duration"`
}

type Idle struct {
	Action string `toml:"action"` // leave | off | color
	Color  string `toml:"color"`
}

type TUI struct {
	Theme       string `toml:"theme"` // auto | dark | light
	ShowSeconds bool   `toml:"show_seconds"`
}

// Schedule only names the active schedule. The schedule itself (days,
// timezone, entries) lives in <config dir>/schedules/<profile>.json, which
// the TUI editor rewrites without disturbing this file.
type Schedule struct {
	Profile string `toml:"profile"`
}

type Config struct {
	APIKey   string            `toml:"api_key"`
	Devices  []Device          `toml:"devices"`
	Palette  map[string]string `toml:"palette"`
	Claude   Claude            `toml:"claude"`
	Pomo     Pomo              `toml:"pomo"`
	Effects  Effects           `toml:"effects"`
	Override Override          `toml:"override"`
	Idle     Idle              `toml:"idle"`
	TUI      TUI               `toml:"tui"`
	Schedule Schedule          `toml:"schedule"`
}

func ptr[T any](v T) *T { return &v }

// Default returns the shipped defaults, including Ashwin's workday schedule.
func Default() Config {
	return Config{
		Claude: Claude{Mode: "override", SplitSegments: 2, Working: "#FF8C00", Ready: "#00C853", Waiting: "#2979FF",
			ReadyHold: "10s", SessionTTL: "30m", Brightness: 100},
		Pomo: Pomo{Focus: "25m", ShortBreak: "5m", LongBreak: "15m", LongEvery: 4,
			AutoStartBreaks: true, FocusColor: "#FF4FA3", ShortBreakColor: "#1DE9B6",
			LongBreakColor: "#9C27FF", FlashOnPhaseEnd: true,
			Progress: "bar", ProgressDirection: "drain", ProgressDim: 20},
		Effects: Effects{ClaudeWorking: "solid", ClaudeReady: "solid", ClaudeWaiting: "solid",
			PomoFocus: "solid", PomoShortBreak: "solid", PomoLongBreak: "solid"},
		Override: Override{DefaultDuration: "15m"},
		Idle:     Idle{Action: "leave"},
		TUI:      TUI{Theme: "auto", ShowSeconds: true},
		Schedule: Schedule{Profile: "workday"},
	}
}

// Load reads path over the defaults. A missing file yields the defaults.
// Unknown keys are returned as warnings, not errors.
func Load(path string) (Config, []string, error) {
	cfg := Default()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil, nil
	}
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return cfg, nil, fmt.Errorf("config: %s: %w", path, err)
	}
	var warns []string
	for _, k := range md.Undecoded() {
		warns = append(warns, fmt.Sprintf("unknown key %q", k.String()))
	}
	return cfg, warns, nil
}

// paletteNameRE is what a [palette] key may look like.
var paletteNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Validate returns one error per problem, each naming the field.
func (c Config) Validate() []error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("%s: %s", f, fmt.Sprint(a...))) }
	pal := color.Palette{}
	for n, v := range c.Palette {
		rgb, err := color.Parse(v, nil)
		if err != nil {
			bad("palette."+n, err)
			continue
		}
		pal[n] = rgb
	}
	for n := range c.Palette {
		if !paletteNameRE.MatchString(n) {
			bad("palette."+n, "names use letters, digits, - and _")
		}
	}
	col := func(f, v string) {
		if _, err := color.Parse(v, pal); err != nil {
			bad(f, err)
		}
	}
	dur := func(f, v string) {
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			bad(f, fmt.Sprintf("invalid duration %q", v))
		}
	}
	oneOf := func(f, v string, ok ...string) {
		for _, o := range ok {
			if v == o {
				return
			}
		}
		bad(f, fmt.Sprintf("%q not one of %v", v, ok))
	}
	oneOf("claude.mode", c.Claude.Mode, "override", "accent", "split", "off")
	if c.Claude.SplitSegments < 1 {
		bad("claude.split_segments", "must be >= 1")
	}
	oneOf("pomo.progress", c.Pomo.Progress, "bar", "solid")
	oneOf("pomo.progress_direction", c.Pomo.ProgressDirection, "drain", "fill")
	if c.Pomo.ProgressDim < 0 || c.Pomo.ProgressDim > 100 {
		bad("pomo.progress_dim", "must be 0-100")
	}
	col("claude.working", c.Claude.Working)
	col("claude.ready", c.Claude.Ready)
	col("claude.waiting", c.Claude.Waiting)
	dur("claude.ready_hold", c.Claude.ReadyHold)
	dur("claude.session_ttl", c.Claude.SessionTTL)
	for f, v := range map[string]string{"pomo.focus": c.Pomo.Focus, "pomo.short_break": c.Pomo.ShortBreak,
		"pomo.long_break": c.Pomo.LongBreak, "override.default_duration": c.Override.DefaultDuration} {
		dur(f, v)
	}
	col("pomo.focus_color", c.Pomo.FocusColor)
	col("pomo.short_break_color", c.Pomo.ShortBreakColor)
	col("pomo.long_break_color", c.Pomo.LongBreakColor)
	if c.Pomo.LongEvery < 1 {
		bad("pomo.long_every", "must be >= 1")
	}
	for f, v := range map[string]string{"effects.claude_working": c.Effects.ClaudeWorking,
		"effects.claude_ready": c.Effects.ClaudeReady, "effects.claude_waiting": c.Effects.ClaudeWaiting,
		"effects.pomo_focus": c.Effects.PomoFocus, "effects.pomo_short_break": c.Effects.PomoShortBreak,
		"effects.pomo_long_break": c.Effects.PomoLongBreak} {
		if _, err := effect.Parse(v); err != nil {
			bad(f, err)
		}
	}
	oneOf("idle.action", c.Idle.Action, "leave", "off", "color")
	if c.Idle.Action == "color" {
		col("idle.color", c.Idle.Color)
	}
	oneOf("tui.theme", c.TUI.Theme, "auto", "dark", "light")
	if !schedule.ValidName(c.Schedule.Profile) {
		bad("schedule.profile", fmt.Sprintf("%q is not a valid profile name (letters, digits, - and _)", c.Schedule.Profile))
	}
	for i, d := range c.Devices {
		f := fmt.Sprintf("devices[%d]", i)
		if d.Name == "" {
			bad(f+".name", "required")
		}
		switch d.Backend {
		case "", "cloud":
		case "lan", "auto":
			bad(f+".backend", fmt.Sprintf("%q was dropped: beacon only uses Govee's cloud API. Remove this line or set it to \"cloud\"", d.Backend))
		default:
			bad(f+".backend", fmt.Sprintf("%q is not \"cloud\"", d.Backend))
		}
		if d.Segments.Count < 0 {
			bad(f+".segments.count", "must be >= 0")
		}
	}
	return errs
}

// Key returns the API key, preferring GOVEE_API_KEY over the config file.
func (c Config) Key() string {
	if k := os.Getenv("GOVEE_API_KEY"); k != "" {
		return k
	}
	return c.APIKey
}

// MaskKey renders a key as ****abcd without ever exposing the full value.
func MaskKey(k string) string {
	if len(k) <= 4 {
		return "****"
	}
	return "****" + k[len(k)-4:]
}

// Save writes cfg to path atomically (temp file + rename). TOML encoding
// drops comments, so an existing file is kept as path+".bak". The file is
// created 0600 because it may hold the API key.
func Save(path string, cfg Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: save: %w", err)
	}
	if old, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("config: backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("config: save: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		tmp.Close()
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		tmp.Close()
		return fmt.Errorf("config: save: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: save: %w", err)
	}
	if err := fsutil.Replace(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: save: %w", err)
	}
	return nil
}

// UserPalette returns the [palette] table parsed into colors, keyed by
// lowercase name. Entries that don't parse are skipped (Validate reports them).
func (c Config) UserPalette() color.Palette {
	pal := color.Palette{}
	for n, v := range c.Palette {
		if rgb, err := color.Parse(v, nil); err == nil {
			pal[strings.ToLower(n)] = rgb
		}
	}
	return pal
}
