package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/engine"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/modes/pomo"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/state"
)

const (
	debounce  = 20 * time.Millisecond // fs events arrive in bursts
	idleWake  = 30 * time.Second      // safety net if an event is ever missed
	retryWake = 5 * time.Second       // after a failed apply
	pollWake  = 500 * time.Millisecond
)

// Options configures Run.
type Options struct {
	Store *state.Store
	// ConfigPath is watched so edits (e.g. saved from the TUI) take effect.
	ConfigPath string
	// LoadConfig is called on every evaluation; a failure keeps the last good config.
	LoadConfig func() (config.Config, error)
	// ScheduleDir is watched for schedule edits; LoadSchedule returns the
	// active profile (nil for none). A failure keeps the last good profile, so
	// saving a file halfway through an edit never blanks the schedule.
	ScheduleDir  string
	LoadSchedule func() (*schedule.Profile, error)
	// Caps are the capabilities of the device(s) being driven; they decide
	// whether the pomodoro is drawn as a segment bar.
	Caps govee.Capabilities
	// Light is what gets driven; wrap it in a govee.Coalescer to batch bursts.
	Light govee.Light
	Log   *slog.Logger
	Now   func() time.Time // defaults to time.Now
	// Failed is signalled by the caller's Coalescer onErr so a failed deferred
	// apply is retried; see NewRetrier.
	Retry *Retrier
	// OnError, if set, is told about failed applies (e.g. for a TUI status bar).
	OnError func(error)
}

// Retrier lets asynchronous apply failures (from a Coalescer) request a resend.
type Retrier struct {
	failed atomic.Bool
	wake   chan struct{}
}

// NewRetrier returns a Retrier whose Fail method is safe to call from any goroutine.
func NewRetrier() *Retrier { return &Retrier{wake: make(chan struct{}, 1)} }

// Fail marks the last applied state as untrustworthy and wakes the loop.
func (r *Retrier) Fail() {
	r.failed.Store(true)
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run drives the light until ctx is cancelled. It holds the single-engine
// lock for its whole life and returns state.ErrEngineRunning if another
// engine (daemon or TUI) already has it.
func Run(ctx context.Context, o Options) error {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Retry == nil {
		o.Retry = NewRetrier()
	}
	release, err := o.Store.AcquireEngine()
	if err != nil {
		return err
	}
	defer release()

	events := make(chan struct{}, 1)
	nudge := func() {
		select {
		case events <- struct{}{}:
		default:
		}
	}
	polling := watch(ctx, o, nudge)
	if polling {
		o.Log.Warn("file watching unavailable; polling", "every", pollWake)
	}

	d := &loop{o: o}
	timer := time.NewTimer(0) // evaluate once right away
	defer timer.Stop()
	reset := func(wait time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
	for {
		select {
		case <-ctx.Done():
			if c, ok := o.Light.(*govee.Coalescer); ok {
				c.Flush() // don't drop the final state on shutdown
			}
			return nil
		case <-events:
			reset(debounce)
			continue
		case <-o.Retry.wake:
			reset(retryWake)
			continue
		case <-timer.C:
		}
		wait := d.evaluate(ctx)
		if polling {
			wait = min(wait, pollWake)
		}
		reset(wait)
	}
}

// watch forwards changes to the state and config files. It returns true if
// it had to fall back to polling.
func watch(ctx context.Context, o Options, nudge func()) (polling bool) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return true
	}
	dirs := map[string]bool{}
	watched := []string{filepath.Dir(o.Store.Path())}
	if o.ConfigPath != "" {
		watched = append(watched, filepath.Dir(o.ConfigPath))
	}
	if o.ScheduleDir != "" {
		watched = append(watched, o.ScheduleDir)
	}
	for _, dir := range watched {
		if dirs[dir] {
			continue
		}
		_ = os.MkdirAll(dir, 0o700)
		if err := w.Add(dir); err != nil {
			w.Close()
			return true
		}
		dirs[dir] = true
	}
	go func() {
		defer w.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				// Our own temp files and lock churn aren't interesting.
				switch {
				case ev.Name == o.Store.Path(), o.ConfigPath != "" && ev.Name == o.ConfigPath:
					nudge()
				case o.ScheduleDir != "" && filepath.Dir(ev.Name) == o.ScheduleDir && filepath.Ext(ev.Name) == ".json":
					nudge()
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				o.Log.Warn("watcher error", "err", err)
				nudge()
			}
		}
	}()
	return false
}

