package cloud

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Limiter is a token bucket. reserve is pure given the clock, so tests drive
// time directly instead of sleeping.
type Limiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewLimiter allows rate events per second with bursts up to burst.
func NewLimiter(rate float64, burst int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{rate: rate, burst: float64(burst), tokens: float64(burst), last: now(), now: now}
}

// reserve takes one token and returns how long the caller must wait before
// using it. Tokens may go negative: that's the queue of waiting callers.
func (l *Limiter) reserve() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	l.tokens = min(l.burst, l.tokens+t.Sub(l.last).Seconds()*l.rate)
	l.last = t
	l.tokens--
	if l.tokens >= 0 {
		return 0
	}
	return time.Duration(-l.tokens / l.rate * float64(time.Second))
}

func (l *Limiter) refund() {
	l.mu.Lock()
	l.tokens = min(l.burst, l.tokens+1)
	l.mu.Unlock()
}

// Wait blocks until a token is available. If the wait would outlive ctx's
// deadline it fails immediately instead of burning the caller's whole budget
// (a hook has ~1s; sleeping past it helps nobody).
func (l *Limiter) Wait(ctx context.Context) error {
	d := l.reserve()
	if d == 0 {
		return nil
	}
	if dl, ok := ctx.Deadline(); ok && l.now().Add(d).After(dl) {
		l.refund()
		return fmt.Errorf("cloud: local rate limit: would wait %s", d.Round(time.Millisecond))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		l.refund()
		return ctx.Err()
	}
}
