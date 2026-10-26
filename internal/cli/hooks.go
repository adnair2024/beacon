package cli

import (
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/adnair2024/beacon/internal/modes/claude"
)

func newHooks() *cobra.Command {
	cmd := &cobra.Command{Use: "hooks", Short: "Claude Code hook setup"}
	var (
		windows       bool
		path          string
		pre           bool
		noStopFailure bool
	)
	print := &cobra.Command{
		Use:   "print",
		Short: "Print the Claude Code settings.json hooks snippet",
		Long: `Print a "hooks" block to merge into ~/.claude/settings.json (the VS Code
extension reads the same file). By default the command uses the full path of
this binary, since the shell Claude Code uses may not have beacon on PATH.
With --windows, pass --path to the beacon.exe location if you are generating
the snippet on another machine.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			bin := path
			if bin == "" {
				switch exe, err := os.Executable(); {
				case windows && !strings.HasSuffix(strings.ToLower(exe), ".exe"):
					bin = `C:\path\to\beacon.exe` // generating for another machine
					c.PrintErrln("note: replace the placeholder path with your beacon.exe location, or pass --path")
				case err == nil:
					bin = exe
				default:
					if p, lerr := exec.LookPath("beacon"); lerr == nil {
						bin = p
					} else {
						bin = "beacon"
					}
				}
			}
			if windows {
				// Forward slashes work in every shell Claude Code might use on
				// Windows; backslashes get eaten as escapes by bash.
				bin = strings.ReplaceAll(bin, `\`, "/")
			}
			out, err := claude.Snippet(bin, claude.SnippetOptions{PreToolUse: pre, StopFailure: !noStopFailure})
			if err != nil {
				return err
			}
			c.Println(string(out))
			return nil
		},
	}
	print.Flags().BoolVar(&windows, "windows", false, "emit a Windows-style command (full path to beacon.exe)")
	print.Flags().StringVar(&path, "path", "", "path to the beacon binary to put in the commands")
	print.Flags().BoolVar(&pre, "pre-tool-use", false, "also hook PreToolUse (re-asserts 'working' after a permission prompt)")
	print.Flags().BoolVar(&noStopFailure, "no-stop-failure", false, "leave out StopFailure (needs a recent Claude Code)")
	cmd.AddCommand(print)
	return cmd
}
