// Package fsutil holds the small filesystem helpers whose behavior differs by
// OS. Everything beacon writes is written to a temp file and renamed over the
// target; Replace is that last step.
package fsutil
