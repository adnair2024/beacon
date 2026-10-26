package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parse(t *testing.T, src string) map[string]string {
	t.Helper()
	vars, errs := Parse(strings.NewReader(src))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	return vars
}

func TestParseFormats(t *testing.T) {
	src := string(rune(0xFEFF)) + "# a comment\r\n" +
		"\r\n" +
		"PLAIN=abc123\r\n" +
		"export EXPORTED=yes\n" +
		"SPACED = padded value  \n" +
		"TRAILING=value # a comment\n" +
		"HASHINSIDE=ab#cd\n" +
		`DOUBLE="has # hash and \"quotes\" and \\ and \n newline"` + "\n" +
		`SINGLE='raw \n $HOME # kept'` + "\n" +
		`QUOTED_THEN_COMMENT="v" # note` + "\n" +
		"EMPTY=\n" +
		`EMPTYQ=""` + "\n"
	got := parse(t, src)
	want := map[string]string{
		"PLAIN": "abc123", "EXPORTED": "yes", "SPACED": "padded value", "TRAILING": "value",
		"HASHINSIDE": "ab#cd", "DOUBLE": "has # hash and \"quotes\" and \\ and \n newline",
		"SINGLE": `raw \n $HOME # kept`, "QUOTED_THEN_COMMENT": "v", "EMPTY": "", "EMPTYQ": "",
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s = %q (present %v), want %q", k, g, ok, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra keys: %v", got)
	}
}

func TestParseReportsEachBadLineAndKeepsTheRest(t *testing.T) {
	src := "GOOD=1\nnot a pair\n9BAD=x\nUNTERMINATED=\"abc\nTRAIL=\"a\" junk\nALSO_GOOD=2\n"
	vars, errs := Parse(strings.NewReader(src))
	if vars["GOOD"] != "1" || vars["ALSO_GOOD"] != "2" || len(vars) != 2 {
		t.Fatalf("%v", vars)
	}
	if len(errs) != 4 {
		t.Fatalf("want 4 errors, got %v", errs)
	}
	for i, want := range []string{"line 2", "line 3", "line 4", "line 5"} {
		if !strings.Contains(errs[i].Error(), want) {
			t.Errorf("error %d should name %s: %v", i, want, errs[i])
		}
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadSetsAllowedVariables(t *testing.T) {
	t.Setenv("GOVEE_API_KEY", "")
	os.Unsetenv("GOVEE_API_KEY")
	p := write(t, "GOVEE_API_KEY=secret-from-file\n")
	res := Load(p)
	if os.Getenv("GOVEE_API_KEY") != "secret-from-file" || len(res.Set) != 1 || res.Set[0] != "GOVEE_API_KEY" {
		t.Fatalf("%+v", res)
	}
	t.Cleanup(func() { os.Unsetenv("GOVEE_API_KEY") })
}

func TestRealEnvironmentWins(t *testing.T) {
	t.Setenv("GOVEE_API_KEY", "from-the-shell")
	res := Load(write(t, "GOVEE_API_KEY=from-the-file\n"))
	if os.Getenv("GOVEE_API_KEY") != "from-the-shell" || len(res.Set) != 0 {
		t.Fatalf("a variable already in the environment must not be overridden: %+v", res)
	}
}

// A .env in an unrelated directory must not be able to change anything but
// beacon's own settings.
func TestOnlyAllowlistedVariablesAreHonored(t *testing.T) {
	t.Setenv("PATH_BEFORE", "x")
	pathBefore, ldBefore := os.Getenv("PATH"), os.Getenv("LD_PRELOAD")
	res := Load(write(t, "PATH=/evil\nLD_PRELOAD=/evil.so\nHOME=/evil\nGOVEE_API_KEY=ok\n"))
	t.Cleanup(func() { os.Unsetenv("GOVEE_API_KEY") })
	if os.Getenv("PATH") != pathBefore || os.Getenv("LD_PRELOAD") != ldBefore || os.Getenv("HOME") == "/evil" {
		t.Fatal("a non-allowlisted variable was applied")
	}
	if len(res.Skipped) != 3 {
		t.Fatalf("skipped should list them (names only): %v", res.Skipped)
	}
}

func TestEmptyTemplateValuesAreIgnored(t *testing.T) {
	os.Unsetenv("GOVEE_API_KEY")
	res := Load(write(t, "GOVEE_API_KEY=\nBEACON_STATE_DIR=\"\"\n"))
	if os.Getenv("GOVEE_API_KEY") != "" || len(res.Set) != 0 {
		t.Fatalf("an unfilled .env.example line is not a value: %+v", res)
	}
}

func TestMissingFileIsFine(t *testing.T) {
	res := Load(filepath.Join(t.TempDir(), "nope"))
	if len(res.Set) != 0 || len(res.Errors) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestResultNeverContainsValues(t *testing.T) {
	os.Unsetenv("GOVEE_API_KEY")
	t.Cleanup(func() { os.Unsetenv("GOVEE_API_KEY") })
	res := Load(write(t, "GOVEE_API_KEY=topsecretvalue\nOTHER=anothersecret\nbroken line topsecretvalue\n"))
	dump := strings.Join(append(append(res.Set, res.Skipped...), errStrings(res.Errors)...), " ")
	if strings.Contains(dump, "topsecretvalue") || strings.Contains(dump, "anothersecret") {
		t.Fatalf("diagnostics must not echo values: %q", dump)
	}
}

func errStrings(errs []error) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Error()
	}
	return out
}

func TestTooOpen(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("mode bits are not meaningful on Windows")
	}
	p := write(t, "GOVEE_API_KEY=x\n")
	if TooOpen(p) {
		t.Fatal("0600 is fine")
	}
	os.Chmod(p, 0o644)
	if !TooOpen(p) {
		t.Fatal("a world-readable credential file should be flagged")
	}
	if TooOpen(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("a missing file is not 'too open'")
	}
}
