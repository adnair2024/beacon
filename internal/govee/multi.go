package govee

import (
	"context"
	"errors"
	"fmt"

	"github.com/adnair2024/beacon/internal/color"
)

// Named pairs a Light with the name used in error messages.
type Named struct {
	Name  string
	Light Light
}

// Multi fans one command out to several lights. A failure on one member
// doesn't stop the others; all failures come back joined, tagged by name.
type Multi []Named

var _ Light = Multi(nil)

func (m Multi) each(f func(Light) error) error {
	var errs []error
	for _, n := range m {
		if err := f(n.Light); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Name, err))
		}
	}
	return errors.Join(errs...)
}

// Device describes the group, not any one member.
func (m Multi) Device() Device { return Device{Name: "group", Backend: "multi"} }

func (m Multi) SetPower(ctx context.Context, on bool) error {
	return m.each(func(l Light) error { return l.SetPower(ctx, on) })
}
func (m Multi) SetBrightness(ctx context.Context, pct int) error {
	return m.each(func(l Light) error { return l.SetBrightness(ctx, pct) })
}
func (m Multi) SetColor(ctx context.Context, c color.RGB) error {
	return m.each(func(l Light) error { return l.SetColor(ctx, c) })
}
func (m Multi) SetKelvin(ctx context.Context, k int) error {
	return m.each(func(l Light) error { return l.SetKelvin(ctx, k) })
}
func (m Multi) SetSegments(ctx context.Context, segs []Segment) error {
	return m.each(func(l Light) error { return l.SetSegments(ctx, segs) })
}
func (m Multi) Apply(ctx context.Context, s State) error {
	return m.each(func(l Light) error { return l.Apply(ctx, s) })
}

// Status returns the first member's state.
func (m Multi) Status(ctx context.Context) (State, error) {
	if len(m) == 0 {
		return State{}, errors.New("no devices")
	}
	return m[0].Light.Status(ctx)
}
