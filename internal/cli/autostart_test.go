package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/autostart"
	"github.com/adnair2024/beacon/internal/envfile"
)

// keyedEnv is an env whose config holds the API key (a top-level key, so it
// must come before the [[devices]] table).
func keyedEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, "")
	cfg := "api_key = \"secret-key-1234\"\n[[devices]]\nname=\"desk\"\nid=\"FA:KE:00:00:00:00:00:01\"\nbackend=\"cloud\"\n"
	if err := os.WriteFile(e.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

type fakeAutostart struct {
	installed []autostart.Options
	removed   int
	failWith  error
}

func (f *fakeAutostart) Name() string { return "test service" }
func (f *fakeAutostart) Render(o autostart.Options) (string, error) {
	return "RENDERED exe=" + o.Exe + " wd=" + o.WorkDir, nil
}
func (f *fakeAutostart) Install(o autostart.Options) error {
	f.installed = append(f.installed, o)
	return f.failWith
}
func (f *fakeAutostart) Remove() error           { f.removed++; return nil }
func (f *fakeAutostart) Status() (string, error) { return "status: ok", nil }

func autostartRun(t *testing.T, e *env, res envfile.Result, shellKey string, fb *fakeAutostart, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	old := newAutostart
	newAutostart = func() autostart.Backend { return fb }
	t.Cleanup(func() { newAutostart = old })
	t.Setenv("BEACON_CONFIG", e.cfgPath)
	t.Setenv("BEACON_STATE_DIR", e.stateDir)
	t.Setenv("GOVEE_API_KEY", shellKey)
	var out, errb bytes.Buffer
	root := NewRoot("test", res)
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"autostart"}, args...))
	err = root.Execute()
	return out.String(), errb.String(), err
}

func TestAutostartInstallWithTheKeyInConfig(t *testing.T) {
	e := keyedEnv(t)
	fb := &fakeAutostart{}
	out, errOut, err := autostartRun(t, e, envfile.Result{}, "", fb, "install")
	if err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	if len(fb.installed) != 1 || fb.installed[0].Exe == "" || fb.installed[0].WorkDir != "" {
		t.Fatalf("%+v", fb.installed)
	}
	// The daemon is pointed at the same config and state this shell uses.
	if fb.installed[0].Env["BEACON_CONFIG"] != e.cfgPath || fb.installed[0].Env["BEACON_STATE_DIR"] != e.stateDir {
		t.Fatalf("%+v", fb.installed[0].Env)
	}
	if !strings.Contains(out, "installed the test service") || strings.Contains(out+errOut, "secret-key") {
		t.Fatalf("%q %q", out, errOut)
	}
	for k, v := range fb.installed[0].Env {
		if strings.Contains(strings.ToLower(k), "key") || strings.Contains(v, "secret") {
			t.Fatal("the key must never be baked into the service definition")
		}
	}
}

func TestAutostartWithTheKeyOnlyInTheShellIsRefused(t *testing.T) {
	e := newEnv(t, "")
	fb := &fakeAutostart{}
	_, _, err := autostartRun(t, e, envfile.Result{}, "shell-only-key", fb, "install")
	if err == nil || !strings.Contains(err.Error(), "won't have") || !strings.Contains(err.Error(), "config.toml") {
		t.Fatalf("a login daemon can't see your shell's environment: %v", err)
	}
	if len(fb.installed) != 0 {
		t.Fatal("nothing should be installed")
	}
	_, _, err = autostartRun(t, e, envfile.Result{}, "", fb, "install")
	if err == nil || !strings.Contains(err.Error(), "no API key") || len(fb.installed) != 0 {
		t.Fatalf("no key at all: %v", err)
	}
}

func TestAutostartWithTheKeyFromDotenvStartsInThatDirectory(t *testing.T) {
	e := newEnv(t, "")
	fb := &fakeAutostart{}
	wd, _ := os.Getwd()
	_, errOut, err := autostartRun(t, e, envfile.Result{Path: ".env", Set: []string{"GOVEE_API_KEY"}}, "from-dotenv-key", fb, "install")
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.installed) != 1 || fb.installed[0].WorkDir != wd {
		t.Fatalf("the daemon starts where .env is: %+v (cwd %s)", fb.installed, wd)
	}
	if !strings.Contains(errOut, "start in that directory") || strings.Contains(errOut, "from-dotenv-key") {
		t.Fatal(errOut)
	}
}

func TestAutostartPrintChangesNothing(t *testing.T) {
	e := keyedEnv(t)
	fb := &fakeAutostart{}
	out, _, err := autostartRun(t, e, envfile.Result{}, "", fb, "install", "--print")
	if err != nil || !strings.Contains(out, "RENDERED exe=") || len(fb.installed) != 0 {
		t.Fatalf("%q %v %+v", out, err, fb.installed)
	}
}

func TestAutostartInstallFailureIsReported(t *testing.T) {
	e := keyedEnv(t)
	fb := &fakeAutostart{failWith: os.ErrPermission}
	if _, _, err := autostartRun(t, e, envfile.Result{}, "", fb, "install"); err == nil {
		t.Fatal("an install error must surface")
	}
}

func TestAutostartRemoveAndStatus(t *testing.T) {
	e := newEnv(t, "")
	fb := &fakeAutostart{}
	if out, _, err := autostartRun(t, e, envfile.Result{}, "", fb, "remove"); err != nil || fb.removed != 1 || !strings.Contains(out, "removed the test service") {
		t.Fatalf("%q %v", out, err)
	}
	if out, _, err := autostartRun(t, e, envfile.Result{}, "", fb, "status"); err != nil || !strings.Contains(out, "status: ok") {
		t.Fatalf("%q %v", out, err)
	}
}
