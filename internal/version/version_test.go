package version

import (
	"runtime/debug"
	"testing"
)

func build(main string, kv ...string) *debug.BuildInfo {
	b := &debug.BuildInfo{Main: debug.Module{Version: main}}
	for i := 0; i < len(kv); i += 2 {
		b.Settings = append(b.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return b
}

func TestString(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		b    *debug.BuildInfo
		want string
	}{
		{"release build", Info{"0.2.0", "abcdef1234567", "2026-10-26T01:02:03Z"}, nil, "0.2.0 (abcdef1, 2026-10-26)"},
		{"plain go build with no info", Info{}, nil, "dev"},
		{"go build in a checkout", Info{}, build("(devel)", "vcs.revision", "0123456789abcdef", "vcs.time", "2026-10-25T20:00:00Z"), "dev (0123456, 2026-10-25)"},
		{"checkout with uncommitted changes", Info{}, build("(devel)", "vcs.revision", "0123456789abcdef", "vcs.modified", "true"), "dev (0123456, dirty)"},
		{"checkout build with a pseudo-version", Info{}, build("v0.0.0-20261025233825-267ef3091cc4+dirty", "vcs.revision", "267ef3091cc4aaaa", "vcs.modified", "true"), "dev (267ef30, dirty)"},
		{"go install at a version", Info{}, build("v0.3.1"), "0.3.1"},
		{"ldflags win over build info", Info{"1.0.0", "aaaaaaa", ""}, build("v9.9.9", "vcs.revision", "bbbbbbb"), "1.0.0 (aaaaaaa)"},
		{"short commit kept as is", Info{"1.0.0", "abc", ""}, nil, "1.0.0 (abc)"},
	}
	for _, c := range cases {
		if got := String(c.in, c.b); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
