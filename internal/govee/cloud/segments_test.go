package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

var update = flag.Bool("update", false, "rewrite golden files")

// rawServer records each /device/control request body exactly as sent, with
// the random requestId normalized. apiCode, if set, is returned in the body
// (as HTTP 200, the way Govee reports bad requests).
type rawServer struct {
	mu      sync.Mutex
	bodies  []string
	apiCode int
	apiMsg  string
}

func (r *rawServer) reset() { r.mu.Lock(); r.bodies = nil; r.mu.Unlock() }

func (r *rawServer) taken() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.bodies
	r.bodies = nil
	return out
}

func newRaw(t *testing.T) (*rawServer, *Client) {
	t.Helper()
	rs := &rawServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["requestId"] = "REQ"
		norm, _ := json.MarshalIndent(m, "", "  ")
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if rs.apiCode != 0 {
			fmt.Fprintf(w, `{"code":%d,"msg":%q}`, rs.apiCode, rs.apiMsg)
			return
		}
		rs.bodies = append(rs.bodies, string(norm))
		io.WriteString(w, `{"requestId":"x","code":200,"msg":"success"}`)
	}))
	t.Cleanup(srv.Close)
	return rs, New("test-key", WithBaseURL(srv.URL))
}

func h612f(t *testing.T) govee.Device {
	t.Helper()
	d := fixtureDevices(t, "h612f_devices.json")["H612F"]
	d.ID = "XX:XX:XX:XX:XX:XX:XX:XX"
	return d
}

func seg(i int, hex string, bri int) govee.Segment {
	c, err := color.Parse(hex, nil)
	if err != nil {
		panic(err)
	}
	return govee.Segment{Index: i, Color: c, Brightness: bri}
}

func halves(left, right string, rb int) []govee.Segment {
	var s []govee.Segment
	for i := 0; i < 10; i++ {
		if i < 5 {
			s = append(s, seg(i, left, 100))
		} else {
			s = append(s, seg(i, right, rb))
		}
	}
	return s
}

