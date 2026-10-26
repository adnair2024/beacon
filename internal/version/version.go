// Package version formats beacon's version string from what the release build
// stamps in (via -ldflags) or, failing that, what the Go toolchain recorded in
// the binary, so `go install` and `go build` from a checkout identify
// themselves too.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// pseudo matches the pseudo-versions the toolchain stamps on builds from a
// checkout (v0.0.0-20261025233825-267ef3091cc4, possibly +dirty). They aren't
// releases, so they're not shown as the version.
var pseudo = regexp.MustCompile(`-\d{14}-[0-9a-f]{12}`)

// Info is the stamped build information; any field may be empty.
type Info struct {
	Version string // "0.2.0", or "dev" when built without ldflags
	Commit  string
	Date    string
}

// String renders "0.2.0 (abc1234, 2026-10-26)", "dev (abc1234, dirty)" and so on.
// build is the toolchain's record of the binary (debug.ReadBuildInfo), or nil.
func String(i Info, build *debug.BuildInfo) string {
	v, commit, date, dirty := i.Version, i.Commit, i.Date, false
	if v == "" {
		v = "dev"
	}
	if build != nil {
		for _, s := range build.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == "" {
					commit = s.Value
				}
			case "vcs.time":
				if date == "" {
					date = s.Value
				}
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		// `go install github.com/.../beacon@v0.2.0` records the module version.
		if mv := build.Main.Version; v == "dev" && mv != "" && mv != "(devel)" && !pseudo.MatchString(mv) {
			v = strings.TrimPrefix(mv, "v")
		}
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if i := strings.IndexByte(date, 'T'); i > 0 { // 2026-10-26T01:02:03Z -> 2026-10-26
		date = date[:i]
	}
	var parts []string
	if commit != "" {
		parts = append(parts, commit)
	}
	if dirty {
		parts = append(parts, "dirty")
	}
	if date != "" {
		parts = append(parts, date)
	}
	if len(parts) == 0 {
		return v
	}
	return v + " (" + strings.Join(parts, ", ") + ")"
}

// Current formats the running binary's version.
func Current(i Info) string {
	b, _ := debug.ReadBuildInfo()
	return String(i, b)
}
