package paths

import (
	"path/filepath"
	"testing"
)

func TestSchedulesDirFollowsTheConfigFile(t *testing.T) {
	t.Setenv("BEACON_CONFIG", "/x/y/config.toml")
	got, err := SchedulesDir()
	if err != nil || got != filepath.FromSlash("/x/y/schedules") {
		t.Fatal(got, err)
	}
}