// short renders recorded bodies as "instance value" lines for readable asserts.
func short(t *testing.T, bodies []string) []string {
	t.Helper()
	var out []string
	for _, b := range bodies {
		var m struct {
			Payload struct {
				Capability struct {
					Instance string          `json:"instance"`
					Value    json.RawMessage `json:"value"`
				} `json:"capability"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(b), &m); err != nil {
			t.Fatal(err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, m.Payload.Capability.Value); err != nil {
			t.Fatal(err)
		}
		out = append(out, m.Payload.Capability.Instance+" "+compact.String())
	}
	return out
}

// ---- golden request bodies: one per row of the control table ----

func TestControlRequestBodiesGolden(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	ctx := context.Background()
	orange := color.RGB{R: 255, G: 140}
	steps := []struct {
		name string
		do   func() error
	}{
		{"power on", func() error { return l.SetPower(ctx, true) }},
		{"brightness 80", func() error { return l.SetBrightness(ctx, 80) }},
		{"colorRgb orange", func() error { return l.SetColor(ctx, orange) }},
		{"colorTemperatureK 3000", func() error { return l.SetKelvin(ctx, 3000) }},
		{"segmentedColorRgb [0,1,2] orange", func() error {
			return l.SetSegments(ctx, []govee.Segment{seg(0, "#FF8C00", 0), seg(1, "#FF8C00", 0), seg(2, "#FF8C00", 0)})
		}},
		{"segmentedBrightness [3,4] 30", func() error {
			return l.SetSegments(ctx, []govee.Segment{seg(3, "#000000", 30), seg(4, "#000000", 30)})
		}},
	}
	var got strings.Builder
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		for _, b := range rs.taken() {
			fmt.Fprintf(&got, "// %s\n%s\n", s.name, b)
		}
	}
	path := filepath.Join("testdata", "control_bodies.golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if string(want) != got.String() {
		t.Fatalf("request bodies changed:\n--- got ---\n%s\n--- want ---\n%s", got.String(), want)
	}
}

// ---- segment behavior ----

func TestSegmentsBatchByColorAndBrightness(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	ctx := context.Background()
	if err := l.SetSegments(ctx, halves("#00FF00", "#0000FF", 100)); err != nil {
		t.Fatal(err)
	}
	// One call per distinct color, one for the single distinct brightness:
	// 10 segments cost 3 requests, not 20.
	want := []string{
		`segmentedColorRgb {"rgb":65280,"segment":[0,1,2,3,4]}`,
		`segmentedColorRgb {"rgb":255,"segment":[5,6,7,8,9]}`,
		`segmentedBrightness {"brightness":100,"segment":[0,1,2,3,4,5,6,7,8,9]}`,
	}
	if got := short(t, rs.taken()); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
}

// The pomodoro bar moving by one segment must cost exactly one request.
func TestMovingTheBarByOneSegmentIsOneRequest(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	ctx := context.Background()
	bar := func(filled int) []govee.Segment {
		var s []govee.Segment
		for i := 0; i < 10; i++ {
			b := 10
			if i < filled {
				b = 100
			}
			s = append(s, seg(i, "#FF4FA3", b))
		}
		return s
	}
	if err := l.SetSegments(ctx, bar(10)); err != nil {
		t.Fatal(err)
	}
	rs.reset()
	if err := l.SetSegments(ctx, bar(9)); err != nil {
		t.Fatal(err)
	}
	got := short(t, rs.taken())
	if len(got) != 1 || got[0] != `segmentedBrightness {"brightness":10,"segment":[9]}` {
		t.Fatalf("one segment dimmed = one request, got %v", got)
	}
	if err := l.SetSegments(ctx, bar(9)); err != nil || len(rs.taken()) != 0 {
		t.Fatal("resending an unchanged bar must send nothing")
	}
	if err := l.SetSegments(ctx, bar(7)); err != nil {
		t.Fatal(err)
	}
	if got := short(t, rs.taken()); len(got) != 1 || got[0] != `segmentedBrightness {"brightness":10,"segment":[7,8]}` {
		t.Fatalf("two segments dimmed together are one batched request: %v", got)
	}
}

func TestWholeStripChangesForgetSegmentMemory(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	ctx := context.Background()
	l.SetSegments(ctx, halves("#00FF00", "#0000FF", 100))
	rs.reset()
	// A whole-strip color overwrites the segments on the device, so our idea
	// of them is stale: the same segments must be sent again in full.
	if err := l.SetColor(ctx, color.RGB{R: 255}); err != nil {
		t.Fatal(err)
	}
	rs.reset()
	if err := l.SetSegments(ctx, halves("#00FF00", "#0000FF", 100)); err != nil {
		t.Fatal(err)
	}
	if n := len(rs.taken()); n != 3 {
		t.Fatalf("expected a full resend (3 requests), got %d", n)
	}
	// Same for a whole-strip brightness change.
	l.SetBrightness(ctx, 50)
	rs.reset()
	l.SetSegments(ctx, halves("#00FF00", "#0000FF", 100))
	if n := len(rs.taken()); n != 3 {
		t.Fatalf("brightness change should also force a resend, got %d", n)
	}
}

func TestLeavingSegmentModeForcesTheWholeStripCommand(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	ctx := context.Background()
	pink := color.RGB{R: 255, G: 79, B: 163}
	l.Apply(ctx, govee.State{On: true, Color: &pink, Brightness: 100})
	l.Apply(ctx, govee.State{On: true, Color: &pink, Brightness: 100, Segments: halves("#FF4FA3", "#FF4FA3", 10)})
	rs.reset()
	// Back to a plain pink state: its whole-strip values equal the last ones
	// sent, which would normally be skipped, but only a whole-strip command
	// clears the segment brightness, so they must be sent.
	if err := l.Apply(ctx, govee.State{On: true, Color: &pink, Brightness: 100}); err != nil {
		t.Fatal(err)
	}
	got := short(t, rs.taken())
	if len(got) != 2 || !strings.HasPrefix(got[0], "colorRgb") || !strings.HasPrefix(got[1], "brightness") {
		t.Fatalf("expected a forced colorRgb + brightness, got %v", got)
	}
}

func TestApplyOrdersWholeStripBeforeSegments(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	pink := color.RGB{R: 255, G: 79, B: 163}
	err := l.Apply(context.Background(), govee.State{On: true, Color: &pink, Brightness: 100,
		Segments: []govee.Segment{seg(0, "#00FF00", 100)}})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range short(t, rs.taken()) {
		order = append(order, strings.Fields(s)[0])
	}
	if strings.Join(order, ",") != "powerSwitch,colorRgb,brightness,segmentedColorRgb,segmentedBrightness" {
		t.Fatalf("a whole-strip command after the segments would wipe them: %v", order)
	}
}

func TestErrorsDropSegmentMemoryAndSurface(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	ctx := context.Background()
	l.SetSegments(ctx, halves("#00FF00", "#0000FF", 100))
	rs.reset()
	// Govee reports bad requests as HTTP 200 with a code in the body.
	rs.apiCode, rs.apiMsg = 400, "Parameter value out of range"
	err := l.SetSegments(ctx, halves("#FF0000", "#0000FF", 100))
	if err == nil || !strings.Contains(err.Error(), "Parameter value out of range") {
		t.Fatalf("a body-level error must surface: %v", err)
	}
	rs.apiCode = 0
	rs.reset()
	// We no longer know what is on the strip, so the retry resends everything.
	if err := l.SetSegments(ctx, halves("#FF0000", "#0000FF", 100)); err != nil {
		t.Fatal(err)
	}
	if n := len(rs.taken()); n != 3 {
		t.Fatalf("after an error everything must be resent, got %d requests", n)
	}
}

func TestReversedStripMapsIndicesAtTheWireOnly(t *testing.T) {
	rs, c := newRaw(t)
	d := h612f(t)
	d.SegReverse = true
	l := c.Light(d)
	if err := l.SetSegments(context.Background(), []govee.Segment{seg(0, "#FF0000", 0), seg(1, "#FF0000", 0), seg(9, "#0000FF", 0)}); err != nil {
		t.Fatal(err)
	}
	got := short(t, rs.taken())
	want := []string{`segmentedColorRgb {"rgb":16711680,"segment":[8,9]}`, `segmentedColorRgb {"rgb":255,"segment":[0]}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("logical 0,1 -> wire 9,8 (sorted), logical 9 -> wire 0:\n got %v\nwant %v", got, want)
	}
}

func TestSegmentValidationHappensBeforeAnyRequest(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	ctx := context.Background()
	for name, segs := range map[string][]govee.Segment{
		"index past the end": {seg(10, "#FF0000", 0)},
		"negative index":     {seg(-1, "#FF0000", 0)},
	} {
		if err := l.SetSegments(ctx, segs); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	// A device without segments refuses with ErrUnsupported.
	plain := govee.Device{ID: "x", SKU: "s", Name: "plain", Caps: govee.DefaultCapabilities()}
	if err := c.Light(plain).SetSegments(ctx, []govee.Segment{seg(0, "#FF0000", 0)}); !errors.Is(err, govee.ErrUnsupported) {
		t.Fatalf("%v", err)
	}
	// Segment brightness on a device that only has segment color is refused.
	noBri := h612f(t)
	noBri.Caps.SegBrightness = false
	if err := c.Light(noBri).SetSegments(ctx, []govee.Segment{seg(0, "#FF0000", 50)}); !errors.Is(err, govee.ErrUnsupported) {
		t.Fatalf("%v", err)
	}
	if n := len(rs.taken()); n != 0 {
		t.Fatalf("rejected requests must send nothing, sent %d", n)
	}
}

func TestDuplicateIndicesAreCollapsedLastWins(t *testing.T) {
	rs, c := newRaw(t)
	l := c.Light(h612f(t))
	err := l.SetSegments(context.Background(), []govee.Segment{seg(2, "#FF0000", 0), seg(2, "#0000FF", 0)})
	if err != nil {
		t.Fatal(err)
	}
	if got := short(t, rs.taken()); len(got) != 1 || got[0] != `segmentedColorRgb {"rgb":255,"segment":[2]}` {
		t.Fatalf("%v", got)
	}
}

func TestSegmentsCollapseOnDevicesWithoutThem(t *testing.T) {
	rs, c := newRaw(t)
	plain := govee.Device{ID: "x", SKU: "s", Name: "plain", Caps: govee.DefaultCapabilities()}
	l := c.Light(plain)
	// Through Apply (the engine's path) segments degrade to a whole-strip
	// color instead of failing.
	if err := l.Apply(context.Background(), govee.State{On: true, Segments: halves("#00FF00", "#0000FF", 100)}); err != nil {
		t.Fatal(err)
	}
	got := short(t, rs.taken())
	if len(got) != 2 || !strings.HasPrefix(got[1], "colorRgb") {
		t.Fatalf("%v", got)
	}
}
