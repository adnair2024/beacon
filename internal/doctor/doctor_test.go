package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/envfile"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/cloud"
	"github.com/adnair2024/beacon/internal/govee/fake"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

// healthy builds an Env in which every check passes. Tests then break one
// thing at a time.
func healthy(t *testing.T) Env {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "beacon")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	strip := fake.Devices()[0]
	strip.ID = "AA:BB:CC:DD:EE:FF:00:01"
	cache := config.CacheOf(strip.Caps)
	cfg := config.Default()
	cfg.Devices = []config.Device{{Name: "desk", ID: strip.ID, SKU: strip.SKU, Backend: "cloud", Caps: &cache}}

	store := state.Open(filepath.Join(dir, "state"))
	snippet, err := claude.Snippet(filepath.ToSlash(exe), claude.SnippetOptions{StopFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	return Env{
		Cfg: cfg, CfgPath: filepath.Join(dir, "config.toml"), CfgFound: true,
		KeySource: "from .env", KeyMasked: "****abcd",
		StateDir: filepath.Join(dir, "state"), Store: store, SchedDir: filepath.Join(dir, "schedules"),
		Exe: exe, GOOS: "linux",
		Claude:   []SettingsFile{{Path: "~/.claude/settings.json", Exists: true, Data: snippet}},
		List:     func(context.Context) ([]govee.Device, error) { return []govee.Device{strip}, nil },
		Online:   func(context.Context, govee.Device) error { return nil },
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
	}
}

// withEngine makes an engine appear to be running for the life of the test.
func withEngine(t *testing.T, e *Env) {
	t.Helper()
	release, err := e.Store.AcquireEngine()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
}

func run(e Env) []Result { return Run(context.Background(), e) }

func find(rs []Result, name string) []Result {
	var out []Result
	for _, r := range rs {
		if r.Name == name {
			out = append(out, r)
		}
	}
	return out
}

// has reports whether a result with the status and a detail containing sub exists.
func has(rs []Result, st Status, sub string) bool {
	for _, r := range rs {
		if r.Status == st && strings.Contains(r.Detail, sub) {
			return true
		}
	}
	return false
}

func dump(rs []Result) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "[%s] %s: %s\n", r.Status.Tag(), r.Name, r.Detail)
		for _, h := range r.Hints {
			fmt.Fprintf(&b, "      %s\n", h)
		}
	}
	return b.String()
}

func TestHealthySetupHasNoFindings(t *testing.T) {
	e := healthy(t)
	withEngine(t, &e)
	rs := run(e)
	if f, w := Summary(rs); f != 0 || w != 0 {
		t.Fatalf("a healthy setup must be clean:\n%s", dump(rs))
	}
	for _, name := range []string{"config", "api key", "state", "schedule", "colors", "api", "device desk", "hooks", "engine"} {
		if len(find(rs, name)) == 0 {
			t.Errorf("no %q check ran:\n%s", name, dump(rs))
		}
	}
}

func TestSummaryAndTags(t *testing.T) {
	rs := []Result{{Status: OK}, {Status: Info}, {Status: Warn}, {Status: Warn}, {Status: Fail}}
	if f, w := Summary(rs); f != 1 || w != 2 {
		t.Fatal(f, w)
	}
	if OK.Tag() != "ok" || Fail.Tag() != "FAIL" || Warn.Tag() != "warn" || Info.Tag() != "info" {
		t.Fatal("tags are plain ASCII words")
	}
}

// ---- config / key / .env ----

