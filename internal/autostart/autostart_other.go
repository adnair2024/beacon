//go:build !linux && !windows

package autostart

import "errors"

var errUnsupported = errors.New("starting at login isn't supported on this OS; run `beacon daemon` from your login items instead")

type unsupported struct{}

// New returns the backend for this OS.
func New() Backend { return unsupported{} }

func (unsupported) Name() string                   { return "login item" }
func (unsupported) Render(Options) (string, error) { return "", errUnsupported }
func (unsupported) Install(Options) error          { return errUnsupported }
func (unsupported) Remove() error                  { return errUnsupported }
func (unsupported) Status() (string, error)        { return "", errUnsupported }
