package schedule

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// Version is the schema version written to schedule files.
const Version = 1

// Entry sets the light from its time onward. It is state, not an event: if
// beacon starts at 10:00 the 09:30 entry is already in force. Fields left out
// inherit from the previous entry that day.
type Entry struct {
	At         string `json:"at"` // "HH:MM", 24-hour
	Name       string `json:"name,omitempty"`
	Power      *bool  `json:"power,omitempty"`      // false ends the day for this layer
	Color      string `json:"color,omitempty"`      // palette name, #RRGGBB or rgb(r,g,b); excludes kelvin
	Kelvin     int    `json:"kelvin,omitempty"`     // excludes color
	Brightness int    `json:"brightness,omitempty"` // 1-100; 0 inherits
}

// Profile is one schedule file.
type Profile struct {
	Version  int      `json:"version"`
	Name     string   `json:"name,omitempty"`
	Enabled  *bool    `json:"enabled,omitempty"`  // nil means true
	Days     []string `json:"days"`               // mon..sun
	Timezone string   `json:"timezone,omitempty"` // IANA name; empty = the system's
	Entries  []Entry  `json:"entries"`
}

func ptr[T any](v T) *T { return &v }

// IsEnabled reports whether the schedule participates.
func (p Profile) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

// SetEnabled records the flag explicitly.
func (p *Profile) SetEnabled(v bool) { p.Enabled = ptr(v) }

// Default is the shipped workday schedule: arrive around 8, lunch at 11,
// leave around 4, on weekdays, in the system's time zone.
func Default() Profile {
	return Profile{
		Version: Version, Name: "workday", Enabled: ptr(true),
		Days: []string{"mon", "tue", "wed", "thu", "fri"},
		Entries: []Entry{
			{At: "07:55", Name: "Arrive", Power: ptr(true), Kelvin: 2900, Brightness: 60},
			{At: "09:30", Name: "Focus", Kelvin: 4800, Brightness: 90},
			{At: "11:00", Name: "Lunch", Color: "amber", Brightness: 25},
			{At: "12:00", Name: "Back to work", Kelvin: 4800, Brightness: 90},
			{At: "14:30", Name: "Wind down", Kelvin: 3000, Brightness: 70},
			{At: "16:15", Name: "Leave", Power: ptr(false)},
		},
	}
}

var (
	hhmm    = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)
	weekday = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"} // time.Weekday order
)

// AllDays lists the day names in display order, Monday first.
var AllDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Validate returns one error per problem, each naming where it is.
func (p Profile) Validate(pal color.Palette) []error {
	var errs []error
	bad := func(f, msg string, a ...any) { errs = append(errs, fmt.Errorf("%s: %s", f, fmt.Sprintf(msg, a...))) }
	if p.Version > Version {
		bad("version", "%d is newer than this beacon understands (%d)", p.Version, Version)
	}
	if p.Timezone != "" {
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			bad("timezone", "%v", err)
		}
	}
	seenDay := map[string]bool{}
	for _, d := range p.Days {
		switch {
		case !contains(AllDays, d):
			bad("days", "%q is not one of %v", d, AllDays)
		case seenDay[d]:
			bad("days", "%q is listed twice", d)
		}
		seenDay[d] = true
	}
	seenAt := map[string]int{}
	for i, e := range p.Entries {
		f := fmt.Sprintf("entries[%d]", i)
		if !hhmm.MatchString(e.At) {
			bad(f+".at", "%q is not HH:MM (24-hour)", e.At)
		} else if j, dup := seenAt[e.At]; dup {
			bad(f+".at", "%s is already used by entries[%d]", e.At, j)
		} else {
			seenAt[e.At] = i
		}
		if e.Color != "" && e.Kelvin != 0 {
			bad(f, "color and kelvin are mutually exclusive")
		}
		if e.Color != "" {
			if _, err := color.Parse(e.Color, pal); err != nil {
				bad(f+".color", "%v", err)
			}
		}
		if e.Kelvin != 0 && (e.Kelvin < 1000 || e.Kelvin > 12000) {
			bad(f+".kelvin", "%d is not a plausible color temperature", e.Kelvin)
		}
		if e.Brightness < 0 || e.Brightness > 100 {
			bad(f+".brightness", "must be 0-100 (0 inherits)")
		}
		if len(e.Name) > 60 {
			bad(f+".name", "too long")
		}
	}
	return errs
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Location is the zone the schedule runs in; fallback is used when the
// profile doesn't name one (normally the clock's own zone).
func (p Profile) Location(fallback *time.Location) (*time.Location, error) {
	if p.Timezone == "" {
		return fallback, nil
	}
	return time.LoadLocation(p.Timezone)
}

func minutes(at string) int {
	m := hhmm.FindStringSubmatch(at)
	if m == nil {
		return -1
	}
	h, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	return h*60 + mm
}

// sorted returns the entries in time order, dropping malformed ones.
func (p Profile) sorted() []Entry {
	out := make([]Entry, 0, len(p.Entries))
	for _, e := range p.Entries {
		if minutes(e.At) >= 0 {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return minutes(out[i].At) < minutes(out[j].At) })
	return out
}

