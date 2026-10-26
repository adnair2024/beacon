package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpecsCoverEveryEventAndMarkTheOptionalOnes(t *testing.T) {
	by := map[string]Spec{}
	for _, s := range Specs() {
		by[s.Hook] = s
		if _, ok := ParseEvent(s.Event); !ok {
			t.Errorf("%s maps to %q, which `beacon hook` doesn't accept", s.Hook, s.Event)
		}
		if s.Why == "" {
			t.Errorf("%s needs an explanation (doctor prints it)", s.Hook)
		}
	}
	for _, h := range []string{"SessionStart", "UserPromptSubmit", "Notification", "Stop", "SessionEnd"} {
		if by[h].Level != Required {
			t.Errorf("%s should be required", h)
		}
	}
	if by["StopFailure"].Level != Recommended || by["PreToolUse"].Level != Optional {
		t.Fatal("StopFailure fixes a gap (recommended); PreToolUse costs a process per tool call (optional)")
	}
	if by["Notification"].Matcher != NotifyMatcher {
		t.Fatal("Notification must be limited to the notifications that need the user")
	}
}

func TestCommandQuotesOnlyWhenNeeded(t *testing.T) {
	if got := Command("/usr/bin/beacon", "working"); got != "/usr/bin/beacon hook working" {
		t.Fatal(got)
	}
	if got := Command("C:/Program Files/b/beacon.exe", "ready"); got != `"C:/Program Files/b/beacon.exe" hook ready` {
		t.Fatal(got)
	}
}

func TestSnippetRoundTripsThroughParseSettings(t *testing.T) {
	out, err := Snippet(`C:/Program Files/beacon/beacon.exe`, SnippetOptions{PreToolUse: true, StopFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	found, err := ParseSettings(out)
	if err != nil || len(found) != len(Specs()) {
		t.Fatalf("%d found (%v), want %d", len(found), err, len(Specs()))
	}
	for _, f := range found {
		bin, ev, ok := f.Beacon()
		if !ok || bin != "C:/Program Files/beacon/beacon.exe" {
			t.Fatalf("%+v -> %q %q %v", f, bin, ev, ok)
		}
		if _, valid := ParseEvent(ev); !valid {
			t.Fatalf("%q", ev)
		}
	}
}

func TestParseSettingsIsTolerantAndFindsOnlyCommands(t *testing.T) {
	src := `{
	  "model": "x", "theme": "dark", "env": {"A": "1"},
	  "hooks": {
	    "PreToolUse": [
	      {"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk-rewrite.sh"}]},
	      {"hooks": [{"type": "command", "command": "/opt/beacon hook working"}]}
	    ],
	    "Stop": [{"hooks": [{"type": "prompt", "prompt": "is it done?"}, {"type": "http", "url": "https://x"}]}],
	    "Weird": "not an array"
	  }
	}`
	// A wrongly shaped event is a JSON type error: the caller reports it.
	if _, err := ParseSettings([]byte(src)); err == nil {
		t.Fatal("an event that isn't a list is reported, not silently skipped")
	}
	src = strings.Replace(src, `,
	    "Weird": "not an array"`, "", 1)
	found, err := ParseSettings([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("only command hooks count (prompt and http hooks are someone else's): %+v", found)
	}
	got := map[string]bool{}
	for _, f := range found {
		_, _, ok := f.Beacon()
		got[f.Command] = ok
	}
	if got["rtk-rewrite.sh"] || !got["/opt/beacon hook working"] {
		t.Fatalf("an unrelated PreToolUse hook must not be claimed as beacon's: %v", got)
	}
	if _, err := ParseSettings([]byte("{ nope")); err == nil {
		t.Fatal("invalid JSON is an error")
	}
	if f, err := ParseSettings([]byte(`{"theme":"dark"}`)); err != nil || len(f) != 0 {
		t.Fatal("a settings file with no hooks is fine")
	}
}

func TestBeaconCommandRecognition(t *testing.T) {
	cases := map[string]struct {
		bin, ev string
		ok      bool
	}{
		"beacon hook working":                     {"beacon", "working", true},
		`"C:\\Users\\a b\\beacon.exe" hook ready`: {`C:\\Users\\a b\\beacon.exe`, "ready", true},
		"/x/beacon hook session-end":              {"/x/beacon", "session-end", true},
		"/x/beacon-dev hook waiting":              {"/x/beacon-dev", "waiting", true},
		"beacon version":                          {"", "", false},
		"git hook run":                            {"", "", false},
		"myscript.sh":                             {"", "", false},
		"":                                        {"", "", false},
	}
	for cmd, want := range cases {
		bin, ev, ok := Found{Command: cmd}.Beacon()
		if ok != want.ok || bin != want.bin || ev != want.ev {
			t.Errorf("%q -> %q %q %v, want %+v", cmd, bin, ev, ok, want)
		}
	}
	_ = json.Valid
}

func TestBeaconWithClaimsARenamedBinaryOnlyForValidEvents(t *testing.T) {
	ours := func(bin string) bool { return bin == "/usr/local/bin/bcn" }
	if bin, ev, ok := (Found{Command: "/usr/local/bin/bcn hook working"}).BeaconWith(ours); !ok || bin != "/usr/local/bin/bcn" || ev != "working" {
		t.Fatalf("a renamed binary that is ours is claimed: %q %q %v", bin, ev, ok)
	}
	if _, _, ok := (Found{Command: "/usr/local/bin/bcn hook working"}).Beacon(); ok {
		t.Fatal("without the predicate the name decides")
	}
	if _, _, ok := (Found{Command: "/usr/local/bin/bcn hook explode"}).BeaconWith(ours); ok {
		t.Fatal("an event beacon doesn't accept isn't a beacon hook")
	}
	if _, _, ok := (Found{Command: "/other/tool hook working"}).BeaconWith(ours); ok {
		t.Fatal("someone else's binary isn't claimed")
	}
}
