package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingGivesTheShippedDefault(t *testing.T) {
	p, exists, err := Load(t.TempDir(), "workday")
	if err != nil || exists || len(p.Entries) != 6 || p.Entries[0].Name != "Arrive" || !p.IsEnabled() {
		t.Fatal(p, exists, err)
	}
}

func TestSaveLoadRoundTripAndBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "schedules")
	p := Default()
	p.Timezone = "America/Chicago"
	p.Entries[0].Brightness = 55
	if err := Save(dir, "workday", p, nil); err != nil {
		t.Fatal(err)
	}
	got, exists, err := Load(dir, "workday")
	if err != nil || !exists || got.Timezone != "America/Chicago" || got.Entries[0].Brightness != 55 {
		t.Fatal(got, exists, err)
	}
	if got.Entries[5].Power == nil || *got.Entries[5].Power {
		t.Fatal("power=false must survive the round trip (a pointer, not a zero value)")
	}
	if got.Entries[1].Power != nil {
		t.Fatal("an omitted power must stay omitted")
	}
	if _, err := os.Stat(filepath.Join(dir, "workday.json.bak")); err == nil {
		t.Fatal("no backup before the second save")
	}
	p.Entries[0].Brightness = 65
	if err := Save(dir, "workday", p, nil); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(filepath.Join(dir, "workday.json.bak"))
	if err != nil || !strings.Contains(string(bak), `"brightness": 55`) {
		t.Fatalf("the previous version should be kept: %v\n%s", err, bak)
	}
	// Human-friendly on disk.
	raw, _ := os.ReadFile(filepath.Join(dir, "workday.json"))
	if !strings.HasSuffix(string(raw), "}\n") || !strings.Contains(string(raw), "\n  \"entries\": [") {
		t.Fatalf("expected indented JSON with a trailing newline:\n%s", raw)
	}
	// No temp files left behind.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover %s", e.Name())
		}
	}
}

func TestSaveRefusesInvalid(t *testing.T) {
	dir := t.TempDir()
	p := Default()
	p.Entries[0].At = "99:99"
	if err := Save(dir, "workday", p, nil); err == nil || !strings.Contains(err.Error(), "entries[0].at") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workday.json")); err == nil {
		t.Fatal("an invalid schedule must not be written")
	}
}

func TestProfileNamesCannotEscapeTheDirectory(t *testing.T) {
	for _, bad := range []string{"", "../x", "a/b", `a\b`, ".hidden", "x.json", "a b", strings.Repeat("a", 65)} {
		if ValidName(bad) {
			t.Errorf("%q should be rejected", bad)
		}
		if _, err := Path("/tmp/s", bad); err == nil {
			t.Errorf("Path(%q) should fail", bad)
		}
	}
	for _, ok := range []string{"workday", "night-shift", "A_1"} {
		if !ValidName(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	if err := Save(t.TempDir(), "../evil", Default(), nil); err == nil {
		t.Fatal("Save must reject a traversal name")
	}
}

func TestParseErrorsNameLineAndColumn(t *testing.T) {
	src := "{\n  \"days\": [\"mon\"],\n  \"entries\": [\n    {\"at\": \"08:00\",, \"kelvin\": 3000}\n  ]\n}\n"
	_, err := Parse([]byte(src), "workday.json")
	if err == nil || !strings.HasPrefix(err.Error(), "workday.json:4:") {
		t.Fatalf("a syntax error should name the line: %v", err)
	}
	_, err = Parse([]byte(`{"entries":[{"at":"08:00","kelvin":"warm"}]}`), "w.json")
	if err == nil || !strings.Contains(err.Error(), "w.json:1:") || !strings.Contains(err.Error(), "kelvin") {
		t.Fatalf("a type error should name the field: %v", err)
	}
}

func TestParseIgnoresUnknownKeysAndDefaultsVersion(t *testing.T) {
	p, err := Parse([]byte(`{"days":["mon"],"future_feature":{"x":1},"entries":[{"at":"08:00","glow":true}]}`), "w.json")
	if err != nil || p.Version != Version || len(p.Entries) != 1 {
		t.Fatal(p, err)
	}
}

func TestLoadReportsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "workday.json"), []byte("{ nope"), 0o644)
	if _, _, err := Load(dir, "workday"); err == nil || !strings.Contains(err.Error(), "workday.json:1:") {
		t.Fatal(err)
	}
}
