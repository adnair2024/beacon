package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/logging"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/paths"
	"github.com/adnair2024/beacon/internal/state"
)

func newPomo() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pomo",
		Short: "Pomodoro timer",
		Long: `A focus/break timer whose phases show on the light. The timer is stored as
wall-clock times, so it keeps counting while beacon isn't running and
survives restarts. With no daemon or TUI running, these commands update the
light themselves.`,
	}
	var dry bool
	cmd.PersistentFlags().BoolVar(&dry, "dry-run", false, "fake devices and dry-run state")

	for _, v := range pomo.Verbs {
		v := v
		cmd.AddCommand(&cobra.Command{
			Use:   v.Name,
			Short: v.Short,
			Args:  cobra.NoArgs,
			RunE:  func(c *cobra.Command, _ []string) error { return runPomo(c, dry, v.Name) },
		})
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the timer",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, ps, store, err := pomoSetup(dry)
			if err != nil {
				return err
			}
			f, err := store.Load()
			if err != nil {
				return err
			}
			c.Println(pomoLine(pomo.Evaluate(pomo.Load(f), time.Now(), ps)))
			_ = cfg
			return nil
		},
	})
	return cmd
}

func pomoSetup(dry bool) (config.Config, pomo.Settings, *state.Store, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return cfg, pomo.Settings{}, nil, err
	}
	ps, err := pomo.SettingsFrom(cfg)
	if err != nil {
		return cfg, ps, nil, err
	}
	store, err := openStore(dry)
	return cfg, ps, store, err
}

func pomoLine(st pomo.Status) string {
	s := st.String()
	if st.Active() {
		s += fmt.Sprintf(" · %d/%d focus blocks this cycle · %d done", st.Dots(), st.LongEvery, st.Completed)
	}
	return s
}

func runPomo(c *cobra.Command, dry bool, verb string) error {
	cfg, ps, store, err := pomoSetup(dry)
	if err != nil {
		return err
	}
	st, err := pomo.Do(store, verb, time.Now(), ps)
	if err != nil {
		return err
	}
	c.Println(pomoLine(st))

	if store.EngineRunning() {
		return nil // the running engine notices the state change
	}
	log := logging.Discard()
	if dir, err := paths.StateDir(); err == nil {
		if l, closer, err := logging.Open(dir); err == nil {
			log = l
			defer closer.Close()
		}
	}
	if err := applyDirect(c, cfg, store, dry, log); err != nil {
		c.PrintErrln("warning: the timer changed but the light could not be updated:", err)
	}
	return nil
}
