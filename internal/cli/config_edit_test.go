package cli

import (
	"os"
	"strings"
	"testing"
)

func fakeEditor(t *testing.T, edit func(path string)) *[]string {
	t.Helper()
	var calls []string
	old := runEditor
	runEditor = func(editor []string, path string) error {
		calls = append(calls, strings.Join(editor, " ")+" "+path)
		if edit != nil {
			edit(path)
		}
		return nil
	}
	t.Cleanup(func() { runEditor = old })
	return &calls
}

func TestEditorCommandPrecedence(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := editorCommand(); len(got) != 1 || got[0] != defaultEditor {
		t.Fatal(got)
	}
	t.Setenv("EDITOR", "nano")
	if got := editorCommand(); got[0] != "nano" {
		t.Fatal(got)
	}
	t.Setenv("VISUAL", "code -w")
	if got := editorCommand(); strings.Join(got, " ") != "code -w" {
		t.Fatalf("$VISUAL wins and may carry arguments: %v", got)
	}
}

func TestConfigEditCreatesThenValidates(t *testing.T) {
	e := newEnv(t, "")
	os.Remove(e.cfgPath) // no config yet
	calls := fakeEditor(t, nil)
	out, errOut, err := e.run("", "config", "edit")
	if err != nil || !strings.Contains(out, "config ok") || !strings.Contains(errOut, "created") {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0], e.cfgPath) {
		t.Fatalf("%v", *calls)
	}
	if _, err := os.Stat(e.cfgPath); err != nil {
		t.Fatal("the editor should open an existing file with the defaults in it")
	}
}

func TestConfigEditReportsWhatYouBroke(t *testing.T) {
	e := newEnv(t, "")
	fakeEditor(t, func(p string) { os.WriteFile(p, []byte("[claude]\nmode = \"bogus\"\n"), 0o600) })
	_, errOut, err := e.run("", "config", "edit")
	if err == nil || !strings.Contains(errOut, "claude.mode") || !strings.Contains(err.Error(), "config edit") {
		t.Fatalf("%q %v", errOut, err)
	}
	fakeEditor(t, func(p string) { os.WriteFile(p, []byte("not = [toml"), 0o600) })
	if _, _, err := e.run("", "config", "edit"); err == nil || !strings.Contains(err.Error(), "still there") {
		t.Fatalf("%v", err)
	}
}
