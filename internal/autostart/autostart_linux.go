//go:build linux

package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adnair2024/beacon/internal/fsutil"
)

// run is how systemctl is invoked; tests replace it.
var run = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

type systemd struct{}

// New returns the backend for this OS.
func New() Backend { return systemd{} }

func (systemd) Name() string { return "systemd user service" }

const unitName = "beacon.service"

func unitPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", unitName), nil
}

func (systemd) Render(o Options) (string, error) {
	p, err := unitPath()
	if err != nil {
		return "", err
	}
	return "# " + p + "\n" + SystemdUnit(o), nil
}

func (systemd) Install(o Options) error {
	p, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".beacon-unit-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(SystemdUnit(o)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	if err := fsutil.Replace(tmp.Name(), p); err != nil {
		return err
	}
	if out, err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("wrote %s, but `systemctl --user daemon-reload` failed (%v: %s); is a systemd user session available?", p, err, out)
	}
	if out, err := run("systemctl", "--user", "enable", "--now", unitName); err != nil {
		return fmt.Errorf("wrote %s, but enabling it failed (%v: %s)", p, err, out)
	}
	return nil
}

func (systemd) Remove() error {
	p, err := unitPath()
	if err != nil {
		return err
	}
	// Stopping something that was never installed is not a problem.
	_, _ = run("systemctl", "--user", "disable", "--now", unitName)
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _ = run("systemctl", "--user", "daemon-reload")
	return nil
}

func (systemd) Status() (string, error) {
	p, err := unitPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err != nil {
		return "not installed (no " + p + ")", nil
	}
	enabled, _ := run("systemctl", "--user", "is-enabled", unitName)
	active, _ := run("systemctl", "--user", "is-active", unitName)
	return fmt.Sprintf("installed at %s: %s, %s", p, orUnknown(enabled), orUnknown(active)), nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
