package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/paths"
)

const redactedID = "XX:XX:XX:XX:XX:XX:XX:XX"

// redact hides a device id anywhere it appears in raw JSON.
func redact(b []byte, id string) []byte {
	if id == "" {
		return b
	}
	return bytes.ReplaceAll(b, []byte(id), []byte(redactedID))
}

func pretty(b []byte) (string, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}

// fakeInspect synthesizes the same two documents the cloud would return, from
// a fake device, so `devices inspect --dry-run` is demoable.
func fakeInspect(ctx context.Context, d govee.Device, l govee.Light) ([]byte, []byte, error) {
	type cp struct {
		Type     string `json:"type"`
		Instance string `json:"instance"`
	}
	caps := []cp{}
	add := func(ok bool, t, i string) {
		if ok {
			caps = append(caps, cp{"devices.capabilities." + t, i})
		}
	}
	add(d.Caps.Power, "on_off", "powerSwitch")
	add(d.Caps.Brightness != nil, "range", "brightness")
	add(d.Caps.RGB, "color_setting", "colorRgb")
	add(d.Caps.Kelvin != nil, "color_setting", "colorTemperatureK")
	add(d.Caps.Segments, "segment_color_setting", "segmentedColorRgb")
	add(d.Caps.SegBrightness, "segment_color_setting", "segmentedBrightness")
	entry, _ := json.Marshal(map[string]any{"sku": d.SKU, "device": d.ID, "deviceName": d.Name, "capabilities": caps})
	var st govee.State
	if l != nil {
		st, _ = l.Status(ctx)
	}
	state, _ := json.Marshal(map[string]any{"code": 200, "msg": "success",
		"payload": map[string]any{"sku": d.SKU, "device": d.ID, "state": st}})
	return entry, state, nil
}

// cacheDevices writes fresh SKU and capabilities into matching [[devices]]
// entries and reports which ones changed.
func cacheDevices(cfg *config.Config, listed []govee.Device) (updated, missing []string) {
	for i := range cfg.Devices {
		c := &cfg.Devices[i]
		d, ok := find(listed, c.ID)
		if !ok {
			d, ok = find(listed, c.Name)
		}
		if !ok {
			missing = append(missing, c.Name)
			continue
		}
		k := config.CacheOf(d.Caps)
		c.ID, c.SKU, c.Caps = d.ID, d.SKU, &k
		updated = append(updated, fmt.Sprintf("%s (%s): %s", c.Name, d.SKU, d.Caps))
	}
	return updated, missing
}

