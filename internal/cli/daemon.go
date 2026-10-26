package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/daemon"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/logging"
	"github.com/adnair2024/beacon/internal/paths"
	"github.com/adnair2024/beacon/internal/state"
)

// coalesceWindow batches bursts of changes (a hook flurry) into one command.
const coalesceWindow = 500 * time.Millisecond

func newDaemon() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the engine loop with no UI (for autostart)",
		Long: `Watches the state file and config, decides what the light should show, and
drives it. With the daemon running, 'beacon hook' only writes state and returns
immediately. Only one daemon or TUI can run at a time.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}
			if errs := cfg.Validate(); len(errs) > 0 {
				return errs[0]
			}
			cfgPath, err := paths.ConfigFile()
			if err != nil {
				return err
			}
			store, err := openStore(dry)
			if err != nil {
				return err
			}
			schedDir, err := scheduleDir(dry)
			if err != nil {
				return err
			}
			if store.EngineRunning() { // fail before any network calls
				return fmt.Errorf("%w (pid %s)", state.ErrEngineRunning, store.EnginePID())
			}
			stateDir, _ := paths.StateDir()
			log, closer, err := logging.Open(stateDir)
			if err != nil {
				return err
			}
			defer closer.Close()

			b, err := newBackend(cfg, dry, c.OutOrStdout())
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			setup, cancel := context.WithTimeout(ctx, 10*time.Second)
			devs, err := resolveTargets(setup, b, cfg)
			cancel()
			if err != nil {
				return err
			}
			var group govee.Multi
			for _, d := range devs {
				group = append(group, govee.Named{Name: d.Name, Light: b.light(d)})
			}
			retry := daemon.NewRetrier()
			light := govee.NewCoalescer(group, coalesceWindow, func(err error) {
				log.Error("apply", "err", err)
				retry.Fail()
			})

			log.Info("daemon starting", "devices", describe(devs), "dry_run", dry)
			c.PrintErrf("beacon daemon running for %s (ctrl-c to stop); log: %s/%s\n", describe(devs), stateDir, logging.File)
			err = daemon.Run(ctx, daemon.Options{
				Store: store, ConfigPath: cfgPath, Light: light, Log: log, Retry: retry, Caps: firstCaps(devs),
				ScheduleDir: schedDir, LoadSchedule: scheduleLoader(schedDir),
				LoadConfig: func() (config.Config, error) {
					cfg, _, err := loadConfig()
					return cfg, err
				},
			})
			if errors.Is(err, state.ErrEngineRunning) {
				return err
			}
			log.Info("daemon stopped")
			return err
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "fake devices and dry-run state; print commands")
	return cmd
}
