package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/adnair2024/beacon/internal/fsutil"
	"github.com/adnair2024/beacon/internal/govee"
)

// Version is the schema version written to state.json.
const Version = 1

// Session is one Claude Code session as last reported by a hook.
type Session struct {
	State   string    `json:"state"` // working | waiting | ready
	Updated time.Time `json:"updated"`
	Cwd     string    `json:"cwd,omitempty"`
}

// Claude holds the hook-reported sessions plus the aggregate that writers
// maintain: Status is waiting > working > ready ("" with no sessions) and
// Changed is when it last changed, which drives ready_hold and accent mode.
type Claude struct {
	Sessions map[string]Session `json:"sessions,omitempty"`
	Status   string             `json:"status,omitempty"`
	Changed  time.Time          `json:"changed,omitzero"`
}

// Pomo is persisted so closing and reopening the TUI resumes the session.
// Times are wall-clock, never tick counts, so it survives sleep and restarts;
// see package pomo for the state machine.
type Pomo struct {
	Phase           string    `json:"phase"`           // idle | focus | short_break | long_break
	State           string    `json:"state,omitempty"` // running | paused | waiting
	StartedAt       time.Time `json:"started_at,omitzero"`
	EndsAt          time.Time `json:"ends_at,omitzero"`              // running
	PausedRemaining int64     `json:"paused_remaining_ms,omitempty"` // paused
	CompletedFocus  int       `json:"completed_focus"`
	Next            string    `json:"next,omitempty"`         // waiting: the phase that starts on the next keypress
	WaitingSince    time.Time `json:"waiting_since,omitzero"` // waiting: when the previous phase ended
}

// Override is a manual `beacon set`. A zero ExpiresAt means until cleared.
type Override struct {
	State     govee.State `json:"state"`
	ExpiresAt time.Time   `json:"expires_at,omitzero"`
}

// File is the whole state.json document.
type File struct {
	Version     int          `json:"version"`
	Claude      Claude       `json:"claude"`
	Pomo        *Pomo        `json:"pomo,omitempty"`
	Override    *Override    `json:"override,omitempty"`
	LastApplied *govee.State `json:"last_applied,omitempty"`
}

// ErrCorrupt is returned by Load for a file that isn't valid state JSON.
// Update recovers from it by moving the bad file aside.
var ErrCorrupt = errors.New("state: corrupt state file")

// Store reads and writes <dir>/state.json.
type Store struct{ dir string }

// Open returns a Store rooted at dir. Nothing is created until a write.
func Open(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path() string { return filepath.Join(s.dir, "state.json") }

func lockAt(path string) *flock.Flock { return flock.New(path) }

func (s *Store) lock() *flock.Flock { return lockAt(filepath.Join(s.dir, "state.json.lock")) }

func (s *Store) read() (File, error) {
	b, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return File{Version: Version}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("state: read: %w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if f.Version > Version {
		return File{}, fmt.Errorf("state: written by a newer beacon (version %d)", f.Version)
	}
	f.Version = Version
	return f, nil
}

// Load returns the current state under a shared lock. A missing file is an
// empty state. The directory may not exist yet, in which case nothing can be
// locked and nothing has been written.
func (s *Store) Load() (File, error) {
	if _, err := os.Stat(s.dir); errors.Is(err, os.ErrNotExist) {
		return File{Version: Version}, nil
	}
	l := s.lock()
	if err := l.RLock(); err != nil {
		return File{}, fmt.Errorf("state: lock: %w", err)
	}
	defer l.Unlock()
	return s.read()
}

// Update runs fn on the current state under an exclusive lock and writes the
// result atomically (temp file + rename). If fn errors nothing is written.
func (s *Store) Update(fn func(*File) error) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	l := s.lock()
	if err := l.Lock(); err != nil {
		return fmt.Errorf("state: lock: %w", err)
	}
	defer l.Unlock()

	f, err := s.read()
	if errors.Is(err, ErrCorrupt) {
		// Keep the evidence, start clean: a hook must not be wedged by a bad file.
		_ = os.Rename(s.path(), s.path()+".corrupt")
		f, err = File{Version: Version}, nil
	}
	if err != nil {
		return err
	}
	if err := fn(&f); err != nil {
		return err
	}
	f.Version = Version
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("state: encode: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("state: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("state: write: %w", err)
	}
	if err := fsutil.Replace(tmp.Name(), s.path()); err != nil {
		return fmt.Errorf("state: replace: %w", err)
	}
	return nil
}

// Path is the state.json location, for watchers.
func (s *Store) Path() string { return s.path() }
