//go:build windows

package autostart

import (
	"fmt"
	"os/exec"
	"strings"
)

// run is how schtasks is invoked; tests replace it.
var run = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

type taskScheduler struct{}

// New returns the backend for this OS.
func New() Backend { return taskScheduler{} }

func (taskScheduler) Name() string { return "Task Scheduler task" }

func (taskScheduler) Render(o Options) (string, error) {
	return "schtasks " + strings.Join(SchtasksCreateArgs(o), " "), nil
}

func (taskScheduler) Install(o Options) error {
	if out, err := run("schtasks", SchtasksCreateArgs(o)...); err != nil {
		return fmt.Errorf("creating the scheduled task failed (%v: %s)", err, out)
	}
	if out, err := run("schtasks", "/Run", "/TN", TaskName); err != nil {
		return fmt.Errorf("the task was created but couldn't be started now (%v: %s); it will start at your next login", err, out)
	}
	return nil
}

func (taskScheduler) Remove() error {
	_, _ = run("schtasks", "/End", "/TN", TaskName)
	// Deleting something that doesn't exist is not a problem.
	_, _ = run("schtasks", "/Delete", "/TN", TaskName, "/F")
	return nil
}

func (taskScheduler) Status() (string, error) {
	out, err := run("schtasks", "/Query", "/TN", TaskName)
	if err != nil {
		return "not installed", nil
	}
	return "installed: " + strings.Join(strings.Fields(out), " "), nil
}
