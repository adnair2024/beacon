package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/engine"
	"github.com/adnair2024/beacon/internal/state"
)

func newClear() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Cancel the manual override and hand the light back",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}
			store, err := openStore(dry)
			if err != nil {
				return err
			}
			had := false
			if err := store.Update(func(f *state.File) error {
				had = f.Override != nil
				f.Override = nil
				return nil
			}); err != nil {
				return err
			}
			if !had {
				c.Println("no override was active")
			}
			f, err := store.Load()
			if err != nil {
				return err
			}
			in, err := engine.BuildInputs(cfg, f, time.Now(), engine.Env{Schedule: activeSchedule(cfg, dry, nil), Caps: primaryCaps(cfg, dry)})
			if err != nil {
				return err
			}
			res := engine.Resolve(time.Now(), in)
			if res.Source == engine.SourceNone {
				c.Println("nothing else wants the light, so it stays as it is")
				return nil
			}
			b, err := newBackend(cfg, dry, c.OutOrStdout())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
			defer cancel()
			listed, err := b.list(ctx)
			if err != nil {
				return err
			}
			devs, err := pick(listed, cfg, "")
			if err != nil {
				return err
			}
			if err := applyNow(ctx, b, devs, res.Layer.State, store); err != nil {
				return err
			}
			c.Println("now showing:", res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "use fake devices and the dry-run state")
	return cmd
}
