package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/modes/schedule"
)

// Monday 10:00 UTC: the shipped schedule is in its Focus block.
func atMonday10(t *testing.T) {
	t.Helper()
	old := clock
	clock = func() time.Time { return time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { clock = old })
}

func (e *env) schedDir() string { return filepath.Join(filepath.Dir(e.cfgPath), "schedules") }

func TestScheduleShowAndNextUseTheShippedDefault(t *testing.T) {
	atMonday10(t)
	e := newEnv(t, "")
	out, _, err := e.run("", "schedule", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"workday · enabled", "built-in default", "▸ 09:30  Focus", "  11:00  Lunch", "off"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	out, _, err = e.run("", "schedule", "next")
	if err != nil || !strings.Contains(out, "next: Lunch at Mon 11:00 (in 1h00m)") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := os.Stat(e.schedDir()); err == nil {
		t.Fatal("reading the schedule must not create files")
	}
}

func TestScheduleInitWritesEditableJSON(t *testing.T) {
	e := newEnv(t, "")
	out, _, err := e.run("", "schedule", "init")
	if err != nil || !strings.Contains(out, "workday.json") {
		t.Fatalf("%q %v", out, err)
	}
	raw, err := os.ReadFile(filepath.Join(e.schedDir(), "workday.json"))
	if err != nil || !strings.Contains(string(raw), `"entries"`) || !strings.Contains(string(raw), `"name": "Lunch"`) {
		t.Fatalf("%v\n%s", err, raw)
	}
	if _, _, err := e.run("", "schedule", "init"); err == nil {
		t.Fatal("init must not clobber an existing file")
	}
	if _, _, err := e.run("", "schedule", "init", "--force"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.schedDir(), "workday.json.bak")); err != nil {
		t.Fatal("--force keeps the old file as .bak")
	}
	if out, _, _ := e.run("", "schedule", "path"); strings.TrimSpace(out) != filepath.Join(e.schedDir(), "workday.json") {
		t.Fatal(out)
	}
}

func TestScheduleEnableDisableAreDryRunSafe(t *testing.T) {
	atMonday10(t)
	e := newEnv(t, "")
	if _, _, err := e.run("", "schedule", "disable", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	// The real schedule directory is untouched by a dry run...
	if _, err := os.Stat(filepath.Join(e.schedDir(), "workday.json")); err == nil {
		t.Fatal("--dry-run must not write the real schedule file")
	}
	// ...and the dry-run copy is what the dry-run commands read.
	out, _, _ := e.run("", "schedule", "show", "--dry-run")
	if !strings.Contains(out, "DISABLED") {
		t.Fatal(out)
	}
	out, _, _ = e.run("", "schedule", "show")
	if strings.Contains(out, "DISABLED") {
		t.Fatal("the real schedule is still enabled")
	}
	out, _, _ = e.run("", "schedule", "next", "--dry-run")
	if !strings.Contains(out, "disabled") {
		t.Fatal(out)
	}

	// For real: disable writes the JSON and an enable restores it.
	if _, _, err := e.run("", "schedule", "disable"); err != nil {
		t.Fatal(err)
	}
	p, exists, _ := schedule.Load(e.schedDir(), "workday")
	if !exists || p.IsEnabled() {
		t.Fatalf("%+v %v", p, exists)
	}
	if _, _, err := e.run("", "schedule", "enable"); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := schedule.Load(e.schedDir(), "workday"); !p.IsEnabled() {
		t.Fatal("enable should re-enable")
	}
}

func TestScheduleValidateAndConfigValidateReportFileProblems(t *testing.T) {
	e := newEnv(t, "")
	os.MkdirAll(e.schedDir(), 0o755)
	bad := `{"days":["mon","funday"],"entries":[{"at":"25:00"},{"at":"09:00","color":"red","kelvin":3000}]}`
	os.WriteFile(filepath.Join(e.schedDir(), "workday.json"), []byte(bad), 0o644)
	_, errOut, err := e.run("", "schedule", "validate")
	if err == nil || !strings.Contains(errOut, "funday") || !strings.Contains(errOut, "entries[0].at") || !strings.Contains(errOut, "mutually exclusive") {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if _, errOut, err := e.run("", "config", "validate"); err == nil || !strings.Contains(errOut, "entries[0].at") {
		t.Fatalf("config validate should check the schedule file too: %v\n%s", err, errOut)
	}
	// A syntactically broken file is reported with its line.
	os.WriteFile(filepath.Join(e.schedDir(), "workday.json"), []byte("{\n\"days\": [\n"), 0o644)
	if _, _, err := e.run("", "schedule", "validate"); err == nil || !strings.Contains(err.Error(), "workday.json:") {
		t.Fatalf("%v", err)
	}
	os.Remove(filepath.Join(e.schedDir(), "workday.json"))
	if out, _, err := e.run("", "schedule", "validate"); err != nil || !strings.Contains(out, "built-in default") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestScheduleProfileNameCannotEscape(t *testing.T) {
	e := newEnv(t, "[schedule]\nprofile = \"../../etc/passwd\"\n")
	if _, _, err := e.run("", "schedule", "show"); err == nil {
		t.Fatal("a traversal profile name must be rejected")
	}
	if _, errOut, err := e.run("", "config", "validate"); err == nil || !strings.Contains(errOut, "schedule.profile") {
		t.Fatalf("%v %s", err, errOut)
	}
}

func TestOtherProfilesAreSeparateFiles(t *testing.T) {
	e := newEnv(t, "[schedule]\nprofile = \"night-shift\"\n")
	if _, _, err := e.run("", "schedule", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.schedDir(), "night-shift.json")); err != nil {
		t.Fatal("the profile name picks the file")
	}
	if _, err := os.Stat(filepath.Join(e.schedDir(), "workday.json")); err == nil {
		t.Fatal("other profiles are untouched")
	}
}
