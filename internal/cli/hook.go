package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/engine"
	"github.com/adnair2024/beacon/internal/logging"
	"github.com/adnair2024/beacon/internal/modes/claude"
	"github.com/adnair2024/beacon/internal/paths"
	"github.com/adnair2024/beacon/internal/state"
)

const (
	stdinWait    = 500 * time.Millisecond
	hookApplyMax = 2 * time.Second
)

// stdinPayload returns what Claude Code piped in, bounded in size and time.
// A terminal (someone ran the hook by hand) or a stdin that never closes
// yields empty input rather than hanging Claude.
func stdinPayload(r io.Reader) io.Reader {
	if f, ok := r.(*os.File); ok {
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return strings.NewReader("")
		}
	}
	ch := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(r, claude.MaxPayload))
		ch <- b
	}()
	select {
	case b := <-ch:
		return bytes.NewReader(b)
	case <-time.After(stdinWait):
		return strings.NewReader("")
	}
}

func newHook() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "hook <event>",
		Short: "Handle a Claude Code hook event (session-start|working|waiting|ready|session-end)",
		Long: `Called by Claude Code hooks; see 'beacon hooks print'. It records the event in
the shared state file and, if no beacon daemon or TUI is running, applies the
result to the light directly.

It always exits 0 and prints nothing to stdout, so it can never disrupt Claude
Code. Problems go to the log file in the state directory.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(c *cobra.Command, args []string) {
			runHook(c, args, dry)
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "fake devices and dry-run state; commands go to stderr")
	return cmd
}

func runHook(c *cobra.Command, args []string, dry bool) {
	start := time.Now()
	log := logging.Discard()
	if dir, err := paths.StateDir(); err == nil {
		if l, closer, err := logging.Open(dir); err == nil {
			log = l
			defer closer.Close()
		}
	}
	// A bug here must never reach Claude Code as a crash or a stack trace.
	defer func() {
		if r := recover(); r != nil {
			log.Error("hook panicked", "panic", fmt.Sprint(r))
		}
	}()
	if len(args) == 0 {
		log.Warn("hook called without an event")
		return
	}
	ev, ok := claude.ParseEvent(args[0])
	if !ok {
		log.Warn("hook: unknown event", "event", args[0])
		return
	}
	in := claude.ReadInput(stdinPayload(c.InOrStdin()))
	// Never log the payload itself; it can contain paths and prompt-adjacent text.
	log = log.With("event", string(ev), "session_id", in.SessionID)

	cfg, _, err := loadConfig()
	if err != nil {
		log.Error("config", "err", err)
		return
	}
	if cfg.Claude.Mode == "off" {
		return
	}
	ttl, err := time.ParseDuration(cfg.Claude.SessionTTL)
	if err != nil {
		log.Error("claude.session_ttl", "err", err)
		return
	}
	store, err := openStore(dry)
	if err != nil {
		log.Error("state dir", "err", err)
		return
	}
	now := time.Now()
	if err := store.Update(func(f *state.File) error {
		claude.Record(&f.Claude, ev, in, now, ttl)
		return nil
	}); err != nil {
		log.Error("state update", "err", err)
		return
	}
	if store.EngineRunning() {
		log.Info("recorded; engine will apply", "took", time.Since(start).String())
		return
	}
	if err := applyDirect(c, cfg, store, dry, log); err != nil {
		log.Error("direct apply", "err", err, "took", time.Since(start).String())
		return
	}
	log.Info("applied directly", "took", time.Since(start).String())
}

// applyDirect is the no-daemon path: resolve and push to the light now.
func applyDirect(c *cobra.Command, cfg config.Config, store *state.Store, dry bool, log *slog.Logger) error {
	f, err := store.Load()
	if err != nil {
		return err
	}
	in, err := engine.BuildInputs(cfg, f, time.Now(), engine.Env{Schedule: activeSchedule(cfg, dry, log), Caps: primaryCaps(cfg, dry)})
	if err != nil {
		return err
	}
	res := engine.Resolve(time.Now(), in)
	if res.Source == engine.SourceNone {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookApplyMax)
	defer cancel()
	b, err := newBackend(cfg, dry, c.ErrOrStderr()) // dry-run chatter must not hit stdout
	if err != nil {
		return err
	}
	devs, err := resolveTargets(ctx, b, cfg)
	if err != nil {
		return err
	}
	log.Debug("targets", "devices", describe(devs))
	return applyNow(ctx, b, devs, res.Layer.State, store)
}
