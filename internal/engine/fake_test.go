package engine

import (
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/fake"
)

func newFakeStrip() *fake.Light {
	return fake.New(govee.Device{ID: "x", SKU: "FAKE", Name: "strip", Caps: stripCaps(10)}, nil)
}
