package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplaceOverwritesAndCreates(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")
	for i, content := range []string{"first", "second"} {
		tmp := filepath.Join(dir, "tmp")
		if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Replace(tmp, dst); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if b, _ := os.ReadFile(dst); string(b) != content {
			t.Fatalf("round %d: %q", i, b)
		}
		if _, err := os.Stat(tmp); err == nil {
			t.Fatal("the temp file should be gone after the rename")
		}
	}
}

// The Windows behavior, exercised here with a stand-in: a rename that keeps
// failing "transiently" is retried, then succeeds.
func TestReplaceRetriesTransientFailures(t *testing.T) {
	busy := errors.New("sharing violation")
	calls, slept := 0, 0
	old := sleep
	sleep = func(time.Duration) { slept++ }
	defer func() { sleep = old }()
	rename := func(a, b string) error {
		calls++
		if calls < 4 {
			return busy
		}
		return nil
	}
	if err := replace(rename, func(e error) bool { return e == busy }, "a", "b"); err != nil || calls != 4 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if slept != 3 {
		t.Fatalf("should wait between attempts, slept %d times", slept)
	}
}

func TestReplaceGivesUpAfterTheLastAttemptAndReturnsTheError(t *testing.T) {
	busy := errors.New("still busy")
	old := sleep
	sleep = func(time.Duration) {}
	defer func() { sleep = old }()
	calls := 0
	err := replace(func(a, b string) error { calls++; return busy }, func(error) bool { return true }, "a", "b")
	if !errors.Is(err, busy) || calls != attempts {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestReplaceDoesNotRetryRealErrors(t *testing.T) {
	boom := errors.New("no such file")
	calls := 0
	err := replace(func(a, b string) error { calls++; return boom }, func(error) bool { return false }, "a", "b")
	if err != boom || calls != 1 {
		t.Fatalf("a permanent error is returned immediately: calls=%d err=%v", calls, err)
	}
}