func TestConfigProblems(t *testing.T) {
	e := healthy(t)
	e.CfgFound = false
	if rs := run(e); !has(find(rs, "config"), Warn, "no config file") {
		t.Fatal(dump(rs))
	}
	e = healthy(t)
	e.CfgErr = errors.New("config.toml: line 3: bad value")
	if rs := run(e); !has(find(rs, "config"), Fail, "line 3") {
		t.Fatal(dump(rs))
	}
	e = healthy(t)
	e.Cfg.Claude.Mode = "bogus"
	e.Cfg.Pomo.Focus = "soon"
	rs := find(run(e), "config")
	if len(rs) != 1 || rs[0].Status != Fail || len(rs[0].Hints) != 2 || !strings.Contains(strings.Join(rs[0].Hints, " "), "claude.mode") {
		t.Fatalf("validation errors are listed as hints: %+v", rs)
	}
	e = healthy(t)
	e.Warnings = []string{`unknown key "claude.colour"`}
	if rs := run(e); !has(find(rs, "config"), Warn, "claude.colour") {
		t.Fatal(dump(rs))
	}
	e = healthy(t) // many problems: capped, with a pointer to the full list
	e.Cfg.Claude.Working, e.Cfg.Claude.Ready, e.Cfg.Claude.Waiting = "x1", "x2", "x3"
	e.Cfg.Pomo.FocusColor, e.Cfg.Pomo.ShortBreakColor, e.Cfg.Pomo.LongBreakColor = "x4", "x5", "x6"
	if r := find(run(e), "config")[0]; len(r.Hints) != 6 || !strings.Contains(r.Hints[5], "more") {
		t.Fatalf("%+v", r.Hints)
	}
}

func TestKeyMissingTellsYouHowToGetOne(t *testing.T) {
	e := healthy(t)
	e.KeySource = ""
	r := find(run(e), "api key")[0]
	if r.Status != Fail || !strings.Contains(strings.Join(r.Hints, " "), "Apply for API Key") || !strings.Contains(strings.Join(r.Hints, " "), ".env") {
		t.Fatalf("%+v", r)
	}
	if rs := run(e); !has(find(rs, "api"), Info, "no API key") {
		t.Fatal("the network check is skipped with a reason, not attempted")
	}
}

func TestDotenvFindings(t *testing.T) {
	e := healthy(t)
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("GOVEE_API_KEY=x\n"), 0o644)
	e.EnvFile = envfile.Result{Path: p, Set: []string{"GOVEE_API_KEY"}, Skipped: []string{"PATH"}, Errors: []error{errors.New("line 2: expected KEY=VALUE")}}
	rs := find(run(e), ".env")
	if !has(rs, Warn, "line 2") || !has(rs, Warn, "PATH") {
		t.Fatal(dump(rs))
	}
	if os.PathSeparator == '/' && !has(rs, Warn, "readable by other users") {
		t.Fatal("a world-readable .env holding a key is flagged")
	}
	// A clean .env that supplied the key is just OK; no .env at all says nothing.
	e = healthy(t)
	os.WriteFile(p, []byte("GOVEE_API_KEY=x\n"), 0o600)
	os.Chmod(p, 0o600) // WriteFile keeps the mode of a file that already exists
	e.EnvFile = envfile.Result{Path: p, Set: []string{"GOVEE_API_KEY"}}
	if rs := find(run(e), ".env"); len(rs) != 1 || rs[0].Status != OK {
		t.Fatal(dump(rs))
	}
	if rs := find(run(healthy(t)), ".env"); len(rs) != 0 {
		t.Fatal("no .env, no line")
	}
}

// ---- state / schedule ----

func TestStateDirProblems(t *testing.T) {
	e := healthy(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o600)
	e.StateDir = filepath.Join(blocker, "state") // a path through a regular file can't be created
	if rs := run(e); !has(find(rs, "state"), Fail, "can't create") {
		t.Fatal(dump(rs))
	}
	e = healthy(t)
	os.MkdirAll(e.StateDir, 0o700)
	os.WriteFile(filepath.Join(e.StateDir, "state.json.corrupt"), []byte("{"), 0o600)
	if rs := run(e); !has(find(rs, "state"), Warn, "set aside") {
		t.Fatal(dump(rs))
	}
}

