package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

// Fixture shaped like Govee's documented response. The SKUs are invented;
// the second device has an unknown capability and no deviceName.
const devicesJSON = `{"code":200,"message":"success","data":[
 {"sku":"TEST-RGB","device":"AA:BB:CC:DD:EE:FF:00:01","deviceName":"Desk","capabilities":[
  {"type":"devices.capabilities.on_off","instance":"powerSwitch","parameters":{"dataType":"ENUM"}},
  {"type":"devices.capabilities.range","instance":"brightness","parameters":{"dataType":"INTEGER","range":{"min":1,"max":100,"precision":1}}},
  {"type":"devices.capabilities.color_setting","instance":"colorRgb","parameters":{"dataType":"INTEGER","range":{"min":0,"max":16777215,"precision":1}}},
  {"type":"devices.capabilities.color_setting","instance":"colorTemperatureK","parameters":{"dataType":"INTEGER","range":{"min":2200,"max":6500,"precision":1}}}]},
 {"sku":"TEST-WHITE","device":"AA:BB:CC:DD:EE:FF:00:02","capabilities":[
  {"type":"devices.capabilities.on_off","instance":"powerSwitch"},
  {"type":"devices.capabilities.range","instance":"brightness","parameters":{"range":{"min":5,"max":100}}},
  {"type":"devices.capabilities.segment_color_setting","instance":"segmentedColorRgb"}]}]}`

type recorder struct {
	mu     sync.Mutex
	bodies []controlReq
	keys   []string
	status int // if nonzero, returned for /device/control
	header http.Header
}

func (r *recorder) sent() []controlReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]controlReq(nil), r.bodies...)
}

