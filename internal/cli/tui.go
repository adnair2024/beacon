package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/daemon"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/logging"
	"github.com/adnair2024/beacon/internal/paths"
	"github.com/adnair2024/beacon/internal/state"
	"github.com/adnair2024/beacon/internal/tui"
)

func newTUI() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the terminal UI (also runs the engine unless a daemon already does)",
		Args:  cobra.NoArgs,
	}
	var dry bool
	cmd.Flags().BoolVar(&dry, "dry-run", false, "fake devices and dry-run state; nothing touches a real light")
	cmd.RunE = func(c *cobra.Command, _ []string) error { return runTUI(c, dry) }
	return cmd
}

// runTUI picks the mode:
//   - a daemon/other TUI holds the engine lock -> viewer;
//   - no usable device or key                  -> offline (still useful: colors, preview);
//   - otherwise                                -> this TUI runs the engine.
func runTUI(c *cobra.Command, dry bool) error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	cfgPath, err := paths.ConfigFile()
	if err != nil {
		return err
	}
	store, err := openStore(dry)
	if err != nil {
		return err
	}
	stateDir, err := paths.StateDir()
	if err != nil {
		return err
	}
	schedDir, err := scheduleDir(dry)
	if err != nil {
		return err
	}
	o := tui.Options{Cfg: cfg, ConfigPath: cfgPath, Store: store, Dry: dry, SchedDir: schedDir,
		LogPath: filepath.Join(stateDir, logging.File)}

	// Nothing may print to the terminal while the TUI owns it, so dry-run
	// "commands" are discarded here (the dashboard shows the result).
	b, berr := newBackend(cfg, dry, io.Discard)
	var targets []govee.Device
	var terr error
	if berr == nil {
		ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
		targets, terr = resolveTargets(ctx, b, cfg)
		cancel()
	}
	o.Targets = targets

	o.Mode, o.Note = chooseMode(store.EngineRunning(), store.EnginePID(), berr, terr)
	if o.Mode == tui.ModeEngine {
		o.Engine = engineFor(cfgPath, store, b, targets, stateDir, schedDir)
	}
	return tui.Run(o)
}

// chooseMode decides who drives the light. A running engine wins (we become
// a viewer); with no usable backend or device we stay useful offline; else we
// are the engine.
func chooseMode(engineRunning bool, pid string, backendErr, targetsErr error) (tui.Mode, string) {
	switch {
	case engineRunning:
		return tui.ModeViewer, "daemon (pid " + pid + ")"
	case backendErr != nil:
		return tui.ModeOffline, backendErr.Error()
	case targetsErr != nil:
		return tui.ModeOffline, targetsErr.Error()
	}
	return tui.ModeEngine, ""
}

// engineFor builds the engine loop the TUI runs beside itself. Errors are
// logged to the file and also handed to onErr for the status bar.
func engineFor(cfgPath string, store *state.Store, b *backend, targets []govee.Device, stateDir, schedDir string) func(context.Context, func(error)) error {
	return func(ctx context.Context, onErr func(error)) error {
		log, closer, err := logging.Open(stateDir)
		if err != nil {
			return fmt.Errorf("log: %w", err)
		}
		defer closer.Close()

		var group govee.Multi
		for _, d := range targets {
			group = append(group, govee.Named{Name: d.Name, Light: b.light(d)})
		}
		retry := daemon.NewRetrier()
		light := govee.NewCoalescer(group, coalesceWindow, func(err error) {
			log.Error("apply", "err", err)
			onErr(err)
			retry.Fail()
		})
		log.Info("tui engine starting", "devices", describe(targets))
		return daemon.Run(ctx, daemon.Options{
			Store: store, ConfigPath: cfgPath, Light: light, Log: log, Retry: retry, OnError: onErr, Caps: firstCaps(targets),
			ScheduleDir: schedDir, LoadSchedule: scheduleLoader(schedDir),
			LoadConfig: func() (config.Config, error) {
				cfg, _, err := loadConfig()
				return cfg, err
			},
		})
	}
}
