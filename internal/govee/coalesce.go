package govee

import (
	"context"
	"sync"
	"time"
)

// Coalescer debounces Apply: rapid-fire states (Claude flipping
// working -> ready -> working within a second) collapse into the last one, so
// a rate-limited backend sees one command. The first Apply in a burst starts
// the window; later ones replace the pending state without extending it, which
// bounds latency to the window. Set* and Status pass straight through.
type Coalescer struct {
	Light
	window time.Duration
	onErr  func(error)

	mu      sync.Mutex
	pending *State
	timer   *time.Timer
}

// NewCoalescer wraps inner. onErr receives errors from deferred applies
// (they can't be returned to the caller); it may be nil.
func NewCoalescer(inner Light, window time.Duration, onErr func(error)) *Coalescer {
	return &Coalescer{Light: inner, window: window, onErr: onErr}
}

func (c *Coalescer) Apply(_ context.Context, s State) error {
	if s.Color != nil {
		col := *s.Color // don't alias the caller's value
		s.Color = &col
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = &s
	if c.timer == nil {
		c.timer = time.AfterFunc(c.window, func() { c.flush() })
	}
	return nil
}

// Flush applies any pending state now.
func (c *Coalescer) Flush() { c.flush() }

func (c *Coalescer) flush() {
	c.mu.Lock()
	s := c.pending
	c.pending, c.timer = nil, nil
	c.mu.Unlock()
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Light.Apply(ctx, *s); err != nil && c.onErr != nil {
		c.onErr(err)
	}
}
