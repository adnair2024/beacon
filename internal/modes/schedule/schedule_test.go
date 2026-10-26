package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
)

var utc = time.UTC

// Monday 2026-10-26 in the profile's zone (system local = UTC in these tests).
func mon(h, m int) time.Time { return time.Date(2026, 10, 26, h, m, 0, 0, utc) }

func at(t *testing.T, p Profile, now time.Time) Info {
	t.Helper()
	info, err := p.At(now, nil)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func name(b *Block) string {
	if b == nil {
		return "<nil>"
	}
	return b.Entry.Name
}

func TestBlockSelectionAndInheritance(t *testing.T) {
	p := Default()
	cases := []struct {
		at       time.Time
		cur      string
		next     string
		kelvin   int
		color    string
		bri      int
		on       bool
		nextTime string
	}{
		{mon(7, 54), "<nil>", "Arrive", 0, "", 0, false, "07:55"},
		{mon(7, 55), "Arrive", "Focus", 2900, "", 60, true, "09:30"},
		{mon(9, 29), "Arrive", "Focus", 2900, "", 60, true, "09:30"},
		{mon(9, 30), "Focus", "Lunch", 4800, "", 90, true, "11:00"},
		// Lunch sets a color: that replaces the inherited kelvin.
		{mon(11, 0), "Lunch", "Back to work", 0, "#FFB300", 25, true, "12:00"},
		// ...and the next entry's kelvin replaces the color again.
		{mon(12, 0), "Back to work", "Wind down", 4800, "", 90, true, "14:30"},
		{mon(14, 30), "Wind down", "Leave", 3000, "", 70, true, "16:15"},
		// Power false: off, and the day is over.
		{mon(16, 15), "Leave", "Arrive", 0, "", 0, false, "07:55"},
		{mon(23, 59), "Leave", "Arrive", 0, "", 0, false, "07:55"},
	}
	for _, c := range cases {
		info := at(t, p, c.at)
		if name(info.Current) != c.cur || name(info.Next) != c.next {
			t.Errorf("%v: current %s, next %s; want %s, %s", c.at.Format("15:04"), name(info.Current), name(info.Next), c.cur, c.next)
			continue
		}
		if info.Next != nil && info.Next.Entry.At != c.nextTime {
			t.Errorf("%v: next at %s, want %s", c.at.Format("15:04"), info.Next.Entry.At, c.nextTime)
		}
		if info.Current == nil {
			continue
		}
		st := info.Current.State
		hex := ""
		if st.Color != nil {
			hex = st.Color.Hex()
		}
		if st.On != c.on || st.Kelvin != c.kelvin || hex != c.color || st.Brightness != c.bri {
			t.Errorf("%v %s: state %+v color %q, want on=%v kelvin=%d color=%q bri=%d",
				c.at.Format("15:04"), c.cur, st, hex, c.on, c.kelvin, c.color, c.bri)
		}
	}
}

func TestNextRollsToTomorrowAndSkipsDisabledDays(t *testing.T) {
	p := Default()
	// Friday evening after Leave: the next block is Monday 07:55.
	fri := time.Date(2026, 10, 30, 18, 0, 0, 0, utc)
	if info := at(t, p, fri); info.Next == nil || !info.Next.Starts.Equal(time.Date(2026, 11, 2, 7, 55, 0, 0, utc)) {
		t.Fatalf("%+v", info.Next)
	}
	// Saturday: nothing in force; next is Monday.
	sat := time.Date(2026, 10, 31, 10, 0, 0, 0, utc)
	info := at(t, p, sat)
	if info.Current != nil || info.Next == nil || info.Next.Starts.Weekday() != time.Monday {
		t.Fatalf("%+v %+v", info.Current, info.Next)
	}
	// Sunday too, and a profile with no enabled days has no next at all.
	p.Days = nil
	if info := at(t, p, mon(10, 0)); info.Current != nil || info.Next != nil {
		t.Fatalf("%+v", info)
	}
}

func TestDisabledScheduleContributesNothing(t *testing.T) {
	p := Default()
	p.SetEnabled(false)
	info := at(t, p, mon(10, 0))
	if info.Enabled || info.Current != nil || info.Next != nil {
		t.Fatalf("%+v", info)
	}
	// A hand-written file with no "enabled" key is enabled.
	var q Profile
	if !q.IsEnabled() {
		t.Fatal("absent enabled means enabled")
	}
}

func TestPowerOffEndsTheDayForLaterEntries(t *testing.T) {
	p := Default()
	p.Entries = append(p.Entries, Entry{At: "18:00", Name: "Evening", Kelvin: 2700})
	if info := at(t, p, mon(19, 0)); name(info.Current) != "Leave" || info.Current.State.On {
		t.Fatalf("entries after a power-off are ignored: %+v", info.Current)
	}
	if info := at(t, p, mon(16, 30)); info.Next == nil || info.Next.Entry.Name != "Arrive" {
		t.Fatalf("and they are never 'next' either: %+v", info.Next)
	}
}

func TestUnsortedEntriesAreOrderedByTime(t *testing.T) {
	p := Profile{Days: AllDays, Entries: []Entry{
		{At: "12:00", Name: "Noon", Kelvin: 5000}, {At: "08:00", Name: "Morning", Kelvin: 3000},
	}}
	if info := at(t, p, mon(9, 0)); name(info.Current) != "Morning" || name(info.Next) != "Noon" {
		t.Fatalf("%s %s", name(info.Current), name(info.Next))
	}
}

func TestOmittedFieldsInheritWithinTheDay(t *testing.T) {
	p := Profile{Days: AllDays, Entries: []Entry{
		{At: "08:00", Name: "A", Color: "red", Brightness: 70},
		{At: "09:00", Name: "B"}, // changes nothing but the name
		{At: "10:00", Name: "C", Brightness: 20},
	}}
	b := at(t, p, mon(9, 30)).Current
	if b.Entry.Name != "B" || b.State.Color.Hex() != "#FF0000" || b.State.Brightness != 70 {
		t.Fatalf("%+v", b.State)
	}
	c := at(t, p, mon(10, 30)).Current
	if c.State.Color.Hex() != "#FF0000" || c.State.Brightness != 20 {
		t.Fatalf("%+v", c.State)
	}
	// Returned states are independent copies.
	b.State.Color.R = 1
	if at(t, p, mon(9, 30)).Current.State.Color.R != 255 {
		t.Fatal("blocks must not share color pointers")
	}
}

func TestConfiguredTimezone(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("no tzdata")
	}
	p := Default()
	p.Timezone = "America/Chicago"
	// 14:00 UTC on Oct 26 is 09:00 CDT: Arrive, not yet Focus.
	info := at(t, p, time.Date(2026, 10, 26, 14, 0, 0, 0, utc))
	if name(info.Current) != "Arrive" || !info.Next.Starts.Equal(time.Date(2026, 10, 26, 9, 30, 0, 0, chicago)) {
		t.Fatalf("%s %v", name(info.Current), info.Next.Starts)
	}
	// 03:00 UTC Tuesday is still Monday 22:00 in Chicago: after Leave.
	if info := at(t, p, time.Date(2026, 10, 27, 3, 0, 0, 0, utc)); name(info.Current) != "Leave" {
		t.Fatalf("the day is the zone's day, not UTC's: %s", name(info.Current))
	}
}

