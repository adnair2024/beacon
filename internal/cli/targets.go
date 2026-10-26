package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/fake"
)

// cachedTargets returns the configured devices straight from the config's
// capability cache, with no network. It succeeds only if every configured
// device has an ID, SKU and cached capabilities (written by `devices
// refresh`). Hooks use this to avoid a /user/devices round trip.
func cachedTargets(cfg config.Config) ([]govee.Device, bool) {
	if len(cfg.Devices) == 0 {
		return nil, false
	}
	out := make([]govee.Device, 0, len(cfg.Devices))
	for _, c := range cfg.Devices {
		if c.ID == "" || c.SKU == "" || c.Caps == nil {
			return nil, false
		}
		out = append(out, withDeviceConfig(govee.Device{ID: c.ID, SKU: c.SKU, Name: c.Name, Backend: "cloud", Caps: c.Caps.Capabilities()}, c))
	}
	return out, true
}

// resolveTargets picks the devices a background command drives: the cache if
// complete, otherwise a live listing matched against the config.
func resolveTargets(ctx context.Context, b *backend, cfg config.Config) ([]govee.Device, error) {
	if devs, ok := cachedTargets(cfg); ok && !b.dry {
		return devs, nil
	}
	listed, err := b.list(ctx)
	if err != nil {
		return nil, err
	}
	if b.dry && len(cfg.Devices) == 0 {
		return listed[:1], nil // demos: just the fake strip
	}
	return pick(listed, cfg, "")
}

// primaryCaps are the capabilities of the first device we'd drive, read from
// the config's cache without any network, for commands that build engine
// inputs before they reach the light. No cache means no segment features,
// which is a safe fallback (solid colors).
func primaryCaps(cfg config.Config, dry bool) govee.Capabilities {
	if dry { // dry runs drive the fake strip
		return fake.Devices()[0].Caps
	}
	if devs, ok := cachedTargets(cfg); ok {
		return devs[0].Caps
	}
	return govee.Capabilities{}
}

// firstCaps is the capabilities of the first device, or none.
func firstCaps(devs []govee.Device) govee.Capabilities {
	if len(devs) == 0 {
		return govee.Capabilities{}
	}
	return devs[0].Caps
}

func describe(devs []govee.Device) string {
	names := make([]string, len(devs))
	for i, d := range devs {
		names[i] = fmt.Sprintf("%s (%s)", d.Name, d.SKU)
	}
	return strings.Join(names, ", ")
}

// withDeviceConfig overlays the per-device [devices.segments] settings:
// enabled=false switches segment features off for the device, count overrides
// a bad parse of the API's segment count, and reverse flips the numbering for
// a strip mounted backwards. Capabilities themselves still come from the API.
func withDeviceConfig(d govee.Device, c config.Device) govee.Device {
	if !c.SegmentsEnabled() {
		d.Caps.Segments, d.Caps.SegBrightness, d.Caps.SegmentCount = false, false, 0
	} else if c.Segments.Count > 0 && d.Caps.Segments {
		d.Caps.SegmentCount = c.Segments.Count
	}
	d.SegReverse = c.Segments.Reverse && d.Caps.Segments
	return d
}
