package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/state"
)

type env struct {
	t        *testing.T
	stateDir string
	cfgPath  string
}

func newEnv(t *testing.T, extraCfg string) *env {
	t.Helper()
	e := &env{t: t, stateDir: t.TempDir(), cfgPath: filepath.Join(t.TempDir(), "c.toml")}
	cfg := "[[devices]]\nname=\"desk\"\nid=\"FA:KE:00:00:00:00:00:01\"\nbackend=\"cloud\"\n" + extraCfg
	if err := os.WriteFile(e.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

// run executes the CLI and returns stdout and stderr separately.
func (e *env) run(stdin string, args ...string) (stdout, stderr string, err error) {
	e.t.Setenv("BEACON_CONFIG", e.cfgPath)
	e.t.Setenv("BEACON_STATE_DIR", e.stateDir)
	e.t.Setenv("GOVEE_API_KEY", "")
	var out, errb bytes.Buffer
	root := NewRoot("test")
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errb.String(), err
}

func (e *env) dryState() state.File {
	f, err := state.Open(filepath.Join(e.stateDir, "dry-run")).Load()
	if err != nil {
		e.t.Fatal(err)
	}
	return f
}

const payload = `{"session_id":"sess-1","hook_event_name":"UserPromptSubmit","cwd":"/work","transcript_path":"/secret/t.jsonl"}`

func TestHookRecordsAndAppliesDirectlyWithNoEngine(t *testing.T) {
	e := newEnv(t, "")
	stdout, stderr, err := e.run(payload, "hook", "working", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Fatalf("hook must never print to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "dry-run [desk]: color #FF8C00") {
		t.Fatalf("expected a direct apply, got %q", stderr)
	}
	f := e.dryState()
	if s := f.Claude.Sessions["sess-1"]; s.State != claude.Working || s.Cwd != "/work" {
		t.Fatalf("%+v", f.Claude.Sessions)
	}
	if f.LastApplied == nil {
		t.Fatal("last_applied should be recorded")
	}
	// Aggregation through the CLI: waiting beats working across sessions.
	e.run(`{"session_id":"sess-2"}`, "hook", "waiting", "--dry-run")
	if f := e.dryState(); f.Claude.Status != claude.Waiting {
		t.Fatal(f.Claude.Status)
	}
	e.run(`{"session_id":"sess-2"}`, "hook", "session-end", "--dry-run")
	if f := e.dryState(); f.Claude.Status != claude.Working || len(f.Claude.Sessions) != 1 {
		t.Fatalf("%+v", f.Claude)
	}
}

func TestHookNeverFailsOrPrintsOnBadInput(t *testing.T) {
	e := newEnv(t, "")
	cases := [][]string{
		{"hook"},                      // no event
		{"hook", "explode"},           // unknown event
		{"hook", "working", "x", "y"}, // extra args
	}
	for _, args := range cases {
		stdout, _, err := e.run("not json at all", append(args, "--dry-run")...)
		if err != nil || stdout != "" {
			t.Errorf("%v: err=%v stdout=%q", args, err, stdout)
		}
	}
	// Garbage stdin on a valid event falls back to the manual session.
	if _, _, err := e.run("\x00\xff garbage", "hook", "ready", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.dryState().Claude.Sessions[claude.ManualSession]; !ok {
		t.Fatal("garbage payload should use the manual session")
	}
}

func TestHookIgnoresEverythingWhenModeOff(t *testing.T) {
	e := newEnv(t, "[claude]\nmode=\"off\"\n")
	stdout, stderr, err := e.run(payload, "hook", "working", "--dry-run")
	if err != nil || stdout != "" || stderr != "" {
		t.Fatal(stdout, stderr, err)
	}
	if len(e.dryState().Claude.Sessions) != 0 {
		t.Fatal("mode=off must not even record sessions")
	}
}

func TestHookBrokenConfigStillExitsZero(t *testing.T) {
	e := newEnv(t, "")
	os.WriteFile(e.cfgPath, []byte("this is = [not toml"), 0o600)
	stdout, _, err := e.run(payload, "hook", "working", "--dry-run")
	if err != nil || stdout != "" {
		t.Fatal(stdout, err)
	}
}

func TestHookJustRecordsWhenEngineRunning(t *testing.T) {
	e := newEnv(t, "")
	// The real (non-dry) state dir: pretend a daemon holds the engine lock.
	// Without an API key a direct apply would fail loudly in the log, so the
	// observable difference is that the session is recorded and nothing else.
	store := state.Open(e.stateDir)
	release, err := store.AcquireEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	stdout, stderr, err := e.run(payload, "hook", "working")
	if err != nil || stdout != "" || stderr != "" {
		t.Fatal(stdout, stderr, err)
	}
	f, _ := store.Load()
	if f.Claude.Sessions["sess-1"].State != claude.Working {
		t.Fatalf("%+v", f.Claude)
	}
	if f.LastApplied != nil {
		t.Fatal("hook must leave driving the light to the running engine")
	}
}

func TestHooksPrintSnippet(t *testing.T) {
	e := newEnv(t, "")
	out, _, err := e.run("", "hooks", "print", "--path", "/usr/local/bin/beacon")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Type, Command string }
		}
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	want := map[string]string{"SessionStart": "session-start", "UserPromptSubmit": "working",
		"Notification": "waiting", "Stop": "ready", "SessionEnd": "session-end", "StopFailure": "ready"}
	for name, ev := range want {
		g := doc.Hooks[name]
		if len(g) != 1 || g[0].Hooks[0].Type != "command" || g[0].Hooks[0].Command != "/usr/local/bin/beacon hook "+ev {
			t.Errorf("%s: %+v", name, g)
		}
	}
	// Only notifications that mean "Claude needs you" turn the light blue.
	if m := doc.Hooks["Notification"][0].Matcher; m != claude.NotifyMatcher || !strings.Contains(m, "permission_prompt") {
		t.Fatalf("Notification needs a matcher: %q", m)
	}
	if doc.Hooks["Stop"][0].Matcher != "" {
		t.Fatal("matchers appear only where they're wanted")
	}
	if _, ok := doc.Hooks["PreToolUse"]; ok {
		t.Fatal("PreToolUse is opt-in")
	}
	out, _, _ = e.run("", "hooks", "print", "--path", "/b", "--pre-tool-use", "--no-stop-failure")
	if !strings.Contains(out, `"PreToolUse"`) || strings.Contains(out, `"StopFailure"`) {
		t.Fatalf("flags should add PreToolUse and drop StopFailure:\n%s", out)
	}
}

func TestHooksPrintWindowsPathUsesForwardSlashesAndQuotes(t *testing.T) {
	e := newEnv(t, "")
	out, _, err := e.run("", "hooks", "print", "--windows", "--path", `C:\Program Files\beacon\beacon.exe`)
	if err != nil {
		t.Fatal(err)
	}
	// Backslashes would be eaten as escapes by bash, so they become slashes;
	// the space still needs quoting.
	if !strings.Contains(out, `\"C:/Program Files/beacon/beacon.exe\" hook working`) {
		t.Fatalf("%s", out)
	}
	var doc map[string]any
	if json.Unmarshal([]byte(out), &doc) != nil {
		t.Fatal("not valid JSON")
	}
}

func TestDaemonRefusesWhenEngineRunning(t *testing.T) {
	e := newEnv(t, "")
	store := state.Open(filepath.Join(e.stateDir, "dry-run"))
	release, err := store.AcquireEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, _, err = e.run("", "daemon", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal(err)
	}
}

func openStoreFor(t *testing.T, e *env) (*state.Store, string) {
	t.Helper()
	dir := filepath.Join(e.stateDir, "dry-run")
	return state.Open(dir), dir
}
