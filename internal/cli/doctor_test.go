package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee/fake"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/state"
)

// doctorRun executes `beacon doctor` against the env, never reading the real
// Claude Code settings: CLAUDE_CONFIG_DIR points at claudeDir.
func doctorRun(t *testing.T, e *env, claudeDir, key string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("BEACON_CONFIG", e.cfgPath)
	t.Setenv("BEACON_STATE_DIR", e.stateDir)
	t.Setenv("GOVEE_API_KEY", key)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	var out, errb bytes.Buffer
	root := NewRoot("test")
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"doctor"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestDoctorReportsABrokenSetupAndFails(t *testing.T) {
	e := newEnv(t, "")
	out, err := doctorRun(t, e, t.TempDir(), "", "--offline")
	if err == nil || !strings.Contains(err.Error(), "problem") {
		t.Fatalf("a broken setup exits non-zero: %v", err)
	}
	for _, want := range []string{
		"beacon doctor · test", "[FAIL] api key", "Apply for API Key", // no key, with how to get one
		"[FAIL] hooks", "beacon hooks print", // no hooks installed
		"[warn] device", // (device desk has no cached capabilities, only shown online) or engine
	} {
		if want == "[warn] device" {
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "skipped (--offline)") {
		t.Fatal("--offline says it skipped the network checks")
	}
	if strings.Contains(out, "dummy-key-9999") {
		t.Fatal("never prints a key")
	}
}

func TestDoctorHealthyFakeSetup(t *testing.T) {
	e := newEnv(t, "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A config whose device (matching the fake strip) has its capabilities cached.
	strip := fake.Devices()[0]
	cache := config.CacheOf(strip.Caps)
	cfg := config.Default()
	cfg.Devices = []config.Device{{Name: "desk", ID: strip.ID, SKU: strip.SKU, Backend: "cloud", Caps: &cache}}
	if err := config.Save(e.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	// Hooks installed and pointing at this very binary.
	claudeDir := t.TempDir()
	snippet, err := claude.Snippet(filepath.ToSlash(exe), claude.SnippetOptions{StopFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), snippet, 0o600); err != nil {
		t.Fatal(err)
	}
	// An engine "running" in the dry-run state dir.
	release, err := state.Open(filepath.Join(e.stateDir, "dry-run")).AcquireEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	out, err := doctorRun(t, e, claudeDir, "dummy-key-9999", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "everything looks good") || strings.Contains(out, "[FAIL]") || strings.Contains(out, "[warn]") {
		t.Fatalf("%s", out)
	}
	for _, want := range []string{"[ ok ] device desk", "segments×10", "[ ok ] hooks", "[ ok ] engine", "[ ok ] api "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "dummy-key-9999") {
		t.Fatal("never prints a key")
	}
}

func TestDoctorWarningsDoNotFail(t *testing.T) {
	e := newEnv(t, "") // the desk has no cache -> a warning in dry-run; no engine -> a warning
	exe, _ := os.Executable()
	claudeDir := t.TempDir()
	snippet, _ := claude.Snippet(filepath.ToSlash(exe), claude.SnippetOptions{StopFailure: true})
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), snippet, 0o600)
	out, err := doctorRun(t, e, claudeDir, "dummy-key-9999", "--dry-run")
	if err != nil {
		t.Fatalf("warnings alone must exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn] engine") || !strings.Contains(out, "warning") || !strings.Contains(out, "beacon devices refresh") {
		t.Fatalf("%s", out)
	}
}
