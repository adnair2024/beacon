//go:build !windows

package fsutil

// isTransient: on Unix an open file never blocks a rename over it, so every
// error is final.
func isTransient(error) bool { return false }