func TestUserPaletteAndBadColor(t *testing.T) {
	p := Profile{Days: AllDays, Entries: []Entry{{At: "08:00", Name: "A", Color: "mine"}}}
	info, err := p.At(mon(9, 0), color.Palette{"mine": {R: 1, G: 2, B: 3}})
	if err != nil || info.Current.State.Color.Hex() != "#010203" {
		t.Fatal(info.Current, err)
	}
	if _, err := p.At(mon(9, 0), nil); err == nil || !strings.Contains(err.Error(), "08:00") {
		t.Fatalf("an unknown color should name the entry: %v", err)
	}
}

// ---- DST (America/Chicago) ----

func chicago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("no tzdata")
	}
	return loc
}

func TestSpringForwardGap(t *testing.T) {
	loc := chicago(t)
	// 2026-03-08: 02:00 CST jumps to 03:00 CDT. 02:30 does not exist.
	p := Profile{Timezone: "America/Chicago", Days: AllDays, Entries: []Entry{
		{At: "01:30", Name: "A", Kelvin: 3000}, {At: "02:30", Name: "B", Kelvin: 4000}, {At: "03:30", Name: "C", Kelvin: 5000},
	}}
	d := func(h, m int) time.Time { return time.Date(2026, 3, 8, h, m, 0, 0, loc) }
	if got := name(at(t, p, d(1, 45)).Current); got != "A" {
		t.Fatal(got)
	}
	// The gap makes B and C land on the same instant (03:30 CDT); the later wins
	// and the missing 02:30 doesn't leave a hole or reorder anything.
	if got := name(at(t, p, d(3, 29)).Current); got != "A" {
		t.Fatalf("before the collapsed boundary still A, got %s", got)
	}
	if got := name(at(t, p, d(3, 30)).Current); got != "C" {
		t.Fatalf("got %s", got)
	}
	if next := at(t, p, d(1, 45)).Next; next.Entry.Name != "C" || !next.Starts.Equal(d(3, 30)) {
		t.Fatalf("next should skip straight to the winner: %+v", next)
	}
	// Exactly 24 wall-clock hours of that day is 23 elapsed hours: the 7:55
	// block the next morning is computed on the calendar, not by adding 24h.
	q := Profile{Timezone: "America/Chicago", Days: AllDays, Entries: []Entry{{At: "07:55", Name: "Arrive"}}}
	next := at(t, q, time.Date(2026, 3, 7, 12, 0, 0, 0, loc)).Next
	if want := time.Date(2026, 3, 8, 7, 55, 0, 0, loc); !next.Starts.Equal(want) || next.Starts.Hour() != 7 {
		t.Fatalf("got %v want %v", next.Starts, want)
	}
}