func newDevices() *cobra.Command {
	cmd := &cobra.Command{Use: "devices", Short: "List and test devices"}
	var dry bool
	cmd.PersistentFlags().BoolVar(&dry, "dry-run", false, "use fake devices instead of the Govee cloud")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List devices on the account with their capabilities",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}
			b, err := newBackend(cfg, dry, c.OutOrStdout())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
			defer cancel()
			devs, err := b.list(ctx)
			if err != nil {
				return err
			}
			configured := map[string]string{}
			for _, d := range cfg.Devices {
				configured[strings.ToLower(d.ID)] = d.Name
			}
			for _, d := range devs {
				mark := " "
				if _, ok := configured[strings.ToLower(d.ID)]; ok {
					mark = "*"
				}
				c.Printf("%s %-16s %-14s %s\n    %s\n", mark, d.Name, d.SKU, d.ID, d.Caps)
			}
			c.Println("\n* = configured in config.toml")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "refresh",
		Short: "Re-read device capabilities from the API into the config",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}
			b, err := newBackend(cfg, dry, c.OutOrStdout())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
			defer cancel()
			listed, err := b.list(ctx)
			if err != nil {
				return err
			}
			if len(cfg.Devices) == 0 {
				c.Println("no [[devices]] in the config; add one (names and ids from `beacon devices list`) and run this again")
				return nil
			}
			updated, missing := cacheDevices(&cfg, listed)
			for _, u := range updated {
				c.Println("refreshed", u)
			}
			for _, m := range missing {
				c.PrintErrln("warning: configured device", m, "isn't on this account")
			}
			if dry {
				c.Println("dry-run: config not written")
				return nil
			}
			p, err := paths.ConfigFile()
			if err != nil {
				return err
			}
			if err := config.Save(p, cfg); err != nil {
				return err
			}
			c.Println("saved", p, "(previous version kept as .bak)")
			return nil
		},
	})

	var noRedact bool
	inspect := &cobra.Command{
		Use:   "inspect <name>",
		Short: "Print a device's raw API entry and state (ids redacted) for fixtures",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}
			b, err := newBackend(cfg, dry, c.OutOrStdout())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
			defer cancel()
			listed, err := b.list(ctx)
			if err != nil {
				return err
			}
			devs, err := pick(listed, cfg, args[0])
			if err != nil {
				return err
			}
			d := devs[0]
			if dry { // so the fake's state is populated like a lived-in device
				b.light(d)
			}
			entry, state, err := b.inspect(ctx, d)
			if err != nil {
				return err
			}
			for _, doc := range []struct {
				title string
				raw   []byte
			}{{"/user/devices entry", entry}, {"/device/state", state}} {
				raw := doc.raw
				if !noRedact {
					raw = redact(raw, d.ID)
				}
				out, err := pretty(raw)
				if err != nil {
					return fmt.Errorf("%s: %w", doc.title, err)
				}
				c.Printf("// %s\n%s\n", doc.title, out)
			}
			return nil
		},
	}
	inspect.Flags().BoolVar(&noRedact, "no-redact", false, "show the real device id (never commit that output)")
	cmd.AddCommand(inspect)

	var hold time.Duration
	test := &cobra.Command{
		Use:   "test <name>",
		Short: "Flash red, green, blue on a device (and chase a segment on segmented strips)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}
			b, err := newBackend(cfg, dry, c.OutOrStdout())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
			defer cancel()
			listed, err := b.list(ctx)
			if err != nil {
				return err
			}
			devs, err := pick(listed, cfg, args[0])
			if err != nil {
				return err
			}
			d := devs[0]
			if !d.Caps.RGB {
				return fmt.Errorf("%s (%s) can't show colors (capabilities: %s)", d.Name, d.SKU, d.Caps)
			}
			if dry {
				hold = 0
			}
			l := b.light(d)
			for _, hex := range []string{"#FF0000", "#00FF00", "#0000FF"} {
				rgb, _ := color.Parse(hex, nil)
				if err := l.Apply(ctx, govee.State{On: true, Brightness: 100, Color: &rgb}); err != nil {
					return err
				}
				time.Sleep(hold)
			}
			if n := d.Caps.SegmentCount; d.Caps.Segments && n > 0 {
				// Chase one white segment from segment 0 to the last, so you can see
				// the numbering (and whether `reverse` is needed for how it's mounted).
				blue, white := color.RGB{B: 255}, color.RGB{R: 255, G: 255, B: 255}
				for i := 0; i < n; i++ {
					step := []govee.Segment{{Index: i, Color: white}}
					if i > 0 {
						step = append(step, govee.Segment{Index: i - 1, Color: blue})
					}
					if err := l.SetSegments(ctx, step); err != nil {
						return err
					}
					time.Sleep(hold / 3)
				}
				if err := l.SetSegments(ctx, []govee.Segment{{Index: n - 1, Color: blue}}); err != nil {
					return err
				}
				c.Printf("chased a white segment across all %d segments, from segment 0\n", n)
			}
			c.Printf("%s left on blue; use `beacon set` to change it\n", d.Name)
			return nil
		},
	}
	test.Flags().DurationVar(&hold, "hold", 700*time.Millisecond, "how long to show each color")
	cmd.AddCommand(test)
	return cmd
}
