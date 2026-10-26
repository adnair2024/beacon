package claude

import (
	"encoding/json"
	"io"
	"time"

	"github.com/adnair2024/beacon/internal/state"
)

// Session states. The aggregate Status uses the same words.
const (
	Working = "working"
	Waiting = "waiting"
	Ready   = "ready"
)

// MaxPayload bounds how much of a hook's stdin is read; it's untrusted input.
const MaxPayload = 64 << 10

// Event is what `beacon hook <event>` was called with.
type Event string

const (
	SessionStart Event = "session-start"
	EvWorking    Event = "working"
	EvWaiting    Event = "waiting"
	EvReady      Event = "ready"
	SessionEnd   Event = "session-end"
)

// ParseEvent validates the command-line event name.
func ParseEvent(s string) (Event, bool) {
	switch e := Event(s); e {
	case SessionStart, EvWorking, EvWaiting, EvReady, SessionEnd:
		return e, true
	}
	return "", false
}

// Input is the little we take from a hook payload. Everything else Claude
// sends (transcript path, prompt-adjacent text) is ignored on purpose.
type Input struct {
	SessionID string
	Cwd       string
}

// ManualSession is the key used when stdin isn't a hook payload, e.g. when
// someone runs `beacon hook working` by hand.
const ManualSession = "manual"

const maxField = 256

// ReadInput never fails: unreadable, oversized, empty or non-JSON input
// yields the manual session.
func ReadInput(r io.Reader) Input {
	if r == nil {
		return Input{SessionID: ManualSession}
	}
	b, _ := io.ReadAll(io.LimitReader(r, MaxPayload))
	var p struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	if json.Unmarshal(b, &p) != nil || p.SessionID == "" {
		return Input{SessionID: ManualSession}
	}
	return Input{SessionID: clip(p.SessionID), Cwd: clip(p.Cwd)}
}

func clip(s string) string {
	if len(s) > maxField {
		return s[:maxField]
	}
	return s
}

func stale(s state.Session, now time.Time, ttl time.Duration) bool {
	return ttl > 0 && now.Sub(s.Updated) >= ttl
}

// Prune drops sessions not updated within ttl: crashed sessions that never
// sent session-end. It reports whether anything was removed.
func Prune(c *state.Claude, now time.Time, ttl time.Duration) bool {
	removed := false
	for id, s := range c.Sessions {
		if stale(s, now, ttl) {
			delete(c.Sessions, id)
			removed = true
		}
	}
	return removed
}

// Summarize reduces live sessions to one status: any waiting wins (needs
// attention), else any working, else ready; "" with no live sessions. Stale
// sessions are ignored even if no writer has pruned them yet.
func Summarize(c state.Claude, now time.Time, ttl time.Duration) (status string, live int) {
	for _, s := range c.Sessions {
		if stale(s, now, ttl) {
			continue
		}
		live++
		switch {
		case s.State == Waiting:
			status = Waiting
		case s.State == Working && status != Waiting:
			status = Working
		case s.State == Ready && status == "":
			status = Ready
		}
	}
	return status, live
}

// Record applies one hook event and refreshes the aggregate. Any event
// refreshes the session's TTL, which is how PreToolUse keeps a long-running
// session alive.
func Record(c *state.Claude, ev Event, in Input, now time.Time, ttl time.Duration) {
	Prune(c, now, ttl)
	if c.Sessions == nil {
		c.Sessions = map[string]state.Session{}
	}
	switch ev {
	case SessionEnd:
		delete(c.Sessions, in.SessionID)
	default:
		st := map[Event]string{SessionStart: Ready, EvWorking: Working, EvWaiting: Waiting, EvReady: Ready}[ev]
		c.Sessions[in.SessionID] = state.Session{State: st, Updated: now, Cwd: in.Cwd}
	}
	if status, _ := Summarize(*c, now, ttl); status != c.Status {
		c.Status, c.Changed = status, now
	}
}

// View is the aggregate as the engine should see it right now.
type View struct {
	Status  string
	Changed time.Time // zero if the stored aggregate is out of date (a session expired)
	Live    int
}

// Current computes the aggregate at now, honoring TTL even when no writer has
// run since a session went stale.
func Current(c state.Claude, now time.Time, ttl time.Duration) View {
	status, live := Summarize(c, now, ttl)
	v := View{Status: status, Live: live}
	if status == c.Status {
		v.Changed = c.Changed
	}
	return v
}

// NextExpiry is when the next live session will go stale, so a daemon can
// wake and drop it from the aggregate.
func NextExpiry(c state.Claude, now time.Time, ttl time.Duration) (time.Time, bool) {
	var best time.Time
	if ttl <= 0 {
		return best, false
	}
	for _, s := range c.Sessions {
		if e := s.Updated.Add(ttl); e.After(now) && (best.IsZero() || e.Before(best)) {
			best = e
		}
	}
	return best, !best.IsZero()
}
