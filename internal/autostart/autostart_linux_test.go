//go:build linux

package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemctl records calls instead of running anything.
func fakeSystemctl(t *testing.T, fail map[string]bool) *[]string {
	t.Helper()
	var calls []string
	old := run
	run = func(name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		for k := range fail {
			if strings.Contains(line, k) {
				return "boom", errors.New("exit status 1")
			}
		}
		return "", nil
	}
	t.Cleanup(func() { run = old })
	return &calls
}

func TestInstallWritesTheUnitAndEnablesIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	calls := fakeSystemctl(t, nil)
	if err := New().Install(Options{Exe: "/usr/local/bin/beacon"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "systemd", "user", "beacon.service"))
	if err != nil || !strings.Contains(string(b), "ExecStart=/usr/local/bin/beacon daemon") {
		t.Fatalf("%v\n%s", err, b)
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable --now beacon.service"}
	if strings.Join(*calls, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", *calls)
	}
	// Installing again just replaces the file (idempotent).
	if err := New().Install(Options{Exe: "/usr/local/bin/beacon"}); err != nil {
		t.Fatal(err)
	}
}

func TestInstallReportsAMissingSystemdSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	fakeSystemctl(t, map[string]bool{"daemon-reload": true})
	err := New().Install(Options{Exe: "/x/beacon"})
	if err == nil || !strings.Contains(err.Error(), "systemd user session") || !strings.Contains(err.Error(), "beacon.service") {
		t.Fatalf("the unit is written, and the error says where and what's wrong: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "systemd", "user", "beacon.service")); statErr != nil {
		t.Fatal("the file should still be there for the user to enable by hand")
	}
}

func TestRemoveAndStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	calls := fakeSystemctl(t, nil)
	if s, err := New().Status(); err != nil || !strings.Contains(s, "not installed") {
		t.Fatalf("%q %v", s, err)
	}
	if err := New().Remove(); err != nil {
		t.Fatal("removing something that isn't installed is not an error:", err)
	}
	New().Install(Options{Exe: "/x/beacon"})
	if s, _ := New().Status(); !strings.Contains(s, "installed at") {
		t.Fatal(s)
	}
	*calls = nil
	if err := New().Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "systemd", "user", "beacon.service")); err == nil {
		t.Fatal("the unit file should be gone")
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0], "disable --now") {
		t.Fatalf("%v", *calls)
	}
}

func TestRenderForPrint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/x/.config")
	s, err := New().Render(Options{Exe: "/x/beacon"})
	if err != nil || !strings.HasPrefix(s, "# /home/x/.config/systemd/user/beacon.service\n") || !strings.Contains(s, "[Install]") {
		t.Fatalf("%v\n%s", err, s)
	}
}
