package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigFile returns the config.toml path. BEACON_CONFIG overrides it.
func ConfigFile() (string, error) {
	if p := os.Getenv("BEACON_CONFIG"); p != "" {
		return p, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("paths: config dir: %w", err)
	}
	return filepath.Join(d, "beacon", "config.toml"), nil
}

// StateDir returns the directory for state.json, locks and logs.
// BEACON_STATE_DIR overrides it; on Linux XDG_STATE_HOME is preferred over
// the cache dir because state.json is not disposable.
func StateDir() (string, error) {
	if p := os.Getenv("BEACON_STATE_DIR"); p != "" {
		return p, nil
	}
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return filepath.Join(p, "beacon"), nil
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("paths: state dir: %w", err)
	}
	return filepath.Join(d, "beacon"), nil
}

// SchedulesDir is where schedule profiles (JSON) live: a schedules/
// directory next to the config file, so BEACON_CONFIG moves both.
func SchedulesDir() (string, error) {
	cfg, err := ConfigFile()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cfg), "schedules"), nil
}
