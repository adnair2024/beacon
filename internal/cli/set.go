package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/state"
)

// overrideExpiry returns when a new override ends. forFlag is the --for value
// if the user passed one; otherwise the config default applies. A zero
// duration means "until cleared".
func overrideExpiry(now time.Time, forFlag *time.Duration, def string) (time.Time, error) {
	d := time.Duration(0)
	if forFlag != nil {
		d = *forFlag
	} else {
		var err error
		if d, err = time.ParseDuration(def); err != nil {
			return time.Time{}, fmt.Errorf("override.default_duration: %w", err)
		}
	}
	if d <= 0 {
		return time.Time{}, nil
	}
	return now.Add(d), nil
}

// setFlags are shared by `set` and `set segments`.
type setFlags struct {
	brightness int
	dryRun     bool
	forDur     time.Duration
	device     string
}

func (f *setFlags) register(cmd *cobra.Command, withBrightness bool) {
	if withBrightness {
		cmd.Flags().IntVar(&f.brightness, "brightness", 100, "brightness percent (1-100)")
	}
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "use fake devices and the dry-run state; print commands")
	cmd.Flags().DurationVar(&f.forDur, "for", 0, "override duration (0 = until cleared); default from config")
	cmd.Flags().StringVar(&f.device, "device", "", "device name or id (default: configured devices, or the only one)")
}

// runSet applies a state built by build, and records it as the manual
// override unless --device was given.
func runSet(c *cobra.Command, f *setFlags, build func(config.Config) (govee.State, error)) error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	forSet := c.Flags().Changed("for")
	if f.device != "" && forSet {
		return fmt.Errorf("--for can't be combined with --device: overrides are global, not per device")
	}
	st, err := build(cfg)
	if err != nil {
		return err
	}
	var forPtr *time.Duration
	if forSet {
		forPtr = &f.forDur
	}
	expiry, err := overrideExpiry(time.Now(), forPtr, cfg.Override.DefaultDuration)
	if err != nil {
		return err
	}
	b, err := newBackend(cfg, f.dryRun, c.OutOrStdout())
	if err != nil {
		return err
	}
	store, err := openStore(f.dryRun)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	listed, err := b.list(ctx)
	if err != nil {
		return err
	}
	devs, err := pick(listed, cfg, f.device)
	if err != nil {
		return err
	}
	if err := applyNow(ctx, b, devs, st, store); err != nil {
		return err
	}
	if f.device != "" {
		return nil
	}
	if err := store.Update(func(sf *state.File) error {
		sf.Override = &state.Override{State: st, ExpiresAt: expiry}
		return nil
	}); err != nil {
		return err
	}
	if expiry.IsZero() {
		c.Println("override active until `beacon clear`")
	} else {
		c.Printf("override active until %s (`beacon clear` to cancel)\n", fmtClock(expiry))
	}
	return nil
}

const overrideHelp = `Set the light and record a manual override, the highest-priority layer.
The override lasts override.default_duration (15m by default); --for changes
that and --for 0 keeps it until 'beacon clear'.

With --device the command is a one-off poke at that device: it is applied now
but no override is recorded, because overrides are global.`

func newSet() *cobra.Command {
	var f setFlags
	cmd := &cobra.Command{
		Use:   "set <color|kelvin|on|off>",
		Short: "Set the light manually (a manual override)",
		Long:  overrideHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runSet(c, &f, func(cfg config.Config) (govee.State, error) {
				return govee.ParseState(args[0], f.brightness, cfg.UserPalette())
			})
		},
	}
	f.register(cmd, true)

	var sf setFlags
	seg := &cobra.Command{
		Use:   "segments <spec>",
		Short: "Set individual segments, e.g. \"0-4=orange,5-9=#FF4FA3@30\"",
		Long: overrideHelp + `

The spec is comma-separated range=color items: a range is N or A-B (segment 0 is
the first one; see devices.segments.reverse for a strip mounted the other way
round), the color is anything 'set' accepts, and an optional @N sets that
item's brightness (1-100). Segments not mentioned keep what they show. Only
devices that advertise segments take this; the count comes from the API.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runSet(c, &sf, func(cfg config.Config) (govee.State, error) {
				segs, err := govee.ParseSegments(args[0], cfg.UserPalette())
				if err != nil {
					return govee.State{}, err
				}
				return govee.State{On: true, Segments: segs}, nil
			})
		},
	}
	sf.register(seg, false)
	cmd.AddCommand(seg)
	return cmd
}