func TestScheduleChecks(t *testing.T) {
	e := healthy(t)
	if rs := find(run(e), "schedule"); len(rs) != 1 || rs[0].Status != OK || !strings.Contains(rs[0].Detail, "built-in default") {
		t.Fatalf("with no file the shipped schedule is fine: %s", dump(rs))
	}
	os.MkdirAll(e.SchedDir, 0o755)
	path := filepath.Join(e.SchedDir, "workday.json")

	os.WriteFile(path, []byte("{\n  nope"), 0o644)
	if rs := run(e); !has(find(rs, "schedule"), Fail, "workday.json:2:") {
		t.Fatal(dump(rs))
	}
	os.WriteFile(path, []byte(`{"days":["mon","funday"],"entries":[{"at":"25:00"}]}`), 0o644)
	r := find(run(e), "schedule")[0]
	if r.Status != Fail || len(r.Hints) != 2 {
		t.Fatalf("%+v", r)
	}
	p := schedule.Default()
	p.SetEnabled(false)
	schedule.Save(e.SchedDir, "workday", p, nil)
	if rs := find(run(e), "schedule"); rs[0].Status != Info || !strings.Contains(rs[0].Detail, "disabled") {
		t.Fatal(dump(rs))
	}
	p.SetEnabled(true)
	p.Days = nil
	schedule.Save(e.SchedDir, "workday", p, nil)
	if rs := find(run(e), "schedule"); rs[0].Status != Warn || !strings.Contains(rs[0].Detail, "no days") {
		t.Fatal(dump(rs))
	}
	e.Cfg.Schedule.Profile = "../x" // already reported by the config check; no second complaint here
	if rs := find(run(e), "schedule"); len(rs) != 0 {
		t.Fatal(dump(rs))
	}
}

// ---- colors ----

func TestColorCollisions(t *testing.T) {
	if rs := find(run(healthy(t)), "colors"); len(rs) != 1 || rs[0].Status != OK {
		t.Fatalf("the shipped colors are all distinct: %s", dump(rs))
	}
	e := healthy(t)
	e.Cfg.Pomo.ShortBreakColor = e.Cfg.Claude.Working // the easy mistake: an orange break color on top of orange "working"
	rs := find(run(e), "colors")
	if len(rs) != 1 || !has(rs, Warn, "claude.working and pomo.short_break are the same color") {
		t.Fatal(dump(rs))
	}
	// Palette names resolve before comparing.
	e = healthy(t)
	e.Cfg.Palette = map[string]string{"mine": e.Cfg.Claude.Ready}
	e.Cfg.Pomo.FocusColor = "mine"
	if rs := run(e); !has(find(rs, "colors"), Warn, "claude.ready and pomo.focus are the same color") {
		t.Fatal(dump(rs))
	}
	// Close but not identical.
	e = healthy(t)
	e.Cfg.Claude.Waiting = "#2979FF"
	e.Cfg.Pomo.LongBreakColor = "#2F7FFF"
	if rs := run(e); !has(find(rs, "colors"), Warn, "very similar") {
		t.Fatal(dump(rs))
	}
	// The idle color only counts when it's actually used.
	e = healthy(t)
	e.Cfg.Idle.Color = e.Cfg.Claude.Working
	if rs := find(run(e), "colors"); rs[0].Status != OK {
		t.Fatalf("idle.action is leave, so idle.color is dormant: %s", dump(rs))
	}
	e.Cfg.Idle.Action = "color"
	if rs := run(e); !has(find(rs, "colors"), Warn, "idle") {
		t.Fatal(dump(rs))
	}
}

// ---- API / devices ----

