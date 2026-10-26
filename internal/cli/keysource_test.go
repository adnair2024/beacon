package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adnair2024/beacon/internal/envfile"
)

const testKey = "abcd1234-secret-wxyz"

// validate runs `beacon config validate` with a given .env result and key sources.
func validate(t *testing.T, e *env, res envfile.Result, shellKey string) (string, string) {
	t.Helper()
	t.Setenv("BEACON_CONFIG", e.cfgPath)
	t.Setenv("BEACON_STATE_DIR", e.stateDir)
	t.Setenv("GOVEE_API_KEY", shellKey)
	var out, errb bytes.Buffer
	root := NewRoot("test", res)
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"config", "validate"})
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, errb.String())
	}
	return out.String(), errb.String()
}

func TestKeySourceReportsWhereTheKeyCameFrom(t *testing.T) {
	cases := []struct {
		name  string
		cfg   string
		shell string
		res   envfile.Result
		want  string
	}{
		{"dotenv", "", testKey, envfile.Result{Set: []string{"GOVEE_API_KEY"}}, "api key: from .env (****wxyz)"},
		{"shell", "", testKey, envfile.Result{}, "api key: from the GOVEE_API_KEY environment variable (****wxyz)"},
		{"config", "api_key = \"" + testKey + "\"\n", "", envfile.Result{}, "api key: from config.toml (****wxyz)"},
		{"none", "", "", envfile.Result{}, "api key: not set"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "")
			if c.cfg != "" {
				os.WriteFile(e.cfgPath, []byte(c.cfg), 0o600)
			}
			out, errOut := validate(t, e, c.res, c.shell)
			if !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
			if strings.Contains(out+errOut, testKey) || strings.Contains(out+errOut, "abcd1234") {
				t.Fatal("the key (or its prefix) must never be printed")
			}
		})
	}
}

func TestDotenvWarnings(t *testing.T) {
	e := newEnv(t, "")
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("GOVEE_API_KEY="+testKey+"\n"), 0o644)
	res := envfile.Result{
		Path:    p,
		Skipped: []string{"PATH", "LD_PRELOAD"},
		Errors:  []error{os.ErrInvalid},
	}
	_, errOut := validate(t, e, res, "")
	for _, want := range []string{"warning: .env:", "PATH, LD_PRELOAD", "chmod 600"} {
		if os.PathSeparator == '/' || want != "chmod 600" {
			if !strings.Contains(errOut, want) {
				t.Errorf("missing %q in:\n%s", want, errOut)
			}
		}
	}
	if strings.Contains(errOut, testKey) {
		t.Fatal("warnings must not echo values")
	}
}
