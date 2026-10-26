package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/adnair2024/beacon/internal/color"
	"github.com/adnair2024/beacon/internal/govee"
)

func TestMissingIsEmpty(t *testing.T) {
	for _, dir := range []string{filepath.Join(t.TempDir(), "nope"), t.TempDir()} {
		f, err := Open(dir).Load()
		if err != nil || f.Version != Version || f.Override != nil {
			t.Fatal(f, err)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "sub"))
	c := color.RGB{R: 255, G: 140}
	exp := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	err := s.Update(func(f *File) error {
		f.Override = &Override{State: govee.State{On: true, Brightness: 80, Color: &c}, ExpiresAt: exp}
		f.Claude.Sessions = map[string]Session{"a": {State: "working", Updated: exp}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Load()
	if err != nil || f.Override == nil || *f.Override.State.Color != c || !f.Override.ExpiresAt.Equal(exp) || f.Claude.Sessions["a"].State != "working" {
		t.Fatalf("%+v %v", f, err)
	}
	if runtimeUnix() {
		if fi, _ := os.Stat(filepath.Join(s.dir, "state.json")); fi.Mode().Perm() != 0o600 {
			t.Fatalf("state.json mode %v", fi.Mode())
		}
	}
}

func runtimeUnix() bool { return os.PathSeparator == '/' }

func TestFnErrorWritesNothing(t *testing.T) {
	s := Open(t.TempDir())
	s.Update(func(f *File) error { f.Pomo = &Pomo{Phase: "focus"}; return nil })
	err := s.Update(func(f *File) error { f.Pomo = nil; return fmt.Errorf("boom") })
	if err == nil {
		t.Fatal("want error")
	}
	if f, _ := s.Load(); f.Pomo == nil {
		t.Fatal("failed update must not be persisted")
	}
}

// The hook path: many short-lived writers must not lose each other's updates.
func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	s := Open(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Update(func(f *File) error {
				if f.Claude.Sessions == nil {
					f.Claude.Sessions = map[string]Session{}
				}
				f.Claude.Sessions[fmt.Sprint("s", i)] = Session{State: "working"}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, err := s.Load()
	if err != nil || len(f.Claude.Sessions) != 20 {
		t.Fatalf("got %d sessions, err %v", len(f.Claude.Sessions), err)
	}
}

func TestCorruptFileRecovered(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600)
	s := Open(dir)
	if _, err := s.Load(); err == nil {
		t.Fatal("Load should report corruption")
	}
	if err := s.Update(func(f *File) error { f.Pomo = &Pomo{Phase: "focus"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json.corrupt")); err != nil {
		t.Fatal("bad file should be kept aside")
	}
	if f, err := s.Load(); err != nil || f.Pomo == nil {
		t.Fatal(f, err)
	}
}

func TestNewerVersionRefused(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":99}`), 0o600)
	if _, err := Open(dir).Load(); err == nil {
		t.Fatal("should refuse a newer schema")
	}
}

func TestEngineLockIsSingleInstance(t *testing.T) {
	s := Open(t.TempDir())
	if s.EngineRunning() {
		t.Fatal("nothing is running yet")
	}
	release, err := s.AcquireEngine()
	if err != nil {
		t.Fatal(err)
	}
	if !s.EngineRunning() {
		t.Fatal("lock is held; EngineRunning must say so")
	}
	if _, err := s.AcquireEngine(); !errors.Is(err, ErrEngineRunning) {
		t.Fatalf("second acquire: %v", err)
	}
	if s.EnginePID() != strconv.Itoa(os.Getpid()) {
		t.Fatal(s.EnginePID())
	}
	release()
	if s.EngineRunning() {
		t.Fatal("released lock should read as not running")
	}
	if _, err := s.AcquireEngine(); err != nil {
		t.Fatal("slot should be reusable", err)
	}
}
