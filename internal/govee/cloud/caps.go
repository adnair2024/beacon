package cloud

import (
	"encoding/json"

	"github.com/adnair2024/beacon/internal/govee"
)

func rng(r *apiRange) *govee.Range {
	if r == nil || r.Max < r.Min {
		return nil
	}
	return &govee.Range{Min: r.Min, Max: r.Max}
}

// segmentCount derives the number of segments from the capability's
// "segment" field, never from a constant. Returns 0 if it can't be parsed,
// in which case segments are treated as unsupported.
func segmentCount(fields []apiField) int {
	for _, f := range fields {
		if f.FieldName != "segment" {
			continue
		}
		if len(f.Options) > 0 {
			hi := f.Options[0].Value
			for _, o := range f.Options {
				hi = max(hi, o.Value)
			}
			return hi + 1
		}
		if f.ElementRange != nil {
			return f.ElementRange.Max + 1
		}
		if f.Size != nil {
			return f.Size.Max
		}
	}
	return 0
}

// parseCaps turns a device's advertised capabilities into govee.Capabilities.
// It's defensive: unknown capability types and malformed entries are ignored.
func parseCaps(raw json.RawMessage) govee.Capabilities {
	c := govee.Capabilities{Raw: raw}
	var in []apiCap
	if err := json.Unmarshal(raw, &in); err != nil {
		return c
	}
	segBrightness := false
	for _, k := range in {
		switch {
		case k.Type == typeOnOff && k.Instance == instPower:
			c.Power = true
		case k.Type == typeRange && k.Instance == instBright:
			// Docs say 1-100; fall back to that if the range is missing.
			if c.Brightness = rng(k.Parameters.Range); c.Brightness == nil {
				c.Brightness = &govee.Range{Min: 1, Max: 100}
			}
		case k.Type == typeColor && k.Instance == instRGB:
			c.RGB = true
		case k.Type == typeColor && k.Instance == instKelvin:
			c.Kelvin = rng(k.Parameters.Range)
		case k.Type == typeSegment && k.Instance == instSegRGB:
			if n := segmentCount(k.Parameters.Fields); n > 0 {
				c.Segments, c.SegmentCount = true, n
			}
		case k.Type == typeSegment && k.Instance == instSegBri:
			segBrightness = true
		}
	}
	c.SegBrightness = c.Segments && segBrightness
	return c
}
