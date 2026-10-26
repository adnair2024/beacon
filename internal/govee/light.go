package govee

import (
	"context"
	"errors"

	"github.com/adnair2024/beacon/internal/color"
)

// Device describes one Govee light.
type Device struct {
	ID      string       // device id from Govee
	SKU     string       // model, as reported by the API
	Name    string       // user-facing nickname
	Backend string       // always "cloud"; kept so a second backend could be added later
	Caps    Capabilities // parsed from the API; never inferred from the SKU
	// SegReverse flips segment numbering for a strip mounted backwards. It is
	// applied only where indices go on the wire (the cloud backend), so
	// everything above that thinks in "segment 0 = the first one".
	SegReverse bool
}

// Segment is one addressable section of a segmented strip.
type Segment struct {
	Index      int       `json:"index"` // 0-based, < Caps.SegmentCount
	Color      color.RGB `json:"color"`
	Brightness int       `json:"brightness,omitempty"` // 0 = inherit State.Brightness
}

// State is the full desired state of a light.
type State struct {
	On         bool       `json:"on"`
	Brightness int        `json:"brightness,omitempty"` // 1-100, clamped to Caps.Brightness
	Color      *color.RGB `json:"color,omitempty"`      // nil when using color temperature
	Kelvin     int        `json:"kelvin,omitempty"`     // 0 when using RGB
	Segments   []Segment  `json:"segments,omitempty"`   // per-segment overrides; nil = whole strip
}

// ErrUnsupported is wrapped by errors for commands a device doesn't advertise.
var ErrUnsupported = errors.New("capability not supported by device")

// Light is a controllable light. Backends must skip redundant commands.
type Light interface {
	Device() Device
	SetPower(ctx context.Context, on bool) error
	SetBrightness(ctx context.Context, pct int) error
	SetColor(ctx context.Context, c color.RGB) error
	SetKelvin(ctx context.Context, k int) error
	SetSegments(ctx context.Context, segs []Segment) error // ErrUnsupported if !Caps.Segments
	Apply(ctx context.Context, s State) error
	Status(ctx context.Context) (State, error)
}
