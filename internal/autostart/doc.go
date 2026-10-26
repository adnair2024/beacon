// Package autostart starts `beacon daemon` at login: a systemd user unit on
// Linux and a Task Scheduler task on Windows. The text and arguments it
// generates are pure functions so they can be tested anywhere; only the thin
// calls to systemctl and schtasks are per-platform.
package autostart

// Backend installs, removes and inspects the login entry on one OS.
type Backend interface {
	// Name is how the mechanism is called in messages ("systemd user service").
	Name() string
	// Render returns what Install would set up, for --print.
	Render(o Options) (string, error)
	// Install sets it up and starts the daemon now.
	Install(o Options) error
	// Remove stops the daemon and deletes the entry. It is not an error for
	// nothing to be installed.
	Remove() error
	// Status describes the entry and whether it's running.
	Status() (string, error)
}

// Options says what to run.
type Options struct {
	Exe string // absolute path of the beacon binary
	// WorkDir, if set, is the directory the daemon starts in, which is where
	// it will look for a .env file. Empty means the platform default.
	WorkDir string
	// Env are variables to bake into the service (BEACON_CONFIG, BEACON_STATE_DIR).
	Env map[string]string
}
