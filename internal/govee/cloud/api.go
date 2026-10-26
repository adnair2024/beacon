package cloud

import "encoding/json"

// Wire types. Only fields beacon uses are declared; unknown fields and
// unknown capabilities (scenes, segments, ...) are ignored.

type envelope struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Message string `json:"message"`
}

func (e envelope) text() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.Message
}

// ok reports whether the body-level code is a success (the docs show 200;
// some responses use 0).
func (e envelope) ok() bool { return e.Code == 0 || e.Code == 200 }

type apiRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type apiField struct {
	FieldName string `json:"fieldName"`
	// The segment field advertises valid indices as options (0..N-1 in the
	// docs' sample); elementRange/size are accepted as fallbacks.
	Options []struct {
		Value int `json:"value"`
	} `json:"options"`
	ElementRange *apiRange `json:"elementRange"`
	Size         *apiRange `json:"size"`
	Range        *apiRange `json:"range"`
}

type apiCap struct {
	Type       string `json:"type"`
	Instance   string `json:"instance"`
	Parameters struct {
		Range  *apiRange  `json:"range"`
		Fields []apiField `json:"fields"`
	} `json:"parameters"`
}

// apiDevice keeps capabilities raw so they can be both parsed and preserved
// (Capabilities.Raw, `devices inspect`).
type apiDevice struct {
	SKU          string          `json:"sku"`
	Device       string          `json:"device"`
	DeviceName   string          `json:"deviceName"`
	Capabilities json.RawMessage `json:"capabilities"`
}

// listResp keeps each device entry raw for the same reason.
type listResp struct {
	envelope
	Data []json.RawMessage `json:"data"`
}

type capability struct {
	Type     string `json:"type"`
	Instance string `json:"instance"`
	Value    any    `json:"value"`
}

type controlReq struct {
	RequestID string `json:"requestId"`
	Payload   struct {
		SKU        string     `json:"sku"`
		Device     string     `json:"device"`
		Capability capability `json:"capability"`
	} `json:"payload"`
}

type stateReq struct {
	RequestID string `json:"requestId"`
	Payload   struct {
		SKU    string `json:"sku"`
		Device string `json:"device"`
	} `json:"payload"`
}

type stateResp struct {
	envelope
	Payload struct {
		Capabilities []struct {
			Type     string `json:"type"`
			Instance string `json:"instance"`
			State    struct {
				Value json.RawMessage `json:"value"`
			} `json:"state"`
		} `json:"capabilities"`
	} `json:"payload"`
}

// Capability type and instance names from Govee's docs.
const (
	typeOnOff   = "devices.capabilities.on_off"
	typeRange   = "devices.capabilities.range"
	typeColor   = "devices.capabilities.color_setting"
	typeOnline  = "devices.capabilities.online"
	typeSegment = "devices.capabilities.segment_color_setting"
	instSegRGB  = "segmentedColorRgb"
	instSegBri  = "segmentedBrightness"
	instPower   = "powerSwitch"
	instBright  = "brightness"
	instRGB     = "colorRgb"
	instKelvin  = "colorTemperatureK"
	instOnline  = "online"
)

// Values for the segment capabilities.
type segColorValue struct {
	Segment []int `json:"segment"`
	RGB     int   `json:"rgb"`
}

type segBrightnessValue struct {
	Segment    []int `json:"segment"`
	Brightness int   `json:"brightness"`
}
