//go:build windows

package envfile

// TooOpen is always false on Windows, where Unix mode bits don't describe
// access; the user-profile ACL protects the file.
func TooOpen(string) bool { return false }