func server(t *testing.T, r *recorder) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/router/api/v1/user/devices", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.keys = append(r.keys, req.Header.Get("Govee-API-Key"))
		r.mu.Unlock()
		io.WriteString(w, devicesJSON)
	})
	mux.HandleFunc("/router/api/v1/device/control", func(w http.ResponseWriter, req *http.Request) {
		var body controlReq
		json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.status != 0 {
			for k, v := range r.header {
				w.Header()[k] = v
			}
			w.WriteHeader(r.status)
			return
		}
		r.bodies = append(r.bodies, body)
		io.WriteString(w, `{"requestId":"x","code":200,"msg":"success"}`)
	})
	mux.HandleFunc("/router/api/v1/device/state", func(w http.ResponseWriter, req *http.Request) {
		io.WriteString(w, `{"code":200,"msg":"success","payload":{"capabilities":[
		 {"type":"devices.capabilities.online","instance":"online","state":{"value":true}},
		 {"type":"devices.capabilities.on_off","instance":"powerSwitch","state":{"value":1}},
		 {"type":"devices.capabilities.range","instance":"brightness","state":{"value":42}},
		 {"type":"devices.capabilities.color_setting","instance":"colorRgb","state":{"value":16747520}},
		 {"type":"devices.capabilities.range","instance":"humidity","state":{"value":""}}]}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New("secret-key-1234", WithBaseURL(srv.URL+"/router/api/v1"))
}

func TestDevicesReadsSKUAndCapsFromResponse(t *testing.T) {
	rec := &recorder{}
	devs, err := server(t, rec).Devices(context.Background())
	if err != nil || len(devs) != 2 {
		t.Fatal(devs, err)
	}
	d, w := devs[0], devs[1]
	if d.SKU != "TEST-RGB" || d.Name != "Desk" || !d.Caps.RGB || d.Caps.Kelvin == nil || d.Caps.Kelvin.Min != 2200 || d.Caps.Kelvin.Max != 6500 {
		t.Fatalf("%+v", d)
	}
	if w.Name != w.ID || w.Caps.RGB || w.Caps.Kelvin != nil || w.Caps.Brightness.Min != 5 || !w.Caps.Power {
		t.Fatalf("white device parsed wrong: %+v", w)
	}
	if rec.keys[0] != "secret-key-1234" {
		t.Fatal("API key header not sent")
	}
}

func TestApplySendsDiffsOnly(t *testing.T) {
	rec := &recorder{}
	c := server(t, rec)
	devs, _ := c.Devices(context.Background())
	l := c.Light(devs[0])
	col := color.RGB{R: 255, G: 140}
	st := govee.State{On: true, Brightness: 80, Color: &col}
	for i := 0; i < 2; i++ {
		if err := l.Apply(context.Background(), st); err != nil {
			t.Fatal(err)
		}
	}
	got := rec.sent()
	if len(got) != 3 {
		t.Fatalf("want 3 commands, got %d", len(got))
	}
	p := got[0].Payload
	if p.SKU != "TEST-RGB" || p.Device != devs[0].ID || p.Capability.Instance != "powerSwitch" {
		t.Fatalf("%+v", p)
	}
	if got[1].Payload.Capability.Value != float64(col.Packed()) {
		t.Fatalf("color value %v", got[1].Payload.Capability.Value)
	}
	if len(got[0].RequestID) != 36 || got[0].RequestID == got[1].RequestID {
		t.Fatalf("bad request ids: %q %q", got[0].RequestID, got[1].RequestID)
	}
}

func TestUnsupportedCapabilitySendsNothing(t *testing.T) {
	rec := &recorder{}
	c := server(t, rec)
	devs, _ := c.Devices(context.Background())
	l := c.Light(devs[1]) // white-only, brightness min 5
	col := color.RGB{R: 1}
	err := l.Apply(context.Background(), govee.State{On: true, Color: &col})
	if !errors.Is(err, govee.ErrUnsupported) || !strings.Contains(err.Error(), devs[1].Name) {
		t.Fatal(err)
	}
	if n := len(rec.sent()); n != 0 {
		t.Fatalf("sent %d commands", n)
	}
}

func TestBrightnessClampedToAdvertisedRange(t *testing.T) {
	rec := &recorder{}
	c := server(t, rec)
	devs, _ := c.Devices(context.Background())
	if err := c.Light(devs[1]).SetBrightness(context.Background(), 2); err != nil { // min is 5
		t.Fatal(err)
	}
	got := rec.sent()
	if len(got) != 1 || got[0].Payload.Capability.Value != float64(5) {
		t.Fatalf("want clamp to 5, got %+v", got)
	}
}

func TestKelvinFallsBackToRGBWhenUnsupported(t *testing.T) {
	rec := &recorder{}
	c := server(t, rec)
	dev := govee.Device{ID: "x", SKU: "s", Name: "n", Caps: govee.Capabilities{Power: true, RGB: true}}
	if err := c.Light(dev).Apply(context.Background(), govee.State{On: true, Kelvin: 3000}); err != nil {
		t.Fatal(err)
	}
	got := rec.sent()
	if len(got) != 2 || got[1].Payload.Capability.Instance != "colorRgb" {
		t.Fatalf("want power + colorRgb approximation, got %+v", got)
	}
}

func TestSegmentsRefusedWithoutSupport(t *testing.T) {
	c := server(t, &recorder{})
	devs, _ := c.Devices(context.Background())
	err := c.Light(devs[0]).SetSegments(context.Background(), []govee.Segment{{Index: 0}})
	if !errors.Is(err, govee.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestUnauthorized(t *testing.T) {
	rec := &recorder{status: 401}
	c := server(t, rec)
	devs := []govee.Device{{ID: "x", SKU: "s", Name: "n", Caps: govee.DefaultCapabilities()}}
	err := c.Light(devs[0]).SetPower(context.Background(), true)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Fatal("error leaks the API key")
	}
}

func TestRateLimitBackoffAndRecovery(t *testing.T) {
	now := time.Unix(1000, 0)
	rec := &recorder{status: 429}
	c := server(t, rec)
	c.now = func() time.Time { return now }
	dev := govee.Device{ID: "x", SKU: "s", Name: "n", Caps: govee.DefaultCapabilities()}
	l := c.Light(dev)

	var rl *RateLimitError
	if err := l.SetPower(context.Background(), true); !errors.As(err, &rl) || rl.RetryAfter != 2*time.Second {
		t.Fatalf("first 429: %v", err)
	}
	// Still inside the window: refused locally, no request made.
	rec.mu.Lock()
	rec.status = 0
	rec.mu.Unlock()
	now = now.Add(time.Second)
	if err := l.SetPower(context.Background(), true); !errors.As(err, &rl) {
		t.Fatalf("want local backoff, got %v", err)
	}
	if len(rec.sent()) != 0 {
		t.Fatal("request sent during backoff")
	}
	// Window over: works again, and state was invalidated so it really sends.
	now = now.Add(2 * time.Second)
	if err := l.SetPower(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(rec.sent()) != 1 {
		t.Fatal("expected one command after recovery")
	}
}

func TestRetryAfterHeaderWins(t *testing.T) {
	rec := &recorder{status: 429, header: http.Header{"Retry-After": {"17"}}}
	c := server(t, rec)
	err := c.Light(govee.Device{ID: "x", Caps: govee.DefaultCapabilities()}).SetPower(context.Background(), true)
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 17*time.Second {
		t.Fatal(err)
	}
}

func TestStatus(t *testing.T) {
	c := server(t, &recorder{})
	s, err := c.Light(govee.Device{ID: "x", SKU: "s", Name: "n", Caps: govee.DefaultCapabilities()}).Status(context.Background())
	if err != nil || !s.On || s.Brightness != 42 || s.Color == nil || s.Color.Hex() != "#FF8C00" {
		t.Fatal(s, err)
	}
}

func TestLimiterBurstThenPaces(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(2, 6, func() time.Time { return now })
	for i := 0; i < 6; i++ {
		if d := l.reserve(); d != 0 {
			t.Fatalf("burst token %d waited %v", i, d)
		}
	}
	if d := l.reserve(); d != 500*time.Millisecond {
		t.Fatalf("7th token: %v", d)
	}
	now = now.Add(10 * time.Second) // refills, capped at burst
	if d := l.reserve(); d != 0 {
		t.Fatal(d)
	}
}

func TestLimiterWontWaitPastDeadline(t *testing.T) {
	now := time.Now()
	l := NewLimiter(1, 1, func() time.Time { return now })
	l.reserve()
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(100*time.Millisecond))
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("should refuse to wait 1s with a 100ms budget")
	}
}

func TestUUIDFormat(t *testing.T) {
	u := newUUID()
	parts := strings.Split(u, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || parts[2][0] != '4' {
		t.Fatal(u)
	}
}
