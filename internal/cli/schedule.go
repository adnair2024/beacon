package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/logging"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/paths"
)

// clock is the time source for commands that depend on "now"; tests replace it.
var clock = time.Now

func newSchedule() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "The time-of-day lighting schedule",
		Long: `The schedule sets the light by time of day on chosen weekdays. It is stored as
JSON in a schedules/ directory next to the config file (one file per profile,
chosen by schedule.profile in the config), so the TUI editor can rewrite it
freely and you can edit it by hand. Until that file exists the shipped workday
schedule is used.

The schedule is evaluated inside beacon, so it only takes effect while
'beacon daemon' or the TUI is running; beacon does not program Govee's
on-device schedules.`,
	}
	var dry bool
	cmd.PersistentFlags().BoolVar(&dry, "dry-run", false, "use the throwaway dry-run schedule and state")

	load := func() (config.Config, string, schedule.Profile, bool, error) {
		cfg, _, err := loadConfig()
		if err != nil {
			return cfg, "", schedule.Profile{}, false, err
		}
		dir, err := scheduleDir(dry)
		if err != nil {
			return cfg, "", schedule.Profile{}, false, err
		}
		p, exists, err := schedule.Load(dir, cfg.Schedule.Profile)
		return cfg, dir, p, exists, err
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the schedule and what is in force now",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, dir, p, exists, err := load()
			if err != nil {
				return err
			}
			path, _ := schedule.Path(dir, cfg.Schedule.Profile)
			src := path
			if !exists {
				src = "built-in default (no file yet; `beacon schedule init` writes " + path + ")"
			}
			tz := p.Timezone
			if tz == "" {
				tz = "system local"
			}
			state := "enabled"
			if !p.IsEnabled() {
				state = "DISABLED"
			}
			c.Printf("%s · %s · days %s · %s\n%s\n\n", cfg.Schedule.Profile, state, strings.Join(p.Days, ","), tz, src)
			rows, err := p.Timeline(cfg.UserPalette())
			if err != nil {
				c.PrintErrln("warning:", err)
			}
			info, _ := p.At(clock(), cfg.UserPalette())
			for _, r := range rows {
				mark := " "
				if info.Current != nil && info.Current.Entry.At == r.Entry.At {
					mark = "▸"
				}
				note := ""
				if r.Ignored {
					note = "  (ignored: after a power-off)"
				}
				c.Printf("%s %s  %-14s %s%s\n", mark, r.Entry.At, r.Entry.Name, describeRow(r), note)
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "next",
		Short: "Show the next schedule change",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, _, p, _, err := load()
			if err != nil {
				return err
			}
			now := clock()
			info, err := p.At(now, cfg.UserPalette())
			if err != nil {
				return err
			}
			switch {
			case !info.Enabled:
				c.Println("the schedule is disabled")
			case info.Next == nil:
				c.Println("no upcoming change (no enabled days or no entries)")
			default:
				c.Printf("next: %s at %s (in %s)\n", info.Next.Entry.Name, info.Next.Starts.Format("Mon 15:04"),
					schedule.HumanIn(info.Next.Starts.Sub(now)))
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "path",
		Short: "Print the schedule file path",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, dir, _, _, err := load()
			if err != nil {
				return err
			}
			p, err := schedule.Path(dir, cfg.Schedule.Profile)
			if err == nil {
				c.Println(p)
			}
			return err
		},
	}, &cobra.Command{
		Use:   "validate",
		Short: "Check the schedule file",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, _, p, exists, err := load()
			if err != nil {
				return err
			}
			errs := p.Validate(cfg.UserPalette())
			for _, e := range errs {
				c.PrintErrln("error:", e)
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d schedule error(s)", len(errs))
			}
			if exists {
				c.Println("schedule ok")
			} else {
				c.Println("schedule ok (built-in default)")
			}
			return nil
		},
	})

	var force bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write the default schedule file so you can edit it",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, dir, _, exists, err := load()
			if err != nil {
				return err
			}
			if exists && !force {
				return fmt.Errorf("the schedule file already exists; use --force to overwrite it (the old one is kept as .bak)")
			}
			if err := schedule.Save(dir, cfg.Schedule.Profile, schedule.Default(), cfg.UserPalette()); err != nil {
				return err
			}
			path, _ := schedule.Path(dir, cfg.Schedule.Profile)
			c.Println("wrote", path)
			return nil
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	cmd.AddCommand(initCmd)

	for _, enable := range []bool{true, false} {
		enable := enable
		verb := map[bool]string{true: "enable", false: "disable"}[enable]
		cmd.AddCommand(&cobra.Command{
			Use:   verb,
			Short: strings.ToUpper(verb[:1]) + verb[1:] + " the schedule",
			Args:  cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				cfg, dir, p, _, err := load()
				if err != nil {
					return err
				}
				p.SetEnabled(enable)
				if err := schedule.Save(dir, cfg.Schedule.Profile, p, cfg.UserPalette()); err != nil {
					return err
				}
				c.Printf("schedule %sd\n", verb)
				return applyAfterChange(c, cfg, dry)
			},
		})
	}
	return cmd
}

func describeRow(r schedule.Row) string {
	var parts []string
	e := r.Entry
	switch {
	case e.Power != nil && !*e.Power:
		parts = append(parts, "off")
	case e.Color != "":
		parts = append(parts, e.Color)
	case e.Kelvin != 0:
		parts = append(parts, fmt.Sprintf("%dK", e.Kelvin))
	}
	if e.Brightness != 0 {
		parts = append(parts, fmt.Sprintf("%d%%", e.Brightness))
	}
	if e.Power != nil && *e.Power {
		parts = append(parts, "on")
	}
	if len(parts) == 0 {
		return "(no change)"
	}
	return strings.Join(parts, " ")
}

// applyAfterChange pushes the new result to the light when no engine is
// running to notice the file change itself.
func applyAfterChange(c *cobra.Command, cfg config.Config, dry bool) error {
	store, err := openStore(dry)
	if err != nil {
		return err
	}
	if store.EngineRunning() {
		return nil
	}
	log := logging.Discard()
	if dir, err := paths.StateDir(); err == nil {
		if l, closer, err := logging.Open(dir); err == nil {
			log = l
			defer closer.Close()
		}
	}
	if err := applyDirect(c, cfg, store, dry, log); err != nil {
		c.PrintErrln("warning: the schedule changed but the light could not be updated:", err)
	}
	return nil
}
