//go:build windows

package fsutil

import (
	"errors"
	"syscall"
)

// Windows error numbers that mean "another process has the file open".
const (
	errAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	errSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
	errLockViolation    = syscall.Errno(33) // ERROR_LOCK_VIOLATION
)

func isTransient(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errAccessDenied || errno == errSharingViolation || errno == errLockViolation
}
