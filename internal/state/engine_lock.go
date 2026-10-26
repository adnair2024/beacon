package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrEngineRunning means another TUI/daemon already drives the light.
var ErrEngineRunning = errors.New("another beacon (tui or daemon) is already running")

func (s *Store) engineLock() string { return filepath.Join(s.dir, "engine.lock") }

// AcquireEngine claims the single engine slot until release is called. It's
// an flock, so the OS drops it if the process dies; no stale pidfiles.
func (s *Store) AcquireEngine() (release func(), err error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	l := lockAt(s.engineLock())
	ok, err := l.TryLock()
	if err != nil {
		return nil, fmt.Errorf("state: engine lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w (pid %s)", ErrEngineRunning, s.EnginePID())
	}
	// Informational only (doctor, error messages); the lock is the truth.
	_ = os.WriteFile(filepath.Join(s.dir, "engine.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	return func() { _ = l.Unlock() }, nil
}

// EngineRunning reports whether some process holds the engine slot. It's
// what a hook uses to decide between "just exit" and "apply directly".
func (s *Store) EngineRunning() bool {
	if _, err := os.Stat(s.engineLock()); err != nil {
		return false
	}
	l := lockAt(s.engineLock())
	ok, err := l.TryLock()
	if err != nil {
		return false
	}
	if ok {
		_ = l.Unlock()
		return false
	}
	return true
}

// EnginePID is best effort and may be "unknown".
func (s *Store) EnginePID() string {
	b, err := os.ReadFile(filepath.Join(s.dir, "engine.pid"))
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}