func TestAPIResults(t *testing.T) {
	e := healthy(t)
	e.Offline = true
	if rs := run(e); !has(find(rs, "api"), Info, "--offline") || len(find(rs, "device desk")) != 0 {
		t.Fatalf("offline skips all network checks:\n%s", dump(rs))
	}
	e = healthy(t)
	e.List = func(context.Context) ([]govee.Device, error) { return nil, cloud.ErrUnauthorized }
	if r := find(run(e), "api")[0]; r.Status != Fail || !strings.Contains(r.Detail, "rejected") {
		t.Fatalf("%+v", r)
	}
	e.List = func(context.Context) ([]govee.Device, error) { return nil, &cloud.RateLimitError{RetryAfter: 30e9} }
	if r := find(run(e), "api")[0]; r.Status != Warn || !strings.Contains(r.Detail, "rate limited") {
		t.Fatalf("%+v", r)
	}
	e.List = func(context.Context) ([]govee.Device, error) { return nil, errors.New("dial tcp: i/o timeout") }
	r := find(run(e), "api")[0]
	if r.Status != Fail || !strings.Contains(strings.Join(r.Hints, " "), "openapi.api.govee.com") {
		t.Fatalf("a network failure names the one host that has to be reachable: %+v", r)
	}
}

func TestDeviceSelection(t *testing.T) {
	strip, white := fake.Devices()[0], fake.Devices()[1]
	e := healthy(t)
	e.Cfg.Devices = nil
	e.List = func(context.Context) ([]govee.Device, error) { return nil, nil }
	if rs := run(e); !has(find(rs, "devices"), Fail, "no devices on this account") {
		t.Fatal(dump(rs))
	}
	e.List = func(context.Context) ([]govee.Device, error) { return []govee.Device{strip}, nil }
	if rs := run(e); !has(find(rs, "devices"), Info, "only one on the account") {
		t.Fatal(dump(rs))
	}
	e.List = func(context.Context) ([]govee.Device, error) { return []govee.Device{strip, white}, nil }
	if rs := run(e); !has(find(rs, "devices"), Fail, "can't tell which to drive") {
		t.Fatal(dump(rs))
	}
}

func TestConfiguredDeviceProblems(t *testing.T) {
	e := healthy(t)
	e.Cfg.Devices[0].ID, e.Cfg.Devices[0].Name = "NOPE", "ghost"
	if rs := run(e); !has(find(rs, "device ghost"), Fail, "isn't on this Govee account") {
		t.Fatal(dump(rs))
	}
	// No cache: every hook would have to list devices first.
	e = healthy(t)
	e.Cfg.Devices[0].Caps = nil
	if rs := run(e); !has(find(rs, "device desk"), Warn, "aren't cached") || !strings.Contains(dump(find(rs, "device desk")), "beacon devices refresh") {
		t.Fatal(dump(rs))
	}
	// Stale cache: the live device now advertises something different.
	e = healthy(t)
	stale := *e.Cfg.Devices[0].Caps
	stale.SegmentCount = 15
	e.Cfg.Devices[0].Caps = &stale
	if rs := run(e); !has(find(rs, "device desk"), Warn, "out of date") {
		t.Fatal(dump(rs))
	}
	// A segment-count override that disagrees with the API.
	e = healthy(t)
	e.Cfg.Devices[0].Segments.Count = 8
	if rs := run(e); !has(find(rs, "device desk"), Warn, "overrides what the API reports (10)") {
		t.Fatal(dump(rs))
	}
	off := false
	e = healthy(t)
	e.Cfg.Devices[0].Segments.Enabled = &off
	if rs := run(e); !has(find(rs, "device desk"), Info, "switched off") {
		t.Fatal(dump(rs))
	}
}

func TestOfflineDeviceGetsTheWifiHint(t *testing.T) {
	e := healthy(t)
	e.Online = func(context.Context, govee.Device) error { return cloud.ErrOffline }
	r := find(run(e), "device desk")
	hints := dump(r)
	if !has(r, Warn, "reports it offline") || !strings.Contains(hints, "2.4 GHz") || !strings.Contains(hints, "captive portal") {
		t.Fatal(hints)
	}
	e.Online = func(context.Context, govee.Device) error { return errors.New("boom") }
	if rs := run(e); !has(find(rs, "device desk"), Warn, "couldn't read its state") {
		t.Fatal(dump(rs))
	}
}

