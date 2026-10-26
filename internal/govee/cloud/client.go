package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/adnair2024/beacon/internal/govee"
)

// DefaultBaseURL is the Cloud API root.
const DefaultBaseURL = "https://openapi.api.govee.com/router/api/v1"

var (
	// ErrUnauthorized means HTTP 401: the API key is wrong or revoked.
	ErrUnauthorized = errors.New("cloud: unauthorized (check the Govee API key)")
	// ErrOffline means the device reported online=false.
	ErrOffline = errors.New("cloud: device is offline")
)

// RateLimitError is returned on HTTP 429 and while backing off after one.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("cloud: rate limited, retry in %s", e.RetryAfter.Round(time.Second))
}

const (
	backoffStart = 2 * time.Second
	backoffMax   = time.Minute
)

// Client talks to the Cloud API. It's safe for concurrent use.
type Client struct {
	key  string // never logged or put in an error
	base string
	hc   *http.Client
	now  func() time.Time

	listLim *Limiter // account: 30/min
	ctlLim  *Limiter // account: 12/s, burst 80

	mu       sync.Mutex
	devCtl   map[string]*Limiter // per device: 2/s, burst 6
	devState map[string]*Limiter // per device: 30/min
	until    time.Time           // no requests before this (after a 429)
	step     time.Duration
}

// Option customizes a Client.
type Option func(*Client)

func WithBaseURL(u string) Option           { return func(c *Client) { c.base = u } }
func WithHTTPClient(h *http.Client) Option  { return func(c *Client) { c.hc = h } }
func withClock(now func() time.Time) Option { return func(c *Client) { c.now = now } }

// New returns a Client for the given API key.
func New(key string, opts ...Option) *Client {
	c := &Client{key: key, base: DefaultBaseURL, hc: &http.Client{Timeout: 5 * time.Second}, now: time.Now,
		devCtl: map[string]*Limiter{}, devState: map[string]*Limiter{}}
	for _, o := range opts {
		o(c)
	}
	c.listLim = NewLimiter(0.5, 30, c.now)
	c.ctlLim = NewLimiter(12, 80, c.now)
	return c
}

func (c *Client) limiter(m map[string]*Limiter, id string, rate float64, burst int) *Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := m[id]
	if !ok {
		l = NewLimiter(rate, burst, c.now)
		m[id] = l
	}
	return l
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failing means the OS is broken
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (c *Client) checkBackoff() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.until.Sub(c.now()); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	return nil
}

// rateLimited starts or extends the backoff, honoring Retry-After (seconds).
func (c *Client) rateLimited(retryAfter string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.step == 0 {
		c.step = backoffStart
	} else {
		c.step = min(c.step*2, backoffMax)
	}
	d := c.step
	if s, err := strconv.Atoi(retryAfter); err == nil && s > 0 {
		d = time.Duration(s) * time.Second
	}
	c.until = c.now().Add(d)
	return d
}

func (c *Client) succeeded() {
	c.mu.Lock()
	c.step = 0
	c.mu.Unlock()
}

// do performs one request and decodes the JSON body into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if err := c.checkBackoff(); err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cloud: encode %s: %w", path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("cloud: %s: %w", path, err)
	}
	req.Header.Set("Govee-API-Key", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cloud: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return &RateLimitError{RetryAfter: c.rateLimited(resp.Header.Get("Retry-After"))}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		var e envelope
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("cloud: %s %s: HTTP %d: %s", method, path, resp.StatusCode, e.text())
	}
	c.succeeded()
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("cloud: decode %s: %w", path, err)
		}
	}
	return nil
}

func (c *Client) rawList(ctx context.Context) ([]json.RawMessage, error) {
	if err := c.listLim.Wait(ctx); err != nil {
		return nil, err
	}
	var r listResp
	if err := c.do(ctx, http.MethodGet, "/user/devices", nil, &r); err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, fmt.Errorf("cloud: list devices: %s (code %d)", r.text(), r.Code)
	}
	return r.Data, nil
}

func toDevice(entry json.RawMessage) (govee.Device, error) {
	var d apiDevice
	if err := json.Unmarshal(entry, &d); err != nil {
		return govee.Device{}, fmt.Errorf("cloud: decode device: %w", err)
	}
	name := d.DeviceName
	if name == "" {
		name = d.Device // the docs' sample omits deviceName
	}
	return govee.Device{ID: d.Device, SKU: d.SKU, Name: name, Backend: "cloud", Caps: parseCaps(d.Capabilities)}, nil
}

// Devices lists the account's devices with SKU and capabilities as the API
// reports them. A device entry that can't be decoded is skipped, not fatal.
func (c *Client) Devices(ctx context.Context) ([]govee.Device, error) {
	entries, err := c.rawList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]govee.Device, 0, len(entries))
	for _, e := range entries {
		if d, err := toDevice(e); err == nil {
			out = append(out, d)
		}
	}
	return out, nil
}

// Inspect returns the device's raw /user/devices entry and raw /device/state
// response, for `beacon devices inspect` (and capturing test fixtures).
func (c *Client) Inspect(ctx context.Context, id string) (entry, state json.RawMessage, err error) {
	entries, err := c.rawList(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		d, derr := toDevice(e)
		if derr != nil || d.ID != id {
			continue
		}
		req := stateReq{RequestID: newUUID()}
		req.Payload.SKU, req.Payload.Device = d.SKU, d.ID
		if err := c.limiter(c.devState, d.ID, 0.5, 30).Wait(ctx); err != nil {
			return nil, nil, err
		}
		if err := c.do(ctx, http.MethodPost, "/device/state", req, &state); err != nil {
			return nil, nil, err
		}
		return e, state, nil
	}
	return nil, nil, fmt.Errorf("cloud: device %s not found on the account", id)
}

func (c *Client) control(ctx context.Context, d govee.Device, k capability) error {
	if err := c.devLimiter(d.ID).Wait(ctx); err != nil {
		return err
	}
	if err := c.ctlLim.Wait(ctx); err != nil {
		return err
	}
	req := controlReq{RequestID: newUUID()}
	req.Payload.SKU, req.Payload.Device, req.Payload.Capability = d.SKU, d.ID, k
	var r envelope
	if err := c.do(ctx, http.MethodPost, "/device/control", req, &r); err != nil {
		return err
	}
	if !r.ok() {
		return fmt.Errorf("cloud: control: %s (code %d)", r.text(), r.Code)
	}
	return nil
}

func (c *Client) devLimiter(id string) *Limiter { return c.limiter(c.devCtl, id, 2, 6) }

func (c *Client) state(ctx context.Context, d govee.Device) (stateResp, error) {
	var r stateResp
	if err := c.limiter(c.devState, d.ID, 0.5, 30).Wait(ctx); err != nil {
		return r, err
	}
	req := stateReq{RequestID: newUUID()}
	req.Payload.SKU, req.Payload.Device = d.SKU, d.ID
	if err := c.do(ctx, http.MethodPost, "/device/state", req, &r); err != nil {
		return r, err
	}
	if !r.ok() {
		return r, fmt.Errorf("cloud: state: %s (code %d)", r.text(), r.Code)
	}
	return r, nil
}
