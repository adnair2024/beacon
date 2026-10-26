package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/cloud"
	"github.com/adnair2024/beacon/internal/govee/fake"
)

// backend abstracts "where devices and lights come from" so every command
// runs identically against the fake (--dry-run) and the cloud.
type backend struct {
	dry   bool
	list  func(ctx context.Context) ([]govee.Device, error)
	light func(d govee.Device) govee.Light
	// inspect returns the device's raw API entry and raw state response.
	inspect func(ctx context.Context, d govee.Device) (entry, state []byte, err error)
}

func newBackend(cfg config.Config, dry bool, out io.Writer) (*backend, error) {
	if dry {
		lights := map[string]*fake.Light{}
		return &backend{
			dry:  true,
			list: func(context.Context) ([]govee.Device, error) { return fake.Devices(), nil },
			light: func(d govee.Device) govee.Light {
				if lights[d.ID] == nil {
					lights[d.ID] = fake.New(d, prefixed{out, d.Name})
				}
				return lights[d.ID]
			},
			inspect: func(ctx context.Context, d govee.Device) ([]byte, []byte, error) {
				return fakeInspect(ctx, d, lights[d.ID])
			},
		}, nil
	}
	key := cfg.Key()
	if key == "" {
		return nil, errors.New("no Govee API key: set GOVEE_API_KEY or api_key in the config (or use --dry-run)")
	}
	c := cloud.New(key)
	lights := map[string]*cloud.Light{}
	return &backend{
		list: c.Devices,
		light: func(d govee.Device) govee.Light {
			if lights[d.ID] == nil {
				lights[d.ID] = c.Light(d)
			}
			return lights[d.ID]
		},
		inspect: func(ctx context.Context, d govee.Device) ([]byte, []byte, error) {
			return c.Inspect(ctx, d.ID)
		},
	}, nil
}

// prefixed tags dry-run output with the device it's for.
type prefixed struct {
	w    io.Writer
	name string
}

func (p prefixed) Write(b []byte) (int, error) {
	s := strings.Replace(string(b), "dry-run: ", fmt.Sprintf("dry-run [%s]: ", p.name), 1)
	return p.w.Write([]byte(s))
}

func names(devs []govee.Device) string {
	n := make([]string, len(devs))
	for i, d := range devs {
		n[i] = d.Name
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

func find(devs []govee.Device, ref string) (govee.Device, bool) {
	for _, d := range devs {
		if strings.EqualFold(d.Name, ref) || strings.EqualFold(d.ID, ref) {
			return d, true
		}
	}
	return govee.Device{}, false
}

// pick chooses which listed devices a command targets:
//  1. an explicit name (config nickname, listed name, or id);
//  2. else the devices in config, matched to the listing by id (or name);
//  3. else the only listed device.
//
// A config nickname replaces the listed name.
func pick(listed []govee.Device, cfg config.Config, name string) ([]govee.Device, error) {
	if len(listed) == 0 {
		return nil, errors.New("no devices found on this account")
	}
	resolve := func(c config.Device) (govee.Device, bool) {
		d, ok := find(listed, c.ID)
		if !ok {
			d, ok = find(listed, c.Name)
		}
		if ok {
			if c.Name != "" {
				d.Name = c.Name
			}
			d = withDeviceConfig(d, c)
		}
		return d, ok
	}
	if name != "" {
		for _, c := range cfg.Devices {
			if strings.EqualFold(c.Name, name) {
				if d, ok := resolve(c); ok {
					return []govee.Device{d}, nil
				}
			}
		}
		if d, ok := find(listed, name); ok {
			return []govee.Device{d}, nil
		}
		return nil, fmt.Errorf("no device named %q (available: %s)", name, names(listed))
	}
	if len(cfg.Devices) > 0 {
		var out []govee.Device
		for _, c := range cfg.Devices {
			d, ok := resolve(c)
			if !ok {
				return nil, fmt.Errorf("configured device %q isn't on this account (available: %s)", c.Name, names(listed))
			}
			out = append(out, d)
		}
		return out, nil
	}
	if len(listed) == 1 {
		return listed, nil
	}
	return nil, fmt.Errorf("several devices found; choose with --device (available: %s)", names(listed))
}