// ---- hooks ----

// settings builds a Claude settings file whose hooks call exe, after mutating it.
func settings(t *testing.T, exe string, mutate func(map[string]any)) []SettingsFile {
	t.Helper()
	snippet, err := claude.Snippet(filepath.ToSlash(exe), claude.SnippetOptions{StopFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(snippet, &doc); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(doc)
	}
	b, _ := json.Marshal(doc)
	return []SettingsFile{{Path: "~/.claude/settings.json", Exists: true, Data: b}}
}

func hooksOf(doc map[string]any) map[string]any { return doc["hooks"].(map[string]any) }

func TestHooksMissingEverything(t *testing.T) {
	e := healthy(t)
	e.Claude = []SettingsFile{{Path: "~/.claude/settings.json"}, {Path: ".claude/settings.json"}}
	r := find(run(e), "hooks")[0]
	if r.Status != Fail || !strings.Contains(r.Detail, "no beacon hooks") || !strings.Contains(r.Detail, ".claude/settings.json") ||
		!strings.Contains(strings.Join(r.Hints, " "), "adding to any hooks you already have") {
		t.Fatalf("%+v", r)
	}
	// Someone else's hooks are not beacon's, and existing ones must be preserved.
	e.Claude = []SettingsFile{{Path: "s", Exists: true, Data: []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"rtk-rewrite.sh"}]}]}}`)}}
	if rs := run(e); !has(find(rs, "hooks"), Fail, "no beacon hooks") {
		t.Fatal(dump(rs))
	}
}

func TestHooksIndividualProblems(t *testing.T) {
	e := healthy(t)
	e.Claude = settings(t, e.Exe, func(d map[string]any) { delete(hooksOf(d), "Stop") })
	if rs := run(e); !has(find(rs, "hooks"), Fail, "missing the Stop hook") {
		t.Fatal(dump(rs))
	}
	e.Claude = settings(t, e.Exe, func(d map[string]any) { delete(hooksOf(d), "StopFailure") })
	rs := run(e)
	if !has(find(rs, "hooks"), Warn, "missing the StopFailure hook") || !strings.Contains(dump(rs), "API error") {
		t.Fatal("a missing recommended hook is a warning that says why:\n" + dump(rs))
	}
	if f, _ := Summary(rs); f != 0 {
		t.Fatal("a missing recommended hook isn't a failure")
	}
	// PreToolUse is optional: absent is fine, silently.
	if rs := run(healthy(t)); has(find(rs, "hooks"), Warn, "PreToolUse") {
		t.Fatal("optional hooks aren't nagged about")
	}
	// A Notification hook with no matcher fires for notifications that don't need you.
	e.Claude = settings(t, e.Exe, func(d map[string]any) {
		g := hooksOf(d)["Notification"].([]any)[0].(map[string]any)
		delete(g, "matcher")
	})
	rs = run(e)
	if !has(find(rs, "hooks"), Warn, "no matcher") || !strings.Contains(dump(rs), claude.NotifyMatcher) {
		t.Fatal(dump(rs))
	}
	e.Claude = settings(t, e.Exe, func(d map[string]any) { d["disableAllHooks"] = true })
	if rs := run(e); !has(find(rs, "hooks"), Fail, "disableAllHooks") {
		t.Fatal(dump(rs))
	}
	e.Claude = []SettingsFile{{Path: "s", Exists: true, Data: []byte("{ nope")}}
	if rs := run(e); !has(find(rs, "hooks"), Fail, "not valid JSON") {
		t.Fatal(dump(rs))
	}
}

func TestHooksSplitAcrossUserAndProjectFiles(t *testing.T) {
	e := healthy(t)
	var doc map[string]any
	json.Unmarshal(e.Claude[0].Data, &doc)
	all := hooksOf(doc)
	user, project := map[string]any{}, map[string]any{}
	i := 0
	for k, v := range all {
		if i%2 == 0 {
			user[k] = v
		} else {
			project[k] = v
		}
		i++
	}
	ub, _ := json.Marshal(map[string]any{"hooks": user})
	pb, _ := json.Marshal(map[string]any{"hooks": project})
	e.Claude = []SettingsFile{{Path: "user", Exists: true, Data: ub}, {Path: "project", Exists: true, Data: pb}}
	withEngine(t, &e)
	if rs := run(e); !has(find(rs, "hooks"), OK, "all required hooks") {
		t.Fatalf("hooks may live in several settings files:\n%s", dump(rs))
	}
}

func TestHookBinaryChecks(t *testing.T) {
	// The binary the hooks point at is gone.
	e := healthy(t)
	e.Claude = settings(t, e.Exe, func(d map[string]any) {
		for _, groups := range hooksOf(d) {
			for _, g := range groups.([]any) {
				for _, h := range g.(map[string]any)["hooks"].([]any) {
					m := h.(map[string]any)
					m["command"] = strings.Replace(m["command"].(string), filepath.ToSlash(e.Exe), "/nonexistent/beacon", 1)
				}
			}
		}
	})
	if rs := run(e); !has(find(rs, "hooks"), Fail, "doesn't exist: /nonexistent/beacon") {
		t.Fatal(dump(rs))
	}
	// A bare name that isn't on PATH here.
	e.Claude = settings(t, e.Exe, func(d map[string]any) {
		for _, groups := range hooksOf(d) {
			for _, g := range groups.([]any) {
				for _, h := range g.(map[string]any)["hooks"].([]any) {
					m := h.(map[string]any)
					m["command"] = strings.Replace(m["command"].(string), filepath.ToSlash(e.Exe), "beacon", 1)
				}
			}
		}
	})
	if rs := run(e); !has(find(rs, "hooks"), Warn, "isn't on PATH here") || !strings.Contains(dump(run(e)), "different PATH") {
		t.Fatal(dump(run(e)))
	}
	// ...and one that is, but isn't the binary doing the checking.
	other := filepath.Join(t.TempDir(), "beacon")
	os.WriteFile(other, []byte("x"), 0o755)
	e.LookPath = func(string) (string, error) { return other, nil }
	if rs := run(e); !has(find(rs, "hooks"), Warn, "the hooks run") {
		t.Fatal(dump(rs))
	}
	e.LookPath = func(string) (string, error) { return e.Exe, nil }
	if rs := run(e); has(find(rs, "hooks"), Warn, "the hooks run") || has(find(rs, "hooks"), Warn, "PATH") {
		t.Fatalf("the same binary reached by name is fine:\n%s", dump(rs))
	}
}

func TestBackslashesInAHookCommandAreFlagged(t *testing.T) {
	e := healthy(t)
	e.Stat = func(string) (os.FileInfo, error) { return os.Stat(e.Exe) } // pretend the Windows path exists
	e.Claude = []SettingsFile{{Path: "s", Exists: true, Data: []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"C:\\tools\\beacon.exe hook ready"}]}]}}`)}}
	rs := run(e)
	if !has(find(rs, "hooks"), Warn, "backslashes") || !strings.Contains(dump(rs), "--windows") {
		t.Fatal(dump(rs))
	}
}

// ---- engine ----

func TestEngineCheck(t *testing.T) {
	e := healthy(t)
	r := find(run(e), "engine")[0]
	if r.Status != Warn || !strings.Contains(strings.Join(r.Hints, " "), "beacon daemon") || !strings.Contains(strings.Join(r.Hints, " "), "150 ms") {
		t.Fatalf("%+v", r)
	}
	withEngine(t, &e)
	if r := find(run(e), "engine")[0]; r.Status != OK || !strings.Contains(r.Detail, "pid") {
		t.Fatalf("%+v", r)
	}
	e.Store = nil
	if r := find(run(e), "engine")[0]; r.Status != Info {
		t.Fatal(r)
	}
}
