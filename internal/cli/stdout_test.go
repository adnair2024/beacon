package cli

import (
	"io"
	"os"
	"testing"
)

// Normal output must go to stdout so it can be piped. Every other test hands
// the root its own writers, which hides this; here nothing is injected.
func TestOutputGoesToStdoutByDefault(t *testing.T) {
	capture := func(f **os.File) (restore func(), read func() string) {
		old := *f
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		*f = w
		return func() { *f = old }, func() string {
			w.Close()
			b, _ := io.ReadAll(r)
			return string(b)
		}
	}
	restoreOut, readOut := capture(&os.Stdout)
	restoreErr, readErr := capture(&os.Stderr)
	t.Setenv("BEACON_CONFIG", t.TempDir()+"/c.toml")
	t.Setenv("BEACON_STATE_DIR", t.TempDir())
	t.Setenv("GOVEE_API_KEY", "")

	root := NewRoot("9.9.9")
	root.SetArgs([]string{"version"})
	err := root.Execute()
	restoreOut()
	restoreErr()
	out, errOut := readOut(), readErr()
	if err != nil || out != "9.9.9\n" || errOut != "" {
		t.Fatalf("version should print to stdout only: err=%v stdout=%q stderr=%q", err, out, errOut)
	}
}
