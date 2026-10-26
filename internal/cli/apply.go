package cli

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/modes/schedule"

	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/paths"
	"github.com/adnair2024/beacon/internal/state"
)

// openStore returns the state store. Dry runs get their own subdirectory so
// a fake override can never leak into a real session.
func openStore(dry bool) (*state.Store, error) {
	d, err := paths.StateDir()
	if err != nil {
		return nil, err
	}
	if dry {
		d = filepath.Join(d, "dry-run")
	}
	return state.Open(d), nil
}

// applyNow sends st to the chosen devices and records it as last applied.
func applyNow(ctx context.Context, b *backend, devs []govee.Device, st govee.State, store *state.Store) error {
	var m govee.Multi
	for _, d := range devs {
		m = append(m, govee.Named{Name: d.Name, Light: b.light(d)})
	}
	if err := m.Apply(ctx, st); err != nil {
		return err
	}
	return store.Update(func(f *state.File) error { f.LastApplied = &st; return nil })
}

func fmtClock(t time.Time) string { return t.Local().Format("15:04") }

// activeSchedule loads the configured schedule profile for engine inputs. A
// missing file means the shipped default; an unreadable one is reported to
// log and treated as no schedule, so it can't take Claude status down with it.
func activeSchedule(cfg config.Config, dry bool, log *slog.Logger) *schedule.Profile {
	dir, err := scheduleDir(dry)
	if err != nil {
		return nil
	}
	p, _, err := schedule.Load(dir, cfg.Schedule.Profile)
	if err != nil {
		if log != nil {
			log.Warn("schedule", "err", err)
		}
		return nil
	}
	return &p
}

// scheduleLoader returns a loader for the engine loop. It re-reads the config
// each call so switching schedule.profile takes effect without a restart.
func scheduleLoader(dir string) func() (*schedule.Profile, error) {
	return func() (*schedule.Profile, error) {
		cfg, _, err := loadConfig()
		if err != nil {
			return nil, err
		}
		p, _, err := schedule.Load(dir, cfg.Schedule.Profile)
		if err != nil {
			return nil, err
		}
		return &p, nil
	}
}

// scheduleDir is where schedule JSON lives. Dry runs get their own
// subdirectory, like their own state directory, so --dry-run can never
// rewrite your real schedule.
func scheduleDir(dry bool) (string, error) {
	d, err := paths.SchedulesDir()
	if err != nil {
		return "", err
	}
	if dry {
		d = filepath.Join(d, ".dry-run")
	}
	return d, nil
}
