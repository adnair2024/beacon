package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/doctor"
	"github.com/adnair2024/beacon/internal/envfile"
	"github.com/adnair2024/beacon/internal/govee"
	"github.com/adnair2024/beacon/internal/govee/cloud"
	"github.com/adnair2024/beacon/internal/paths"
)

// claudeSettingsFiles are the places Claude Code reads hooks from that beacon
// can reasonably expect to be in: the user's settings (honoring
// CLAUDE_CONFIG_DIR) and the current project's.
func claudeSettingsFiles() []doctor.SettingsFile {
	var files []string
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		files = append(files, filepath.Join(d, "settings.json"))
	} else if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	files = append(files, filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json"))
	out := make([]doctor.SettingsFile, 0, len(files))
	for _, p := range files {
		f := doctor.SettingsFile{Path: p}
		switch data, err := os.ReadFile(p); {
		case err == nil:
			f.Exists, f.Data = true, data
		case !errors.Is(err, os.ErrNotExist):
			f.Exists, f.ReadErr = true, err
		}
		out = append(out, f)
	}
	return out
}

func newDoctor(version string, loaded envfile.Result) *cobra.Command {
	var offline, dry bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check your setup and say what to fix",
		Long: `Checks the config, API key and .env, the state directory, the schedule file,
status-color clashes, the connection to Govee and each configured device
(capabilities, online), Claude Code's hooks (installed, matched, pointing at
this binary), and whether a daemon is running. It prints what it found with
what to run to fix it, and exits non-zero if anything is broken.

--offline skips everything that talks to Govee.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfgPath, err := paths.ConfigFile()
			if err != nil {
				return err
			}
			cfg, warns, cfgErr := config.Load(cfgPath)
			_, statErr := os.Stat(cfgPath)
			store, err := openStore(dry)
			if err != nil {
				return err
			}
			stateDir, _ := paths.StateDir()
			if dry {
				stateDir = filepath.Join(stateDir, "dry-run")
			}
			schedDir, _ := scheduleDir(dry)
			exe, _ := os.Executable()
			src, masked := keySource(cfg, loaded)

			e := doctor.Env{
				Cfg: cfg, CfgPath: cfgPath, CfgFound: statErr == nil, CfgErr: cfgErr, Warnings: warns,
				EnvFile: loaded, KeySource: src, KeyMasked: masked,
				StateDir: stateDir, Store: store, SchedDir: schedDir,
				Offline: offline, Exe: exe, GOOS: runtime.GOOS,
				Claude: claudeSettingsFiles(), LookPath: exec.LookPath,
			}
			if !offline {
				if b, err := newBackend(cfg, dry, io.Discard); err == nil {
					e.List = b.list
					e.Online = func(ctx context.Context, d govee.Device) error {
						_, err := b.light(d).Status(ctx)
						if errors.Is(err, cloud.ErrOffline) {
							return cloud.ErrOffline
						}
						return err
					}
				}
			}

			results := doctor.Run(c.Context(), e)
			c.Printf("beacon doctor · %s · %s/%s\n\n", version, runtime.GOOS, runtime.GOARCH)
			for _, r := range results {
				c.Printf("[%s] %-14s %s\n", r.Status.Label(), r.Name, r.Detail)
				for _, h := range r.Hints {
					c.Printf("%s-> %s\n", strings.Repeat(" ", 23), h)
				}
			}
			fails, warnings := doctor.Summary(results)
			c.Println()
			switch {
			case fails > 0:
				return fmt.Errorf("%d problem%s found, %d warning%s", fails, s(fails), warnings, s(warnings))
			case warnings > 0:
				c.Printf("no problems, %d warning%s\n", warnings, s(warnings))
			default:
				c.Println("everything looks good")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the checks that need the network")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "check against fake devices and the dry-run state")
	return cmd
}

func s(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
