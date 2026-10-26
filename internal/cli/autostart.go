package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/autostart"
	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/envfile"
)

// newAutostart is replaceable so tests don't touch systemd or Task Scheduler.
var newAutostart = autostart.New

func newAutostartCmd(loaded envfile.Result) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autostart",
		Short: "Start the beacon daemon at login",
		Long: `Installs (or removes) a login entry that runs 'beacon daemon': a systemd user
service on Linux, a Task Scheduler task on Windows. With the daemon running,
Claude Code hooks only record state and return instantly, so this is the
recommended way to run beacon day to day.

The daemon started at login does not see your shell's environment, so the API
key has to come from config.toml, or from a .env file in the directory you
install from.`,
	}

	var printOnly bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the login entry and start the daemon now",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}
			o, notes, err := autostartOptions(cfg, loaded)
			if err != nil {
				return err
			}
			b := newAutostart()
			if printOnly {
				text, err := b.Render(o)
				if err != nil {
					return err
				}
				c.Println(text)
				return nil
			}
			for _, n := range notes {
				c.PrintErrln("note:", n)
			}
			if err := b.Install(o); err != nil {
				return err
			}
			c.Printf("installed the %s: beacon daemon will start at login, and has been started now\n", b.Name())
			c.Println("check it with `beacon doctor`; stop and remove it with `beacon autostart remove`")
			return nil
		},
	}
	install.Flags().BoolVar(&printOnly, "print", false, "show what would be installed and change nothing")

	cmd.AddCommand(install,
		&cobra.Command{
			Use:   "remove",
			Short: "Stop the daemon and remove the login entry",
			Args:  cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				b := newAutostart()
				if err := b.Remove(); err != nil {
					return err
				}
				c.Printf("removed the %s\n", b.Name())
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Say whether the login entry is installed and running",
			Args:  cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				s, err := newAutostart().Status()
				if err != nil {
					return err
				}
				c.Println(s)
				return nil
			},
		})
	return cmd
}

// autostartOptions decides what the login entry should run, and refuses when
// the daemon it would start couldn't authenticate.
func autostartOptions(cfg config.Config, loaded envfile.Result) (autostart.Options, []string, error) {
	var notes []string
	exe, err := os.Executable()
	if err != nil {
		return autostart.Options{}, nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	o := autostart.Options{Exe: exe, Env: map[string]string{}}
	if strings.Contains(filepath.ToSlash(exe), "/go-build") {
		notes = append(notes, "this looks like a temporary `go run` build ("+exe+"); install the binary somewhere permanent first, or the login entry will break")
	}
	for _, k := range []string{"BEACON_CONFIG", "BEACON_STATE_DIR"} {
		if v := os.Getenv(k); v != "" {
			o.Env[k] = v // the daemon must use the same files this shell does
		}
	}

	src, _ := keySource(cfg, loaded)
	switch src {
	case "":
		return o, nil, errors.New("there's no API key to start the daemon with: put api_key = \"...\" in config.toml (see `beacon config path`) or in a .env file, then run this again")
	case "from the GOVEE_API_KEY environment variable":
		return o, nil, errors.New("your API key is only in the GOVEE_API_KEY environment variable, which a daemon started at login won't have. " +
			"Put it in config.toml as api_key = \"...\" (see `beacon config path`; the file is created 0600) or in a .env file in this directory, then run this again")
	case "from .env":
		wd, err := os.Getwd()
		if err != nil {
			return o, nil, err
		}
		o.WorkDir = wd
		notes = append(notes, fmt.Sprintf("your key comes from %s/.env, so the daemon will start in that directory to find it; if you move it, run this again. config.toml works from anywhere", wd))
	}
	return o, notes, nil
}
