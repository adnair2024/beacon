// Package state is the on-disk state shared between beacon processes (the
// TUI/daemon and short-lived commands like hooks). Writers hold an exclusive
// lock and replace the file atomically, so readers never see a torn file.
package state
