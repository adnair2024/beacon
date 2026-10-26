package cli

import (
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/modes/pomo"
)

func TestPomoLifecycleDryRun(t *testing.T) {
	e := newEnv(t, "")
	step := func(args ...string) (string, string) {
		t.Helper()
		out, errOut, err := e.run("", append([]string{"pomo"}, append(args, "--dry-run")...)...)
		if err != nil {
			t.Fatalf("pomo %v: %v", args, err)
		}
		return out, errOut
	}

	out, errOut := step("status")
	if !strings.HasPrefix(out, "idle") {
		t.Fatal(out)
	}

	out, errOut = step("start")
	if !strings.Contains(out, "focus · 25:00 left") || !strings.Contains(out, "0/4 focus blocks") {
		t.Fatal(out)
	}
	if !strings.Contains(errOut, "dry-run [desk]: color #FF4FA3") || !strings.Contains(errOut, "segments 0-9 #FF4FA3") {
		t.Fatalf("starting should light the strip pink, as a full bar (no engine running): %q", errOut)
	}
	if f := e.dryState(); f.Pomo == nil || f.Pomo.Phase != pomo.Focus || f.Pomo.State != pomo.Running {
		t.Fatalf("%+v", f.Pomo)
	}

	if _, _, err := e.run("", "pomo", "start", "--dry-run"); err == nil {
		t.Fatal("starting a running timer should be an error")
	}

	out, errOut = step("pause")
	if !strings.Contains(out, "focus paused") || !strings.Contains(errOut, "segment-brightness 0-9 20") {
		t.Fatalf("on a segmented strip a paused timer is the bar at 20%%: %q %q", out, errOut)
	}
	if _, _, err := e.run("", "pomo", "pause", "--dry-run"); err == nil {
		t.Fatal("pausing a paused timer should be an error")
	}

	out, errOut = step("resume")
	if !strings.Contains(out, "focus ·") || !strings.Contains(errOut, "segment-brightness 0-9 100") {
		t.Fatalf("%q %q", out, errOut)
	}

	out, errOut = step("skip")
	if !strings.Contains(out, "short break ·") || !strings.Contains(errOut, "color #1DE9B6") {
		t.Fatalf("skipping focus starts a (teal) short break: %q %q", out, errOut)
	}

	out, _ = step("stop")
	if out != "idle\n" {
		t.Fatalf("%q", out)
	}
	if f := e.dryState(); f.Pomo == nil || f.Pomo.Phase != pomo.Idle {
		t.Fatalf("%+v", f.Pomo)
	}
	for _, verb := range []string{"pause", "resume", "skip"} {
		if _, _, err := e.run("", "pomo", verb, "--dry-run"); err == nil {
			t.Errorf("%s on an idle timer should be an error", verb)
		}
	}
}

func TestPomoWithEngineRunningOnlyWritesState(t *testing.T) {
	e := newEnv(t, "")
	store, _ := openStoreFor(t, e)
	release, err := store.AcquireEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	out, errOut, err := e.run("", "pomo", "start", "--dry-run")
	if err != nil || !strings.Contains(out, "focus") || strings.Contains(errOut, "dry-run [") {
		t.Fatalf("with an engine running the CLI must not touch the light: %q %q %v", out, errOut, err)
	}
}

func TestPomoBadConfigIsAnError(t *testing.T) {
	e := newEnv(t, "[pomo]\nfocus = \"soon\"\n")
	if _, _, err := e.run("", "pomo", "start", "--dry-run"); err == nil || !strings.Contains(err.Error(), "pomo.focus") {
		t.Fatal(err)
	}
}
