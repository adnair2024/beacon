package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/config"
	"github.com/adnair2024/beacon/internal/envfile"
	"github.com/adnair2024/beacon/internal/modes/schedule"
	"github.com/adnair2024/beacon/internal/paths"
)

func newConfig(env envfile.Result) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect the configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		RunE: func(c *cobra.Command, _ []string) error {
			p, err := paths.ConfigFile()
			if err == nil {
				c.Println(p)
			}
			return err
		},
	}, &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in your editor",
		Long: `Opens the config file in $VISUAL or $EDITOR (notepad on Windows, vi
elsewhere), creating it from the defaults first if it doesn't exist, and
validates it when you close the editor. The Colors tab in the TUI edits the
same file for the look settings.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			p, err := paths.ConfigFile()
			if err != nil {
				return err
			}
			if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
				if err := config.Save(p, config.Default()); err != nil {
					return err
				}
				c.PrintErrln("created", p, "from the defaults")
			}
			if err := runEditor(editorCommand(), p); err != nil {
				return err
			}
			cfg, warns, err := config.Load(p)
			if err != nil {
				return fmt.Errorf("%w\n(the file is still there; run `beacon config edit` to fix it)", err)
			}
			for _, w := range warns {
				c.PrintErrln("warning:", w)
			}
			if errs := cfg.Validate(); len(errs) > 0 {
				for _, e := range errs {
					c.PrintErrln("error:", e)
				}
				return fmt.Errorf("%d config error(s); run `beacon config edit` to fix them", len(errs))
			}
			c.Println("config ok")
			return nil
		},
	}, &cobra.Command{
		Use:   "validate",
		Short: "Validate the config file",
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, warns, err := loadConfig()
			if err != nil {
				return err
			}
			for _, w := range warns {
				c.PrintErrln("warning:", w)
			}
			reportKeySource(c, cfg, env)
			errs := cfg.Validate()
			if dir, err := paths.SchedulesDir(); err == nil && schedule.ValidName(cfg.Schedule.Profile) {
				switch p, _, err := schedule.Load(dir, cfg.Schedule.Profile); {
				case err != nil:
					errs = append(errs, err)
				default:
					errs = append(errs, p.Validate(cfg.UserPalette())...)
				}
			}
			for _, e := range errs {
				c.PrintErrln("error:", e)
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d config error(s)", len(errs))
			}
			c.Println("config ok")
			return nil
		},
	})
	return cmd
}

func loadConfig() (config.Config, []string, error) {
	p, err := paths.ConfigFile()
	if err != nil {
		return config.Config{}, nil, err
	}
	return config.Load(p)
}

// reportKeySource says where the API key comes from, masked, and flags
// problems with a .env file. It never prints a value.
func reportKeySource(c *cobra.Command, cfg config.Config, env envfile.Result) {
	for _, e := range env.Errors {
		c.PrintErrln("warning: .env:", e)
	}
	if len(env.Skipped) > 0 {
		c.PrintErrln("warning: .env: ignored variables beacon doesn't read:", strings.Join(env.Skipped, ", "))
	}
	if env.Path != "" && envfile.TooOpen(env.Path) {
		c.PrintErrln("warning: .env is readable by other users; run: chmod 600", env.Path)
	}
	src, masked := keySource(cfg, env)
	if src == "" {
		c.Println("api key: not set (use GOVEE_API_KEY, a .env file, or api_key in config.toml)")
		return
	}
	c.Printf("api key: %s (%s)\n", src, masked)
}

// keySource says where the API key came from and shows it masked; src is ""
// when there is no key. It never returns the key itself.
func keySource(cfg config.Config, env envfile.Result) (src, masked string) {
	key := cfg.Key()
	switch {
	case key == "":
		return "", ""
	case slices.Contains(env.Set, "GOVEE_API_KEY"):
		src = "from .env"
	case os.Getenv("GOVEE_API_KEY") != "":
		src = "from the GOVEE_API_KEY environment variable"
	default:
		src = "from config.toml"
	}
	return src, config.MaskKey(key)
}

// editorCommand picks the editor: $VISUAL, then $EDITOR, then a platform default.
func editorCommand() []string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return strings.Fields(v) // "code -w" and the like
		}
	}
	return []string{defaultEditor}
}

// runEditor runs the editor on path with the terminal attached. Tests replace it.
var runEditor = func(editor []string, path string) error {
	cmd := exec.Command(editor[0], append(editor[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running %s: %w", editor[0], err)
	}
	return nil
}
