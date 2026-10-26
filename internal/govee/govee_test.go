package govee_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/fake"
)

func TestMultiFansOutAndJoinsErrors(t *testing.T) {
	devs := fake.Devices()
	rgb, white := fake.New(devs[0], nil), fake.New(devs[1], nil)
	m := govee.Multi{{Name: "desk", Light: rgb}, {Name: "shelf", Light: white}}
	err := m.SetColor(context.Background(), color.RGB{R: 255})
	if !errors.Is(err, govee.ErrUnsupported) || !strings.Contains(err.Error(), "shelf") {
		t.Fatalf("want shelf unsupported, got %v", err)
	}
	if len(rgb.Log()) != 1 {
		t.Fatalf("desk should still be set: %v", rgb.Log())
	}
}

func TestCoalescerSendsOnlyFinalState(t *testing.T) {
	f := fake.New(govee.Device{}, nil)
	co := govee.NewCoalescer(f, 30*time.Millisecond, nil)
	for _, c := range []color.RGB{{R: 255}, {G: 255}, {B: 255}} {
		c := c
		if err := co.Apply(context.Background(), govee.State{On: true, Color: &c}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(f.Log()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(60 * time.Millisecond) // make sure nothing else arrives
	got := strings.Join(f.Log(), ",")
	if got != "power true,color #0000FF" {
		t.Fatalf("got %q", got)
	}
}

func TestCoalescerReportsDeferredErrors(t *testing.T) {
	white := fake.Devices()[1]
	errc := make(chan error, 1)
	co := govee.NewCoalescer(fake.New(white, nil), 10*time.Millisecond, func(e error) { errc <- e })
	c := color.RGB{R: 1}
	co.Apply(context.Background(), govee.State{On: true, Color: &c})
	select {
	case e := <-errc:
		if !errors.Is(e, govee.ErrUnsupported) {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no error reported")
	}
}
