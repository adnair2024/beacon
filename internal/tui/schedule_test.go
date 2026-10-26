package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

// A Monday, so the shipped schedule (weekdays) is live.
var mon10 = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

func (ta *testApp) at(now time.Time) {
	ta.snap = takeSnapshot(ta.cfg, ta.store, ta.schedDir, ta.caps(), now)
	ta.now = now
}

// openSched switches to the tab and feeds it the file as the app would.
func (ta *testApp) openSched() {
	ta.t.Helper()
	ta.press("3")
	ta.run(loadSchedCmd(ta.schedDir, ta.cfg.Schedule.Profile))
}

func (ta *testApp) schedFile() (schedule.Profile, bool) {
	ta.t.Helper()
	p, exists, err := schedule.Load(ta.schedDir, ta.cfg.Schedule.Profile)
	if err != nil {
		ta.t.Fatal(err)
	}
	return p, exists
}

// fill types text into the editor field currently focused.
func (ta *testApp) fill(vals ...string) {
	ta.t.Helper()
	for i, v := range vals {
		ta.sched.fields[i].SetValue(v)
	}
}

func TestScheduleTabShowsBuiltInDefaultUntilEdited(t *testing.T) {
	ta := newTestApp(t)
	ta.at(mon10)
	ta.openSched()
	v := ta.View()
	for _, want := range []string{"workday", "built-in default", "Arrive", "Focus", "Lunch", "Leave", "● now"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
	if _, exists := ta.schedFile(); exists {
		t.Fatal("looking at the schedule must not create the file")
	}
}

func TestAddEntryWritesSortedJSON(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	ta.press("a")
	if ta.sched.mode != schedEdit {
		t.Fatal("a opens the editor")
	}
	ta.fill("13:15", "Stand up", "", "3000k", "55")
	ta.sched.focusField(fBright)
	ta.press("enter")
	if ta.sched.mode != schedBrowse {
		t.Fatalf("saved entry should close the editor: %q", ta.sched.editErr)
	}
	p, exists := ta.schedFile()
	if !exists {
		t.Fatal("the first edit creates schedules/workday.json")
	}
	var got *schedule.Entry
	order := ""
	for i := range p.Entries {
		order += p.Entries[i].At + " "
		if p.Entries[i].At == "13:15" {
			got = &p.Entries[i]
		}
	}
	if got == nil || got.Name != "Stand up" || got.Kelvin != 3000 || got.Brightness != 55 || got.Color != "" {
		t.Fatalf("%+v", got)
	}
	if order != "07:55 09:30 11:00 12:00 13:15 14:30 16:15 " {
		t.Fatalf("entries should be stored in time order: %s", order)
	}
	if !strings.Contains(ta.sched.status, "saved: added 13:15") {
		t.Fatal(ta.sched.status)
	}
	if !strings.Contains(ta.View(), "Stand up") {
		t.Fatal("the new row should show")
	}
}

func TestEditEntryParsesEveryField(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	ta.press("j", "j") // 11:00 Lunch (amber, 25%)
	ta.press("e")
	if ta.sched.fields[fTime].Value() != "11:00" || ta.sched.fields[fLight].Value() != "amber" || ta.sched.fields[fBright].Value() != "25" {
		t.Fatalf("the editor starts from the entry: %q %q %q", ta.sched.fields[fTime].Value(), ta.sched.fields[fLight].Value(), ta.sched.fields[fBright].Value())
	}
	ta.fill("11:30", "Late lunch", "on", "#FF0000", "40")
	ta.sched.focusField(fBright)
	ta.press("enter")
	p, _ := ta.schedFile()
	var e schedule.Entry
	for _, x := range p.Entries {
		if x.Name == "Late lunch" {
			e = x
		}
	}
	if e.At != "11:30" || e.Color != "#FF0000" || e.Power == nil || !*e.Power || e.Brightness != 40 {
		t.Fatalf("%+v", e)
	}
	for _, x := range p.Entries {
		if x.Name == "Lunch" {
			t.Fatal("editing replaces the entry rather than adding another")
		}
	}
}

func TestEditorRejectsBadInputAndWritesNothing(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	cases := []struct {
		vals [numFields]string
		want string
	}{
		{[numFields]string{"25:00", "", "", "", ""}, "entries[6].at"},
		{[numFields]string{"09:30", "dup", "", "", ""}, "already used"},
		{[numFields]string{"10:00", "", "maybe", "", ""}, "power"},
		{[numFields]string{"10:00", "", "", "", "500"}, "brightness"},
		{[numFields]string{"10:00", "", "", "nope", ""}, "color"},
	}
	for _, c := range cases {
		ta.press("a")
		ta.fill(c.vals[:]...)
		ta.sched.focusField(fBright)
		ta.press("enter")
		if ta.sched.mode != schedEdit || !strings.Contains(ta.sched.editErr, c.want) {
			t.Errorf("%v: editor should stay open with an error mentioning %q, got mode %d err %q", c.vals, c.want, ta.sched.mode, ta.sched.editErr)
		}
		ta.press("esc")
	}
	if _, exists := ta.schedFile(); exists {
		t.Fatal("nothing invalid may reach the file")
	}
}

func TestEditorSwallowsGlobalKeys(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	ta.press("a")
	ta.press("q", "1", "?")
	if ta.tab != tabSchedule || ta.helpOn || ta.sched.mode != schedEdit {
		t.Fatal("typing in the editor must not quit, switch tabs, or toggle help")
	}
	if !strings.Contains(ta.sched.fields[fTime].Value(), "q1?") {
		t.Fatalf("the text should have gone into the field: %q", ta.sched.fields[fTime].Value())
	}
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	ta.press("d", "n")
	if _, exists := ta.schedFile(); exists {
		t.Fatal("declining must not write anything")
	}
	ta.press("d", "y")
	p, exists := ta.schedFile()
	if !exists || len(p.Entries) != 5 || p.Entries[0].Name != "Focus" {
		t.Fatalf("%+v", p.Entries)
	}
}

func TestToggleDays(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	ta.press("t")
	if ta.sched.mode != schedDays {
		t.Fatal("t enters day mode")
	}
	ta.press("right", "right", " ")          // Wed off
	ta.press("right", "right", "right", " ") // Sat on
	p, _ := ta.schedFile()
	if strings.Join(p.Days, ",") != "mon,tue,thu,fri,sat" {
		t.Fatalf("%v", p.Days)
	}
	ta.press("esc")
	if ta.sched.mode != schedBrowse {
		t.Fatal("esc leaves day mode")
	}
}

func TestEnableDisableToggle(t *testing.T) {
	ta := newTestApp(t)
	ta.at(mon10)
	ta.openSched()
	ta.press("E")
	p, _ := ta.schedFile()
	if p.IsEnabled() {
		t.Fatal("E disables an enabled schedule")
	}
	ta.at(mon10)
	if !strings.Contains(ta.View(), "DISABLED") || ta.snap.Res.Source == "schedule" {
		t.Fatal("a disabled schedule shows as such and stops contributing")
	}
	ta.press("E")
	if p, _ := ta.schedFile(); !p.IsEnabled() {
		t.Fatal("E again re-enables")
	}
}

func TestPreviewSetsAShortOverride(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	ta.press("j", "j", "p") // Lunch: amber, 25%
	f, _ := ta.store.Load()
	if f.Override == nil || f.Override.State.Color == nil || f.Override.State.Color.Hex() != "#FFB300" || f.Override.State.Brightness != 25 {
		t.Fatalf("%+v", f.Override)
	}
	if d := time.Until(f.Override.ExpiresAt); d < 3*time.Second || d > 6*time.Second {
		t.Fatalf("a preview lasts about 5 seconds, got %v", d)
	}
	if ta.status != "previewing for 5 seconds" {
		t.Fatal(ta.status)
	}
	// Entries after a power-off never take effect, so there is nothing to preview.
	p := schedule.Default()
	p.Entries = append(p.Entries, schedule.Entry{At: "18:00", Name: "Evening", Kelvin: 2700})
	if err := schedule.Save(ta.schedDir, ta.cfg.Schedule.Profile, p, nil); err != nil {
		t.Fatal(err)
	}
	ta.store.Update(func(f *state.File) error { f.Override = nil; return nil })
	ta.run(loadSchedCmd(ta.schedDir, ta.cfg.Schedule.Profile))
	ta.press("j", "j", "j", "j") // last row
	ta.press("p")
	if f, _ := ta.store.Load(); f.Override != nil || !ta.sched.statusE {
		t.Fatalf("ignored row must not preview: %+v %q", f.Override, ta.sched.status)
	}
}

func TestBrokenScheduleFileIsReportedWithItsLine(t *testing.T) {
	ta := newTestApp(t)
	os.MkdirAll(ta.schedDir, 0o755)
	os.WriteFile(filepath.Join(ta.schedDir, "workday.json"), []byte("{\n  \"days\": [\"mon\"],\n  \"entries\": [,]\n}\n"), 0o644)
	ta.openSched()
	if v := ta.View(); !strings.Contains(v, ":3:16") || !strings.Contains(v, "invalid character") {
		t.Fatalf("the tab should show where the JSON is broken:\n%s", v)
	}
	ta.at(mon10)
	if ta.snap.SchedErr == nil {
		t.Fatal("the snapshot carries the error too")
	}
	if v := ta.View(); !strings.Contains(v, ":3:16") {
		t.Fatal("and so does the dashboard")
	}
	// Claude status still works meanwhile.
	if ta.snap.Err != nil {
		t.Fatalf("a bad schedule must not break the rest of the dashboard: %v", ta.snap.Err)
	}
}

func TestReloadDoesNotClobberAnEditInProgress(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	ta.press("a")
	ta.fill("13:00", "mid-edit", "", "", "")
	ta.run(loadSchedCmd(ta.schedDir, ta.cfg.Schedule.Profile)) // e.g. a periodic reload
	if ta.sched.fields[fName].Value() != "mid-edit" || ta.sched.mode != schedEdit {
		t.Fatal("a reload while editing must be ignored")
	}
}

func TestSwitchingToTheTabReloadsFromDisk(t *testing.T) {
	ta := newTestApp(t)
	ta.openSched()
	p := schedule.Default()
	p.Entries[0].Name = "Hand-edited"
	if err := schedule.Save(ta.schedDir, ta.cfg.Schedule.Profile, p, nil); err != nil {
		t.Fatal(err)
	}
	ta.press("1", "3")
	if !strings.Contains(ta.View(), "Hand-edited") {
		t.Fatal("hand edits (or another process) should show when you come back to the tab")
	}
}

func TestLightFieldParsing(t *testing.T) {
	m := newSched("", "workday")
	for text, want := range map[string]schedule.Entry{
		"3000k":   {Kelvin: 3000},
		"3000K":   {Kelvin: 3000},
		"amber":   {Color: "amber"},
		"#FF8C00": {Color: "#FF8C00"},
		"":        {},
	} {
		m.fields[fTime].SetValue("08:00")
		m.fields[fLight].SetValue(text)
		e, err := m.entryFromFields()
		if err != nil || e.Kelvin != want.Kelvin || e.Color != want.Color {
			t.Errorf("%q -> %+v (%v), want %+v", text, e, err, want)
		}
	}
}

// ---- goldens ----

func TestGoldenSchedule(t *testing.T) {
	ta := newTestApp(t)
	ta.at(mon10)
	ta.openSched()
	golden(t, "schedule_browse", ta.View())

	ta.press("j", "j", "t")
	golden(t, "schedule_days", ta.View())
	ta.press("esc", "d")
	golden(t, "schedule_delete", ta.View())
	ta.press("n", "e")
	golden(t, "schedule_edit", ta.View())
}

func TestGoldenScheduleDisabledAndEmpty(t *testing.T) {
	ta := newTestApp(t)
	ta.at(mon10)
	p := schedule.Default()
	p.SetEnabled(false)
	p.Entries = nil
	schedule.Save(ta.schedDir, "workday", p, nil)
	ta.openSched()
	golden(t, "schedule_empty_disabled", ta.View())
}

func TestGoldenDashboardWithSchedule(t *testing.T) {
	ta := newTestApp(t)
	ta.at(mon10)
	golden(t, "dashboard_schedule", ta.View())
}
