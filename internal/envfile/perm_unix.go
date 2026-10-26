//go:build !windows

package envfile

import "os"

// TooOpen reports whether path is readable by group or others. A .env holds a
// credential, so it should be 0600.
func TooOpen(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().Perm()&0o077 != 0
}
