package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/adnair2024/beacon/internal/govee"
)

func fixtureDevices(t *testing.T, name string) map[string]govee.Device {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "govee", name))
	if err != nil {
		t.Fatal(err)
	}
	var r listResp
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	out := map[string]govee.Device{}
	for _, e := range r.Data {
		d, err := toDevice(e)
		if err != nil {
			t.Fatal(err)
		}
		out[d.SKU] = d
	}
	return out
}

// A real capture from an H612F (testdata/govee/h612f_devices.json). Nothing
// here is assumed from the model name: the numbers are what the API said.
func TestParseRealH612FCapture(t *testing.T) {
	d := fixtureDevices(t, "h612f_devices.json")["H612F"]
	c := d.Caps
	if d.Name != "Strip Light" || !c.Power || !c.RGB {
		t.Fatalf("%+v", d)
	}
	if c.Kelvin == nil || *c.Kelvin != (govee.Range{Min: 2700, Max: 6500}) {
		t.Fatalf("kelvin: %+v", c.Kelvin)
	}
	if c.Brightness == nil || *c.Brightness != (govee.Range{Min: 1, Max: 100}) {
		t.Fatalf("brightness: %+v", c.Brightness)
	}
	// 10 segments, advertised as size{1,10} + elementRange{0,9} (not as an
	// options list like Govee's docs sample), with per-segment brightness.
	if !c.Segments || c.SegmentCount != 10 || !c.SegBrightness {
		t.Fatalf("segments: %+v", c)
	}
	if got := c.String(); got != "power brightness(1-100) rgb kelvin(2700-6500) segments×10+brightness" {
		t.Fatal(got)
	}
	if len(c.Raw) == 0 {
		t.Fatal("raw capabilities should be preserved")
	}
}

// Govee's documented sample lists segment indices as options; that shape must
// keep working alongside the real one.
func TestParseDocsShapedSegments(t *testing.T) {
	c := fixtureDevices(t, "docs_shape_devices.json")["TEST-DOCS-SEG"].Caps
	if !c.Segments || c.SegmentCount != 15 || !c.SegBrightness || c.Kelvin.Min != 2000 || c.Kelvin.Max != 9000 {
		t.Fatalf("%+v", c)
	}
}

// The real /device/state reports "" for the segment instances (their state
// can't be read back) and kelvin 0 while in color mode; Status must cope.
func TestStatusParsesTheRealStateResponse(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "govee", "h612f_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	c := New("k", WithBaseURL(srv.URL))
	dev := fixtureDevices(t, "h612f_devices.json")["H612F"]
	st, err := c.Light(dev).Status(context.Background())
	if err != nil || !st.On || st.Brightness != 100 || st.Color == nil || st.Color.Hex() != "#FF7F00" || st.Kelvin != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	if len(st.Segments) != 0 {
		t.Fatal("segment state isn't reported, so none can be returned")
	}
}

func TestParseVariants(t *testing.T) {
	v := fixtureDevices(t, "variants_devices.json")
	if c := v["TEST-NOSEG"].Caps; c.Segments || c.SegmentCount != 0 || c.SegBrightness || c.Kelvin == nil || c.Kelvin.Min != 2200 {
		t.Fatalf("non-segmented: %+v", c)
	}
	if c := v["TEST-NOKELVIN"].Caps; c.Kelvin != nil || !c.RGB {
		t.Fatalf("kelvin-less: %+v", c)
	}
	if c := v["TEST-WHITE"].Caps; c.RGB || c.Brightness.Min != 5 || c.Kelvin.Max != 6500 {
		t.Fatalf("white-only: %+v", c)
	}
	// A segment capability whose indices can't be parsed means "no segments".
	if c := v["TEST-BADSEG"].Caps; c.Segments || c.SegmentCount != 0 {
		t.Fatalf("unparseable segments: %+v", c)
	}
	// Unknown capability types are ignored, not errors.
	if c := v["TEST-UNKNOWN"].Caps; c.Power || c.RGB || c.Segments || c.Kelvin != nil || c.Brightness != nil {
		t.Fatalf("unknown-only: %+v", c)
	}
}

func TestSegmentCountFallbacks(t *testing.T) {
	if n := segmentCount([]apiField{{FieldName: "segment", Options: []struct {
		Value int `json:"value"`
	}{{0}, {1}, {2}}}}); n != 3 {
		t.Fatal(n)
	}
	if n := segmentCount([]apiField{{FieldName: "segment", ElementRange: &apiRange{Min: 0, Max: 9}}}); n != 10 {
		t.Fatal(n)
	}
	if n := segmentCount([]apiField{{FieldName: "segment", Size: &apiRange{Min: 1, Max: 12}}}); n != 12 {
		t.Fatal(n)
	}
	if n := segmentCount(nil); n != 0 {
		t.Fatal(n)
	}
}

func TestMalformedCapabilitiesDontPanic(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`, `"x"`, `[1,2]`, `[{"type":5}]`} {
		_ = parseCaps(json.RawMessage(raw))
	}
}
