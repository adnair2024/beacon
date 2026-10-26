package fsutil

import (
	"os"
	"time"
)

const (
	attempts = 25
	delay    = 20 * time.Millisecond
)

// sleep is a variable so tests don't actually wait.
var sleep = func(d time.Duration) { time.Sleep(d) }

// replace renames tmp over dst, retrying while the failure is one that an
// open handle in another process can cause. On Windows that means "access
// denied" and "sharing violation": a reader that has dst open for a moment
// blocks the rename, and it goes away on its own, so waiting up to ~half a
// second is right. Any other error is returned at once.
func replace(rename func(oldpath, newpath string) error, transient func(error) bool, tmp, dst string) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = rename(tmp, dst); err == nil || !transient(err) {
			return err
		}
		sleep(delay)
	}
	return err
}

// Replace atomically moves tmp over dst (creating dst if needed).
func Replace(tmp, dst string) error { return replace(os.Rename, isTransient, tmp, dst) }