type loop struct {
	o         Options
	cfg       config.Config
	haveCfg   bool
	prof      *schedule.Profile
	schedWarn string // last schedule problem logged, to log each only once
	last      string // JSON of the last state we applied; "" = unknown
	source    engine.Source
}

func (l *loop) evaluate(ctx context.Context) time.Duration {
	o := l.o
	now := o.Now()
	if o.Retry.failed.Swap(false) {
		l.last = ""
	}
	if cfg, err := o.LoadConfig(); err != nil {
		o.Log.Error("config", "err", err)
		if !l.haveCfg {
			return retryWake
		}
	} else {
		l.cfg, l.haveCfg = cfg, true
	}
	f, err := o.Store.Load()
	if err != nil {
		o.Log.Error("state", "err", err)
		return retryWake
	}
	if ttl, err := time.ParseDuration(l.cfg.Claude.SessionTTL); err == nil {
		l.prune(&f, now, ttl)
	}
	// Complete pomodoro phases that ended since anyone last looked, so the
	// stored state (and its focus count) stays current.
	if ps, err := pomo.SettingsFrom(l.cfg); err == nil && f.Pomo != nil {
		if changed, err := pomo.Persist(o.Store, now, ps); err != nil {
			o.Log.Warn("pomo", "err", err)
		} else if changed {
			if nf, err := o.Store.Load(); err == nil {
				f = nf
			}
		}
	}
	l.reloadSchedule()
	in, err := engine.BuildInputs(l.cfg, f, now, engine.Env{Schedule: l.prof, Caps: o.Caps})
	if in.ScheduleErr != nil {
		l.warnSchedule(in.ScheduleErr)
	} else {
		l.schedWarn = ""
	}
	if err != nil {
		o.Log.Error("inputs", "err", err)
		return idleWake
	}
	res := engine.Resolve(now, in)
	if res.Source != l.source {
		o.Log.Info("layer", "source", res.Source, "why", res.Reason)
		l.source = res.Source
	}
	wait := idleWake
	if next, ok := engine.NextWake(l.cfg, f, now, in); ok {
		wait = min(wait, max(next.Sub(now), debounce))
	}
	if res.Source == engine.SourceNone {
		return wait
	}
	// Effects other than solid need a stream of commands, which the cloud
	// API's rate limits can't take; only the (planned) LAN backend animates.
	st := res.Layer.State
	b, _ := json.Marshal(st)
	if string(b) == l.last {
		return wait
	}
	if err := o.Light.Apply(ctx, st); err != nil {
		o.Log.Error("apply", "err", err)
		if o.OnError != nil {
			o.OnError(err)
		}
		l.last = ""
		return min(wait, retryWake)
	}
	l.last = string(b)
	if err := o.Store.Update(func(f *state.File) error { f.LastApplied = &st; return nil }); err != nil {
		o.Log.Warn("record last_applied", "err", err)
	}
	return wait
}

// prune drops stale Claude sessions from the file so it doesn't grow, and
// reloads it if it did (the aggregate may have changed).
func (l *loop) prune(f *state.File, now time.Time, ttl time.Duration) {
	stale := false
	for _, s := range f.Claude.Sessions {
		if now.Sub(s.Updated) >= ttl {
			stale = true
			break
		}
	}
	if !stale {
		return
	}
	err := l.o.Store.Update(func(sf *state.File) error {
		if claude.Prune(&sf.Claude, now, ttl) {
			if status, _ := claude.Summarize(sf.Claude, now, ttl); status != sf.Claude.Status {
				sf.Claude.Status, sf.Claude.Changed = status, now
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		l.o.Log.Warn("prune", "err", err)
		return
	}
	if nf, err := l.o.Store.Load(); err == nil {
		*f = nf
	}
}

func (l *loop) reloadSchedule() {
	if l.o.LoadSchedule == nil {
		return
	}
	p, err := l.o.LoadSchedule()
	if err != nil {
		l.warnSchedule(err)
		return // keep the last good profile
	}
	l.prof = p
}

func (l *loop) warnSchedule(err error) {
	if msg := err.Error(); msg != l.schedWarn {
		l.schedWarn = msg
		l.o.Log.Warn("schedule", "err", err)
	}
}
