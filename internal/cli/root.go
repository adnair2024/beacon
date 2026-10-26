package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/envfile"
)

// NewRoot builds the command tree.
func NewRoot(version string, env ...envfile.Result) *cobra.Command {
	var loaded envfile.Result
	if len(env) > 0 {
		loaded = env[0]
	}
	root := &cobra.Command{
		Use:           "beacon",
		Short:         "Govee desk status beacon",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// cobra's Print* helpers fall back to stderr when no writer is set, which
	// would send every command's normal output to stderr. Output belongs on
	// stdout so it can be piped; diagnostics go to stderr explicitly.
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	var dry bool
	root.Flags().BoolVar(&dry, "dry-run", false, "fake devices and dry-run state (same as 'beacon tui --dry-run')")
	root.RunE = func(c *cobra.Command, _ []string) error { return runTUI(c, dry) }
	root.AddCommand(newSet(), newClear(), newPomo(), newSchedule(), newConfig(loaded), newDoctor(version, loaded), newAutostartCmd(loaded), newDevices(), newHook(), newHooks(), newDaemon(), newTUI(), &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run:   func(c *cobra.Command, _ []string) { c.Println(version) },
	})
	return root
}