func TestFallBackRepeatedHour(t *testing.T) {
	loc := chicago(t)
	// 2026-11-01: 02:00 CDT falls back to 01:00 CST, so 01:30 happens twice.
	p := Profile{Timezone: "America/Chicago", Days: AllDays, Entries: []Entry{
		{At: "01:00", Name: "A", Kelvin: 3000}, {At: "01:30", Name: "B", Kelvin: 4000}, {At: "03:00", Name: "C", Kelvin: 5000},
	}}
	// First 01:30 (CDT) = 06:30 UTC; second 01:30 (CST) = 07:30 UTC.
	for _, c := range []struct {
		utc  time.Time
		want string
	}{
		{time.Date(2026, 11, 1, 6, 15, 0, 0, utc), "A"},
		{time.Date(2026, 11, 1, 6, 30, 0, 0, utc), "B"},
		{time.Date(2026, 11, 1, 7, 45, 0, 0, utc), "B"}, // in the repeated hour: stays B, no flapping
		{time.Date(2026, 11, 1, 8, 59, 0, 0, utc), "B"},
		{time.Date(2026, 11, 1, 9, 0, 0, 0, utc), "C"}, // 03:00 CST
	} {
		if got := name(at(t, p, c.utc).Current); got != c.want {
			t.Errorf("%v: %s, want %s", c.utc.Format("15:04Z"), got, c.want)
		}
	}
	// The next morning's boundary is 07:55 CST = 13:55 UTC (adding 24h to the
	// previous day's 07:55 CDT would give 12:55 UTC).
	q := Profile{Timezone: "America/Chicago", Days: AllDays, Entries: []Entry{{At: "07:55", Name: "Arrive"}}}
	next := at(t, q, time.Date(2026, 10, 31, 12, 0, 0, 0, loc)).Next
	if want := time.Date(2026, 11, 1, 13, 55, 0, 0, utc); !next.Starts.Equal(want) {
		t.Fatalf("got %v want %v", next.Starts.UTC(), want)
	}
}

// ---- validation ----

func TestValidate(t *testing.T) {
	if errs := Default().Validate(nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	bad := Profile{
		Version: 99, Timezone: "Mars/Olympus", Days: []string{"mon", "funday", "mon"},
		Entries: []Entry{
			{At: "25:00"}, {At: "9:30"},
			{At: "10:00", Color: "red", Kelvin: 3000},
			{At: "10:00", Name: "dup"},
			{At: "11:00", Color: "nope"},
			{At: "12:00", Brightness: 101},
			{At: "13:00", Kelvin: 50},
		},
	}
	errs := bad.Validate(nil)
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{"version", "timezone", "funday", "listed twice", "entries[0].at", "entries[1].at",
		"mutually exclusive", "already used by entries[2]", "entries[4].color", "entries[5].brightness", "entries[6].kelvin"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing an error mentioning %q in:\n%s", want, joined)
		}
	}
}

func TestSortedKeepsMalformedEntriesSoValidateCanReportThem(t *testing.T) {
	p := Profile{Days: AllDays, Entries: []Entry{{At: "12:00"}, {At: "25:00"}, {At: "08:00"}, {At: "bogus"}}}
	got := p.Sorted()
	order := ""
	for _, e := range got.Entries {
		order += e.At + " "
	}
	if order != "08:00 12:00 25:00 bogus " {
		t.Fatalf("valid entries in time order, malformed ones last and not dropped: %q", order)
	}
	if len(got.Validate(nil)) != 2 {
		t.Fatalf("both malformed entries must be reported: %v", got.Validate(nil))
	}
	if p.Entries[0].At != "12:00" {
		t.Fatal("Sorted must not reorder the original")
	}
}

func TestTimelineFoldsAndFlagsIgnoredRows(t *testing.T) {
	p := Default()
	p.Entries = append(p.Entries, Entry{At: "18:00", Name: "Evening", Kelvin: 2700})
	rows, err := p.Timeline(nil)
	if err != nil || len(rows) != 7 {
		t.Fatal(len(rows), err)
	}
	if rows[2].Entry.Name != "Lunch" || rows[2].State.Color == nil || rows[2].State.Kelvin != 0 || rows[2].State.Brightness != 25 {
		t.Fatalf("%+v", rows[2])
	}
	if rows[3].State.Color != nil || rows[3].State.Kelvin != 4800 {
		t.Fatalf("each row folds on top of the previous: %+v", rows[3])
	}
	if rows[5].State.On || rows[5].Ignored {
		t.Fatalf("the power-off row is off but in force: %+v", rows[5])
	}
	if !rows[6].Ignored {
		t.Fatal("entries after a power-off never take effect")
	}
}

func TestHumanIn(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "<1m", 42 * time.Minute: "42m", time.Hour: "1h00m",
		65 * time.Minute: "1h05m", 47 * time.Hour: "47h00m", 72 * time.Hour: "3d",
	} {
		if got := HumanIn(d); got != want {
			t.Errorf("%v: %s, want %s", d, got, want)
		}
	}
}