// instant is the moment entry e starts on the calendar day of ref (plus
// offsetDays), in loc. It is built from the calendar, never by adding 24h.
//
// A wall-clock time that doesn't exist (02:30 on the spring-forward day)
// would be resolved by time.Date to an hour *earlier*, which would fire the
// entry early and out of order. So when the result's wall clock differs from
// what was asked for, the entry is shifted forward by the gap: 02:30 becomes
// 03:30. A time that occurs twice (fall back) resolves to its first occurrence.
func instant(ref time.Time, offsetDays int, e Entry, loc *time.Location) time.Time {
	m := minutes(e.At)
	y, mo, d := ref.Year(), ref.Month(), ref.Day()+offsetDays
	t := time.Date(y, mo, d, m/60, m%60, 0, 0, loc)
	want := time.Date(y, mo, d, m/60, m%60, 0, 0, time.UTC)
	got := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	if gap := want.Sub(got); gap != 0 {
		t = t.Add(gap)
	}
	return t
}

func (p Profile) dayEnabled(t time.Time) bool { return contains(p.Days, weekday[t.Weekday()]) }

// dayEntries are the entries in force on a day: everything up to and
// including the first power-off, which ends the day for this layer.
func (p Profile) dayEntries() []Entry {
	var out []Entry
	for _, e := range p.sorted() {
		out = append(out, e)
		if e.Power != nil && !*e.Power {
			break
		}
	}
	return out
}

// apply folds one entry into a running state: color and kelvin replace each
// other, and omitted fields leave the inherited value alone.
func apply(st *govee.State, e Entry, pal color.Palette) error {
	if e.Color != "" {
		c, err := color.Parse(e.Color, pal)
		if err != nil {
			return fmt.Errorf("entry %s: %w", e.At, err)
		}
		st.Color, st.Kelvin = &c, 0
	}
	if e.Kelvin != 0 {
		st.Kelvin, st.Color = e.Kelvin, nil
	}
	if e.Brightness != 0 {
		st.Brightness = e.Brightness
	}
	return nil
}

func clone(st govee.State) govee.State {
	if st.Color != nil {
		c := *st.Color
		st.Color = &c
	}
	return st
}

// Block is a schedule entry in force, with the light state it implies.
type Block struct {
	Entry  Entry
	State  govee.State
	Starts time.Time
}

// Label is "Name (HH:MM)".
func (b Block) Label() string {
	if b.Entry.Name == "" {
		return b.Entry.At
	}
	return fmt.Sprintf("%s (%s)", b.Entry.Name, b.Entry.At)
}

// Info is the schedule's answer at one instant.
type Info struct {
	Enabled bool
	Current *Block // nil before the first entry, on a disabled day, or when disabled
	Next    *Block // the next entry to start (State not computed); nil if none within a week
}

// At evaluates the schedule at now. Colors are resolved against pal.
func (p Profile) At(now time.Time, pal color.Palette) (Info, error) {
	info := Info{Enabled: p.IsEnabled()}
	if !info.Enabled {
		return info, nil
	}
	loc, err := p.Location(now.Location())
	if err != nil {
		return info, fmt.Errorf("timezone: %w", err)
	}
	local := now.In(loc)
	entries := p.dayEntries()

	if p.dayEnabled(local) {
		st := govee.State{On: true}
		for _, e := range entries {
			start := instant(local, 0, e, loc)
			if start.After(now) {
				break
			}
			if e.Power != nil && !*e.Power {
				info.Current = &Block{Entry: e, State: govee.State{}, Starts: start}
				break
			}
			if err := apply(&st, e, pal); err != nil {
				return info, err
			}
			cp := clone(st)
			info.Current = &Block{Entry: e, State: cp, Starts: start}
		}
	}

	// Next boundary: scan today and the coming week, skipping disabled days.
	for off := 0; off <= 7 && info.Next == nil; off++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+off, 12, 0, 0, 0, loc)
		if !p.dayEnabled(day) {
			continue
		}
		for i, e := range entries {
			if start := instant(local, off, e, loc); start.After(now) {
				// Entries that land on the same instant (a spring-forward gap
				// collapses 02:30 onto 03:30) take effect together; the last wins.
				for i+1 < len(entries) && instant(local, off, entries[i+1], loc).Equal(start) {
					i++
					e = entries[i]
				}
				info.Next = &Block{Entry: e, Starts: start}
				break
			}
		}
	}
	return info, nil
}

// Row is one entry as an editor shows it: the light state the entry produces
// once everything before it has been folded in.
type Row struct {
	Entry   Entry
	State   govee.State
	Ignored bool // after a power-off, so it never takes effect
}

// Timeline lists every entry in time order with its resulting state. It
// reports the first color problem but still returns the rows it could build.
func (p Profile) Timeline(pal color.Palette) ([]Row, error) {
	var rows []Row
	var firstErr error
	st := govee.State{On: true}
	ended := false
	for _, e := range p.sorted() {
		r := Row{Entry: e, Ignored: ended}
		if !ended {
			if e.Power != nil && !*e.Power {
				ended = true
				r.State = govee.State{}
			} else {
				if err := apply(&st, e, pal); err != nil && firstErr == nil {
					firstErr = err
				}
				r.State = clone(st)
			}
		}
		rows = append(rows, r)
	}
	return rows, firstErr
}

// Sorted returns a copy with the entries in time order, which is how they are
// stored once an editor has touched them. Unlike evaluation it keeps
// malformed entries (sorting them last) so Validate still sees and reports
// them rather than having them silently disappear.
func (p Profile) Sorted() Profile {
	p.Entries = append([]Entry(nil), p.Entries...)
	key := func(e Entry) int {
		if m := minutes(e.At); m >= 0 {
			return m
		}
		return 24 * 60
	}
	sort.SliceStable(p.Entries, func(i, j int) bool { return key(p.Entries[i]) < key(p.Entries[j]) })
	return p
}

// HumanIn renders a time-until as "42m", "1h05m" or "3d".
func HumanIn(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}
